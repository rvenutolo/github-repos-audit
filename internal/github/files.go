package github

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/nixflake"
	"github.com/rvenutolo/github-repos-audit/internal/workflows"
)

// Where filesQuery's directories live, joined to an entry's name to give the
// path the facts record.
const (
	workflowsDir = ".github/workflows/"
	actionsDir   = ".github/actions/"
)

// fetchFiles runs filesQuery for one repository.
func (c *Client) fetchFiles(ctx context.Context, name string) (*repoFiles, error) {
	var data struct {
		Repository *repoFiles `json:"repository"`
	}
	vars := map[string]string{"owner": c.owner, "name": name}
	if err := c.postGraphQL(ctx, filesQuery, vars, &data); err != nil {
		return nil, err
	}
	if data.Repository == nil {
		return nil, fmt.Errorf("%s: %w: no repository in the file query's response", graphQLPath, errGraphQL)
	}
	return data.Repository, nil
}

// recordFiles turns filesQuery's answer into the workflow, composite-action
// and flake facts. It parses what it is given and judges none of it.
func recordFiles(repo *audit.Repo, f *repoFiles) {
	repo.Workflows = workflowFacts(f.Workflows)
	repo.Actions = compositeFacts(f.Actions)
	repo.Flake = flakeFacts(f)
}

// blobText returns a file's whole text, or why it has none: a binary file has
// no text, and a truncated one has only part of it. Partial text is never
// handed on. A check that read the first half of a file could pass on that
// half alone, so a file not read in full is recorded as unreadable, or as
// saying nothing, and either way the check that needed it fails.
func blobText(b *fileBlob, path string) (text, problem string) {
	switch {
	case b == nil || b.IsBinary || b.Text == nil:
		return "", path + ": not a text file"
	case b.IsTruncated:
		return "", path + ": too large to read in full"
	}
	return *b.Text, ""
}

// workflowFacts parses every .yml or .yaml file in .github/workflows, which
// are the only names GitHub runs as workflows, sorted by path. A file that
// cannot be read in full is recorded with ParseError like one that does not
// parse, so every check reading it fails as unreadable.
func workflowFacts(t *blobTree) []audit.Workflow {
	if t == nil {
		return nil
	}
	var out []audit.Workflow
	for _, e := range t.Entries {
		if e.Type != "blob" || !strings.HasSuffix(e.Name, ".yml") && !strings.HasSuffix(e.Name, ".yaml") {
			continue
		}
		path := workflowsDir + e.Name
		text, problem := blobText(e.Object, path)
		if problem != "" {
			out = append(out, audit.Workflow{Path: path, ParseError: problem})
			continue
		}
		out = append(out, workflows.ParseWorkflow(path, text))
	}
	slices.SortFunc(out, func(a, b audit.Workflow) int { return cmp.Compare(a.Path, b.Path) })
	return out
}

// compositeFacts parses the action file of every directory in
// .github/actions, sorted by path. action.yml is preferred to action.yaml, as
// the Actions runner prefers it; a directory with neither is not an action
// and is skipped.
func compositeFacts(t *actionsTree) []audit.CompositeAction {
	if t == nil {
		return nil
	}
	var out []audit.CompositeAction
	for _, dir := range t.Entries {
		if dir.Type != "tree" || dir.Object == nil {
			continue
		}
		file, ok := actionFile(dir.Object)
		if !ok {
			continue
		}
		path := actionsDir + dir.Name + "/" + file.Name
		text, problem := blobText(file.Object, path)
		if problem != "" {
			out = append(out, audit.CompositeAction{Path: path, ParseError: problem})
			continue
		}
		out = append(out, workflows.ParseComposite(path, text))
	}
	slices.SortFunc(out, func(a, b audit.CompositeAction) int { return cmp.Compare(a.Path, b.Path) })
	return out
}

// actionFile returns an action directory's metadata file.
func actionFile(dir *blobTree) (blobEntry, bool) {
	for _, name := range []string{"action.yml", "action.yaml"} {
		for _, e := range dir.Entries {
			if e.Type == "blob" && e.Name == name {
				return e, true
			}
		}
	}
	return blobEntry{}, false
}

// flakeFacts records what flake.lock says. Its existence is a fact even when
// its text cannot be read, so HasLock is set either way and LockError says why
// the rest is missing.
func flakeFacts(f *repoFiles) audit.Flake {
	var flake audit.Flake
	if f.FlakeLock != nil {
		flake.HasLock = true
		text, problem := blobText(f.FlakeLock, "flake.lock")
		if problem == "" {
			var facts nixflake.LockFacts
			facts, problem = nixflake.ParseLock(text)
			flake.NixpkgsLastModified = facts.NixpkgsLastModified
			flake.AllRevPinned = facts.AllRevPinned
		}
		flake.LockError = problem
	}
	return flake
}
