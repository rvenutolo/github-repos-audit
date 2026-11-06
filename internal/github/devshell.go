package github

import (
	"cmp"
	"context"
	"path"
	"slices"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/filepaths"
)

// devshellWrapperPath is the one file the devShell checks treat as the
// wrapper: it is judged by its own markers, not as a script.
const devshellWrapperPath = ".ci/in-devshell"

// devshellGenerated reports whether a file's base name is a well-known
// generated wrapper of a build tool, which is not the repository's script.
// Such files are dropped before reading, so they cost no request and do not
// count toward the bound.
func devshellGenerated(file string) bool {
	switch path.Base(file) {
	case "mvnw", "gradlew":
		return true
	}
	return false
}

// devshellFacts records the lines of a repository's executable shell scripts
// that make up a devShell self-wrap, and the lines of its .ci/in-devshell
// wrapper that make up a temp-directory cleanup. A repository with no
// executable file and no wrapper costs no request and has no facts.
//
// It decides nothing. A shebang can only be seen by reading, so every
// executable (mode 100755) file is read, except the generated wrappers; a
// file that is not a shell script is skipped, as is a binary file. A symlink
// has no shebang of its own, and a 100644 file is not run directly, which is
// how a sourced library falls out. The wrapper is read whatever its mode, but
// if it is not a regular file it is recorded as unreadable without a read.
// A file that cannot be read in full is recorded as unreadable with its
// reason, since it may hold the lines, and a query that fails fails the run,
// as goPathsFacts does. Past getFlakeFilesMax executable files, for the
// reason it gives, no script is read, but the wrapper still is.
func (c *Client) devshellFacts(ctx context.Context, name string, entries []filepaths.Entry) (audit.Devshell, error) {
	var out audit.Devshell
	var candidates []string
	wrapperRead := false
	for _, e := range entries {
		if e.Path == devshellWrapperPath {
			if !e.Dir && (e.Mode == "100644" || e.Mode == "100755") {
				wrapperRead = true
			} else {
				out.Wrapper = &audit.DevshellWrapper{Present: true, Unreadable: devshellWrapperPath + ": not a regular file"}
			}
			continue
		}
		if !e.Dir && e.Mode == "100755" && !devshellGenerated(e.Path) {
			candidates = append(candidates, e.Path)
		}
	}
	if len(candidates) > getFlakeFilesMax {
		out.TooManyFiles = true
		candidates = nil
	}
	if len(candidates) == 0 && !wrapperRead {
		return out, nil
	}

	toRead := candidates
	if wrapperRead {
		toRead = append(slices.Clone(candidates), devshellWrapperPath)
	}
	slices.Sort(toRead)
	blobs, err := c.readBlobs(ctx, "DevshellFiles", name, toRead)
	if err != nil {
		return audit.Devshell{}, err
	}

	if wrapperRead {
		out.Wrapper = &audit.DevshellWrapper{Present: true}
		b := blobs[devshellWrapperPath]
		if text, problem := blobText(b, devshellWrapperPath); problem != "" {
			out.Wrapper.Unreadable = problem
		} else {
			out.Wrapper.Wait, out.Wrapper.Mktemp, out.Wrapper.KeepTmpdir, out.Wrapper.ExitTrap = wrapperLines(text)
		}
	}
	for _, p := range candidates {
		b := blobs[p]
		if b != nil && b.IsBinary {
			continue
		}
		text, problem := blobText(b, p)
		if problem != "" {
			out.Scripts = append(out.Scripts, audit.DevshellScript{Path: p, Unreadable: problem})
			continue
		}
		if !shellShebang(text) {
			continue
		}
		s := audit.DevshellScript{Path: p, Exempt: exemptReasons(text)}
		s.InDevshellTest, s.ExecInDevshell = scriptLines(text)
		out.Scripts = append(out.Scripts, s)
	}
	slices.SortFunc(out.Scripts, func(a, b audit.DevshellScript) int { return cmp.Compare(a.Path, b.Path) })
	return out, nil
}
