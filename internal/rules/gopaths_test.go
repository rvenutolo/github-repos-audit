package rules_test

import (
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

const (
	stdGoPath     = `export GOPATH="$HOME/.cache/go"`
	stdGoModCache = `export GOMODCACHE="$HOME/.cache/go/mod"`
)

// goPathsCell judges one tools repository with a root go.mod and a
// flake.nix, which standard-types.toml holds to go_paths = "required", and
// returns its go_paths cell.
func goPathsCell(t *testing.T, g audit.GoPaths) rules.Cell {
	t.Helper()
	r := fixture("alpha", "tools")
	r.Files.GoMod = true
	r.Files.FlakeNix = true
	r.GoPaths = g
	rep := mustEvaluateOne(t, r)
	return rep.Repos[0].Cell(rules.CheckGoPaths)
}

// TestEvaluate_goPaths_naWithoutGoFlake: only a repository with both a root
// go.mod and a flake.nix has a Go devShell to judge.
func TestEvaluate_goPaths_naWithoutGoFlake(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		goMod, flake bool
	}{
		{"neither", false, false},
		{"go.mod only", true, false},
		{"flake.nix only", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := fixture("alpha", "tools")
			r.Files.GoMod = tc.goMod
			r.Files.FlakeNix = tc.flake
			rep := mustEvaluateOne(t, r)
			if got := rep.Repos[0].Cell(rules.CheckGoPaths); got.Verdict != rules.NA {
				t.Errorf("cell = %v %q, want n/a", got.Verdict, got.Value)
			}
		})
	}
}

// TestEvaluate_goPaths_standardPasses: both standard lines pass, whether in
// one file or split across two.
func TestEvaluate_goPaths_standardPasses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		files []audit.GoPathsFile
	}{
		{"one file", []audit.GoPathsFile{{Path: "flake.nix", Lines: []string{stdGoPath, stdGoModCache}}}},
		{"two files", []audit.GoPathsFile{
			{Path: "flake.nix", Lines: []string{stdGoModCache}},
			{Path: "nix/shell.nix", Lines: []string{stdGoPath}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := goPathsCell(t, audit.GoPaths{Files: tc.files})
			if got.Verdict != rules.Pass || got.Value != "" {
				t.Errorf("cell = %v %q, want pass", got.Verdict, got.Value)
			}
		})
	}
}

// TestEvaluate_goPaths_missingFails: the cell names each variable whose
// standard line is absent, GOPATH first. Setting a variable any other way,
// even to a directory outside ~/go, is not the standard.
func TestEvaluate_goPaths_missingFails(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		lines []string
		want  string
	}{
		{"nothing set", nil, "GOPATH, GOMODCACHE"},
		{"only GOPATH", []string{stdGoPath}, "GOMODCACHE"},
		{"only GOMODCACHE", []string{stdGoModCache}, "GOPATH"},
		{"home go", []string{`export GOPATH="$HOME/go"`, stdGoModCache}, "GOPATH"},
		{"nix attribute", []string{`env.GOPATH = "/x";`, stdGoModCache}, "GOPATH"},
		{"braced HOME", []string{`export GOPATH="${HOME}/.cache/go"`, stdGoModCache}, "GOPATH"},
		{"unquoted", []string{stdGoPath, `export GOMODCACHE=$HOME/.cache/go/mod`}, "GOMODCACHE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var g audit.GoPaths
			if tc.lines != nil {
				g.Files = []audit.GoPathsFile{{Path: "flake.nix", Lines: tc.lines}}
			}
			got := goPathsCell(t, g)
			if got.Verdict != rules.Fail || got.Value != tc.want {
				t.Errorf("cell = %v %q, want fail %q", got.Verdict, got.Value, tc.want)
			}
		})
	}
}

// TestEvaluate_goPaths_unreadableFails: a file not read in full may set
// either variable, so it fails the check even beside both standard lines,
// and is named after any missing variable, in path order.
func TestEvaluate_goPaths_unreadableFails(t *testing.T) {
	t.Parallel()

	got := goPathsCell(t, audit.GoPaths{Files: []audit.GoPathsFile{
		{Path: "flake.nix", Lines: []string{stdGoPath, stdGoModCache}},
		{Path: "z.nix", Unreadable: "z.nix: too large to read in full"},
		{Path: "a.nix", Unreadable: "a.nix: not a text file"},
	}})
	const want = "unreadable a.nix, unreadable z.nix"
	if got.Verdict != rules.Fail || got.Value != want {
		t.Errorf("cell = %v %q, want fail %q", got.Verdict, got.Value, want)
	}

	got = goPathsCell(t, audit.GoPaths{Files: []audit.GoPathsFile{{Path: "a.nix", Unreadable: "a.nix: not a text file"}}})
	const wantBoth = "GOPATH, GOMODCACHE, unreadable a.nix"
	if got.Verdict != rules.Fail || got.Value != wantBoth {
		t.Errorf("cell = %v %q, want fail %q", got.Verdict, got.Value, wantBoth)
	}
}

// TestEvaluate_goPaths_tooManyFails: files not read may set either variable,
// so the cell fails and says why.
func TestEvaluate_goPaths_tooManyFails(t *testing.T) {
	t.Parallel()

	got := goPathsCell(t, audit.GoPaths{TooManyFiles: true})
	if got.Verdict != rules.Fail || got.Value != "too many Nix files" {
		t.Errorf("cell = %v %q, want fail %q", got.Verdict, got.Value, "too many Nix files")
	}
}
