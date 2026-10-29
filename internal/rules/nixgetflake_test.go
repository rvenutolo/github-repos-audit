package rules_test

import (
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// getFlakeCell judges one tools repository, which standard-types.toml holds
// to nix_getflake_git = "required", and returns its nix_getflake_git cell.
func getFlakeCell(t *testing.T, g audit.NixGetFlake) rules.Cell {
	t.Helper()
	r := fixture("alpha", "tools")
	r.NixGetFlake = g
	rep := mustEvaluateOne(t, r)
	return rep.Repos[0].Cell(rules.CheckNixGetflakeGit)
}

// TestEvaluate_nixGetflakeGit_naWithoutNix: with no tracked Nix file there is
// no call to judge.
func TestEvaluate_nixGetflakeGit_naWithoutNix(t *testing.T) {
	t.Parallel()

	if got := getFlakeCell(t, audit.NixGetFlake{}); got.Verdict != rules.NA {
		t.Errorf("cell = %v %q, want n/a", got.Verdict, got.Value)
	}
}

// TestEvaluate_nixGetflakeGit_noCallsPasses: Nix files that never call
// builtins.getFlake have nothing reading the working tree.
func TestEvaluate_nixGetflakeGit_noCallsPasses(t *testing.T) {
	t.Parallel()

	if got := getFlakeCell(t, audit.NixGetFlake{NixFiles: true}); got.Verdict != rules.Pass {
		t.Errorf("cell = %v %q, want pass", got.Verdict, got.Value)
	}
}

// TestEvaluate_nixGetflakeGit_arguments: an argument passes only when it is
// a string Nix fetches through git or a registry, never a path or a string
// naming one, and never an expression the tool cannot read.
func TestEvaluate_nixGetflakeGit_arguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arg  string
		pass bool
	}{
		{"toString REPO_PLACEHOLDER", false},
		{"toString", false},
		{"./.", false},
		{"/abs", false},
		{"~/x", false},
		{"src", false},
		{`"path:/x"`, false},
		{`"/x"`, false},
		{`"./x"`, false},
		{`"~/x"`, false},
		{`"${REPO_DIR}"`, false},
		{`"'"`, false},
		{"''${x}''", false},
		{`"git+file://REPO_PLACEHOLDER"`, true},
		{`"git+file://${REPO_DIR}"`, true},
		{`"github:o/r"`, true},
		{`"https://x/y.tar.gz"`, true},
		{`"nixpkgs"`, true},
		{`"nixpkgs/nixos-24.05"`, true},
	}
	for _, tc := range tests {
		t.Run(tc.arg, func(t *testing.T) {
			t.Parallel()

			got := getFlakeCell(t, audit.NixGetFlake{
				NixFiles: true,
				Files:    []audit.GetFlakeFile{{Path: ".ci/x", Args: []string{tc.arg}}},
			})
			if tc.pass {
				if got.Verdict != rules.Pass || got.Value != "" {
					t.Errorf("cell = %v %q, want pass", got.Verdict, got.Value)
				}
				return
			}
			if got.Verdict != rules.Fail || got.Value != "not via git in .ci/x" {
				t.Errorf("cell = %v %q, want fail %q", got.Verdict, got.Value, "not via git in .ci/x")
			}
		})
	}
}

// TestEvaluate_nixGetflakeGit_shellLeftoverFails: a lone quote left from
// shell quoting is no readable string, so it fails.
func TestEvaluate_nixGetflakeGit_shellLeftoverFails(t *testing.T) {
	t.Parallel()

	got := getFlakeCell(t, audit.NixGetFlake{
		NixFiles: true,
		Files:    []audit.GetFlakeFile{{Path: ".ci/x", Args: []string{"'"}}},
	})
	if got.Verdict != rules.Fail {
		t.Errorf("cell = %v %q, want fail", got.Verdict, got.Value)
	}
}

// TestEvaluate_nixGetflakeGit_problemsOrdered: failing files come first in
// path order, each once, then unreadable files in path order.
func TestEvaluate_nixGetflakeGit_problemsOrdered(t *testing.T) {
	t.Parallel()

	got := getFlakeCell(t, audit.NixGetFlake{
		NixFiles: true,
		Files: []audit.GetFlakeFile{
			{Path: "b.nix", Args: []string{"./."}},
			{Path: "a.nix", Args: []string{"./.", `"/x"`}},
			{Path: "c.nix", Unreadable: "binary"},
		},
	})
	const want = "not via git in a.nix, not via git in b.nix, unreadable c.nix"
	if got.Verdict != rules.Fail || got.Value != want {
		t.Errorf("cell = %v %q, want fail %q", got.Verdict, got.Value, want)
	}
}

// TestEvaluate_nixGetflakeGit_tooManyFails: files not read may hold a call,
// so the cell fails and says why.
func TestEvaluate_nixGetflakeGit_tooManyFails(t *testing.T) {
	t.Parallel()

	got := getFlakeCell(t, audit.NixGetFlake{NixFiles: true, TooManyFiles: true})
	if got.Verdict != rules.Fail || got.Value != "too many Nix files" {
		t.Errorf("cell = %v %q, want fail %q", got.Verdict, got.Value, "too many Nix files")
	}
}
