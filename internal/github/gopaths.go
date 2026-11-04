package github

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/filepaths"
)

// goPathsAssignment matches a line that sets GOPATH or GOMODCACHE in any
// form: a shell `export GOPATH=`, a bare assignment, or a Nix attribute
// such as `env.GOPATH =`. The name must not be the tail of a longer one, so
// MY_GOPATH= is not counted. Every such line is recorded, whatever its value,
// so a value other than the expected one is visible in audit.json.
var goPathsAssignment = regexp.MustCompile(`(^|[^A-Za-z0-9_])(GOPATH|GOMODCACHE)\s*=`)

// goPathsFacts records the lines of a Go flake's tracked Nix files that set
// GOPATH or GOMODCACHE. Only a repository with both a root go.mod and a
// flake.nix is searched: its devShell is where `go` runs. Every other
// repository costs no request and has no facts.
//
// It decides nothing. Every *.nix file is read, not only flake.nix, so a
// shellHook kept in a file the flake imports is still found. A file that
// cannot be read in full is recorded as unreadable with its reason, since it
// may hold the lines; a binary file holds none and is skipped; and a query
// that fails fails the run, as getFlakeFacts does. The bound on files read
// is getFlakeFilesMax, for the reason it gives.
func (c *Client) goPathsFacts(ctx context.Context, name string, entries []filepaths.Entry, files audit.Files) (audit.GoPaths, error) {
	if !files.GoMod || !files.FlakeNix {
		return audit.GoPaths{}, nil
	}
	var candidates []string
	for _, e := range entries {
		// A symlink's text is its target path; the target is itself
		// tracked if it is a file here.
		if !e.Dir && e.Mode != "120000" && strings.HasSuffix(e.Path, ".nix") {
			candidates = append(candidates, e.Path)
		}
	}
	if len(candidates) > getFlakeFilesMax {
		return audit.GoPaths{TooManyFiles: true}, nil
	}

	slices.Sort(candidates)
	blobs, err := c.readBlobs(ctx, "GoPathsFiles", name, candidates)
	if err != nil {
		return audit.GoPaths{}, err
	}
	var out audit.GoPaths
	for _, p := range candidates {
		b := blobs[p]
		if b != nil && b.IsBinary {
			continue
		}
		text, problem := blobText(b, p)
		if problem != "" {
			out.Files = append(out.Files, audit.GoPathsFile{Path: p, Unreadable: problem})
			continue
		}
		if lines := goPathsLines(text); len(lines) > 0 {
			out.Files = append(out.Files, audit.GoPathsFile{Path: p, Lines: lines})
		}
	}
	return out, nil
}

// goPathsLines returns every line of text that assigns GOPATH or GOMODCACHE,
// trimmed, in file order.
func goPathsLines(text string) []string {
	var out []string
	for line := range strings.Lines(text) {
		if goPathsAssignment.MatchString(line) {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}
