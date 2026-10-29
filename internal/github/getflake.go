package github

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/filepaths"
	"github.com/rvenutolo/github-repos-audit/internal/getflake"
)

// getFlakeFilesMax bounds the files read for builtins.getFlake calls: every
// Nix file and every file under .ci/, together. Past it nothing is read and
// the repository is recorded as having too many, for the reason claudeFilesMax
// gives. The workflow and action texts are not counted: filesQuery has
// already fetched them.
const getFlakeFilesMax = 200

// ciDir is the root directory whose files are scanned for calls: the scripts
// a Nix-using repository's checks run.
const ciDir = ".ci/"

// getFlakeFacts records the builtins.getFlake calls in the repository's Nix
// files, its .ci/ files and its workflow and composite-action texts. A
// repository that tracks no Nix file costs no request and has no facts: the
// call needs Nix. Workflow and action texts come from f, which fetchFiles
// has already read, and are scanned as raw text so a file that does not
// parse as YAML is still searched.
//
// It decides nothing. A file that cannot be read in full is recorded as
// unreadable with its reason, a binary file under .ci/ or among the Nix files
// holds no call and is skipped, and a query that fails fails the run, as
// claudeFacts does.
func (c *Client) getFlakeFacts(ctx context.Context, name string, entries []filepaths.Entry, f *repoFiles) (audit.NixGetFlake, error) {
	var out audit.NixGetFlake
	var candidates []string
	for _, e := range entries {
		if e.Dir {
			continue
		}
		isNix := strings.HasSuffix(e.Path, ".nix")
		if isNix {
			out.NixFiles = true
		}
		// A symlink's text is its target path, which holds no call; the
		// target is itself tracked if it is a file here.
		if e.Mode == "120000" || !isNix && !strings.HasPrefix(e.Path, ciDir) {
			continue
		}
		candidates = append(candidates, e.Path)
	}
	if !out.NixFiles {
		return audit.NixGetFlake{}, nil
	}
	if len(candidates) > getFlakeFilesMax {
		out.TooManyFiles = true
		return out, nil
	}

	blobs, err := c.readBlobs(ctx, "GetFlakeFiles", name, candidates)
	if err != nil {
		return audit.NixGetFlake{}, err
	}
	for _, p := range candidates {
		b := blobs[p]
		// A binary file holds no call, so it is not a problem; one cut short
		// might, so it is.
		if b != nil && b.IsBinary {
			continue
		}
		out.Files = appendGetFlakeFile(out.Files, p, b)
	}
	if f != nil {
		if f.Workflows != nil {
			for _, e := range f.Workflows.Entries {
				if e.Type == "blob" && (strings.HasSuffix(e.Name, ".yml") || strings.HasSuffix(e.Name, ".yaml")) {
					out.Files = appendGetFlakeFile(out.Files, workflowsDir+e.Name, e.Object)
				}
			}
		}
		if f.Actions != nil {
			for _, dir := range f.Actions.Entries {
				if dir.Type != "tree" || dir.Object == nil {
					continue
				}
				if file, ok := actionFile(dir.Object); ok {
					out.Files = appendGetFlakeFile(out.Files, actionsDir+dir.Name+"/"+file.Name, file.Object)
				}
			}
		}
	}
	slices.SortFunc(out.Files, func(a, b audit.GetFlakeFile) int { return cmp.Compare(a.Path, b.Path) })
	return out, nil
}

// appendGetFlakeFile adds the file to files when it holds a call or cannot be
// read in full.
func appendGetFlakeFile(files []audit.GetFlakeFile, path string, b *fileBlob) []audit.GetFlakeFile {
	text, problem := blobText(b, path)
	if problem != "" {
		return append(files, audit.GetFlakeFile{Path: path, Unreadable: problem})
	}
	if args := getflake.Args(text); len(args) > 0 {
		return append(files, audit.GetFlakeFile{Path: path, Args: args})
	}
	return files
}
