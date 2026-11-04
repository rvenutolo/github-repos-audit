package rules

import (
	"slices"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// The account's standard Go paths: every Go flake's devShell carries both
// lines verbatim, so `go` never falls back to ~/go and every project shares
// one module cache with the host shell. $HOME and not $XDG_CACHE_HOME,
// which a devShell entered with --ignore-environment does not keep. Exact
// text rather than any acceptable value, because the point is that every
// repository says it the same way.
const (
	goPathLine     = `export GOPATH="$HOME/.cache/go"`
	goModCacheLine = `export GOMODCACHE="$HOME/.cache/go/mod"`
)

// observeGoPaths judges whether a Go flake's Nix files set GOPATH and
// GOMODCACHE to the standard. It passes when both standard lines appear in
// tracked *.nix files, in one file or two. The cell names each variable
// whose line is missing, then each Nix file that could not be read, since
// it may set either; too many files to read is a problem of its own. Any
// other line setting either variable is no substitute for the standard one.
//
// It is n/a without a root go.mod or without a flake.nix: only a Go flake
// has a devShell for the lines to configure.
func observeGoPaths(r audit.Repo) observation {
	if !r.Files.GoMod || !r.Files.FlakeNix {
		return observation{unknown: true}
	}
	g := r.GoPaths
	if g.TooManyFiles {
		return observation{value: "too many Nix files", mark: true}
	}
	var problems, unreadable []string
	for _, want := range []struct{ name, line string }{
		{"GOPATH", goPathLine},
		{"GOMODCACHE", goModCacheLine},
	} {
		if !slices.ContainsFunc(g.Files, func(f audit.GoPathsFile) bool { return slices.Contains(f.Lines, want.line) }) {
			problems = append(problems, want.name)
		}
	}
	for _, f := range g.Files {
		if f.Unreadable != "" {
			unreadable = append(unreadable, f.Path)
		}
	}
	problems = appendFiles(problems, "unreadable ", unreadable)
	return observation{ok: len(problems) == 0, value: strings.Join(problems, ", "), mark: true}
}
