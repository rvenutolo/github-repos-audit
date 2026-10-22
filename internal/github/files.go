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

// Probe is what the collector looks for in a repository's files beyond the
// facts it always records. It comes from repos.toml, which is why it is
// handed in rather than known here: the names it carries are configuration.
type Probe struct {
	// Lint is the hermetic-lint command name. Where flake.nix and the gate
	// files mention it is recorded on audit.Flake; empty records neither.
	Lint string
}

// Where filesQuery's directories live, joined to an entry's name to give the
// path the facts record.
const (
	workflowsDir = ".github/workflows/"
	actionsDir   = ".github/actions/"
	ciDir        = ".ci/"
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
func recordFiles(repo *audit.Repo, f *repoFiles, probe Probe) {
	repo.Workflows = workflowFacts(f.Workflows)
	repo.Actions = compositeFacts(f.Actions)
	repo.Flake = flakeFacts(f, probe)
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

// flakeFacts records what flake.nix and flake.lock say, and which files name
// probe.Lint (a mention only in a full-line comment does not count; see
// namesOutsideComments). flake.lock's existence is a fact even when its text cannot be
// read, so HasLock is set either way and LockError says why the rest is
// missing. A flake.nix not read in full is recorded the same way, through
// NixError, so a check reading it fails as unreadable instead of on facts
// that were never read. A missing flake.nix records nothing: Files.FlakeNix
// already says it is absent, and absence is not a read failure.
func flakeFacts(f *repoFiles, probe Probe) audit.Flake {
	var flake audit.Flake
	if f.FlakeNix != nil {
		text, problem := blobText(f.FlakeNix, "flake.nix")
		if problem == "" {
			flake.Inputs = nixflake.Inputs(text)
			flake.NamesLint = probe.Lint != "" && namesOutsideComments(text, probe.Lint)
		}
		flake.NixError = problem
	}
	if f.FlakeLock != nil {
		flake.HasLock = true
		text, problem := blobText(f.FlakeLock, "flake.lock")
		if problem == "" {
			var facts nixflake.LockFacts
			facts, problem = nixflake.ParseLock(text)
			flake.LockedInputs = facts.LockedInputs
			flake.NixpkgsLastModified = facts.NixpkgsLastModified
			flake.AllRevPinned = facts.AllRevPinned
		}
		flake.LockError = problem
	}
	flake.GateFiles = gateFiles(f, probe.Lint)
	return flake
}

// gateFiles returns the path of every gate file whose text names lint outside
// a full-line comment, sorted: run-all-checks, the justfile and each file
// directly under .ci. Naming the
// lint shows the wiring exists, not that the gate reaches it; that is as far
// as reading text can go. A gate file not read in full is passed over.
func gateFiles(f *repoFiles, lint string) []string {
	if lint == "" {
		return nil
	}
	candidates := []blobEntry{
		{Name: "run-all-checks", Object: f.RunAllChecks},
		{Name: "justfile", Object: f.Justfile},
		{Name: ".justfile", Object: f.DotJustfile},
		{Name: "Justfile", Object: f.CapitalJustfile},
	}
	if f.CI != nil {
		for _, e := range f.CI.Entries {
			if e.Type == "blob" {
				candidates = append(candidates, blobEntry{Name: ciDir + e.Name, Object: e.Object})
			}
		}
	}
	var out []string
	for _, c := range candidates {
		if text, problem := blobText(c.Object, c.Name); problem == "" && namesOutsideComments(text, lint) {
			out = append(out, c.Name)
		}
	}
	slices.Sort(out)
	return out
}

// namesOutsideComments reports whether some line of text whose first
// non-whitespace character is not '#' contains name. Both shell and Nix
// start a comment with '#', so a full-line comment is wiring that does not
// run. A trailing comment is not stripped: telling a '#' that starts a
// comment from one inside a string or a URL needs a shell or Nix parser, and
// a mention before a trailing comment is far more often live than not.
// Trimming whitespace also drops the '\r' of a CRLF line ending.
func namesOutsideComments(text, name string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, name) {
			return true
		}
	}
	return false
}
