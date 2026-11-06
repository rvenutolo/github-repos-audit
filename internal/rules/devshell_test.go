package rules_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// fullWrapper is a wrapper carrying all four markers.
func fullWrapper() *audit.DevshellWrapper {
	return &audit.DevshellWrapper{
		Present:    true,
		Wait:       []string{`wait "${child_pid}"`},
		Mktemp:     []string{`tmp="$(mktemp --directory)"`},
		KeepTmpdir: []string{"TMPDIR"},
		ExitTrap:   []string{`trap cleanup EXIT`},
	}
}

// wrapping is a script that tests IN_DEVSHELL and execs the wrapper.
func wrapping(path string) audit.DevshellScript {
	return audit.DevshellScript{
		Path:           path,
		InDevshellTest: []string{`if [[ -z "${IN_DEVSHELL:-}" ]]; then`},
		ExecInDevshell: []string{`exec .ci/in-devshell "$0" "$@"`},
	}
}

// devshellReport judges one tools repository, which standard-types.toml holds
// to both devshell checks as required, holding the given facts.
func devshellReport(t *testing.T, d audit.Devshell) *rules.Report {
	t.Helper()
	r := fixture("alpha", "tools")
	r.Devshell = d
	return mustEvaluateOne(t, r)
}

// TestEvaluate_devshell_naWhenNothingJudged: the zero value, an empty
// repository, and a repository whose only scripts are exempt (with no
// wrapper) have nothing to judge, so both rows are n/a and list nothing.
func TestEvaluate_devshell_naWhenNothingJudged(t *testing.T) {
	t.Parallel()

	empty := fixture("alpha", "tools")
	empty.Empty = true
	empty.Devshell = audit.Devshell{Scripts: []audit.DevshellScript{{Path: "a.sh", Exempt: []string{"vendored"}}}}
	for name, rep := range map[string]*rules.Report{
		"zero value":  devshellReport(t, audit.Devshell{}),
		"empty repo":  mustEvaluateOne(t, empty),
		"only exempt": devshellReport(t, audit.Devshell{Scripts: []audit.DevshellScript{{Path: "a.sh", Exempt: []string{"vendored"}}}}),
	} {
		for _, c := range []rules.Check{rules.CheckDevshellScripts, rules.CheckDevshellTmpCleanup} {
			if got := rep.Repos[0].Cell(c); got.Verdict != rules.NA {
				t.Errorf("%s %s = %v %q, want n/a", name, c, got.Verdict, got.Value)
			}
		}
		if len(rep.DevshellScriptProblems) != 0 || len(rep.DevshellWrapperProblems) != 0 {
			t.Errorf("%s lists = %v %v, want none", name, rep.DevshellScriptProblems, rep.DevshellWrapperProblems)
		}
	}
}

// TestEvaluate_devshell_passes: a self-wrapping script and a full wrapper
// pass both rows.
func TestEvaluate_devshell_passes(t *testing.T) {
	t.Parallel()

	rep := devshellReport(t, audit.Devshell{
		Wrapper: fullWrapper(),
		Scripts: []audit.DevshellScript{wrapping("a.sh"), {Path: "b.sh", Exempt: []string{"runs on the host"}}},
	})
	wantPlainPass(t, rep.Repos[0].Cell(rules.CheckDevshellScripts))
	wantPlainPass(t, rep.Repos[0].Cell(rules.CheckDevshellTmpCleanup))
	if len(rep.DevshellScriptProblems) != 0 || len(rep.DevshellWrapperProblems) != 0 {
		t.Errorf("lists = %v %v, want none", rep.DevshellScriptProblems, rep.DevshellWrapperProblems)
	}
}

// TestEvaluate_devshellScripts_failures: each problem fails the row with the
// cell text and list the spec gives.
func TestEvaluate_devshellScripts_failures(t *testing.T) {
	t.Parallel()

	noExec := audit.DevshellScript{Path: "b.sh", InDevshellTest: []string{`[[ -n "${IN_DEVSHELL:-}" ]]`}}
	noTest := audit.DevshellScript{Path: "a.sh", ExecInDevshell: []string{`exec .ci/in-devshell "$0"`}}
	for _, tc := range []struct {
		name  string
		d     audit.Devshell
		value string
		items []string
	}{
		{"missing exec line", audit.Devshell{Wrapper: fullWrapper(), Scripts: []audit.DevshellScript{noExec, wrapping("c.sh")}}, "1 script", []string{"b.sh"}},
		{"two scripts in path order", audit.Devshell{Wrapper: fullWrapper(), Scripts: []audit.DevshellScript{noTest, noExec}}, "2 scripts", []string{"a.sh", "b.sh"}},
		{"no wrapper", audit.Devshell{Scripts: []audit.DevshellScript{wrapping("a.sh")}}, "no .ci/in-devshell", []string{"no .ci/in-devshell"}},
		{"no wrapper and a failing script", audit.Devshell{Scripts: []audit.DevshellScript{noExec}}, "no .ci/in-devshell, b.sh", []string{"no .ci/in-devshell", "b.sh"}},
		{"empty exempt reason", audit.Devshell{Wrapper: fullWrapper(), Scripts: []audit.DevshellScript{{Path: "a.sh", Exempt: []string{""}}}}, "a.sh: exempt without a reason", []string{"a.sh: exempt without a reason"}},
		{"unreadable script", audit.Devshell{Wrapper: fullWrapper(), Scripts: []audit.DevshellScript{{Path: "a.sh", Unreadable: "too large"}}}, "unreadable a.sh", []string{"unreadable a.sh"}},
		{"too many files", audit.Devshell{TooManyFiles: true, Wrapper: fullWrapper()}, "too many files", []string{"too many files"}},
		{"too many files hides the rest", audit.Devshell{TooManyFiles: true}, "too many files", []string{"too many files"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rep := devshellReport(t, tc.d)
			wantCell(t, rep.Repos[0].Cell(rules.CheckDevshellScripts), rules.Fail, tc.value)
			want := []rules.RepoList[string]{{Repo: "alpha", Items: tc.items}}
			if diff := cmp.Diff(want, rep.DevshellScriptProblems); diff != "" {
				t.Errorf("DevshellScriptProblems differs (-want +got):\n%s", diff)
			}
		})
	}
}

// TestEvaluate_devshellTmpCleanup_failures: each missing marker gives its own
// text, and several are listed in marker order.
func TestEvaluate_devshellTmpCleanup_failures(t *testing.T) {
	t.Parallel()

	without := func(mutate func(*audit.DevshellWrapper)) audit.Devshell {
		w := fullWrapper()
		mutate(w)
		return audit.Devshell{Wrapper: w}
	}
	for _, tc := range []struct {
		name  string
		d     audit.Devshell
		items []string
	}{
		{"no wait", without(func(w *audit.DevshellWrapper) { w.Wait = nil }), []string{"no wait for nix develop"}},
		{"no mktemp", without(func(w *audit.DevshellWrapper) { w.Mktemp = nil }), []string{"no mktemp --directory"}},
		{"no tmpdir", without(func(w *audit.DevshellWrapper) { w.KeepTmpdir = nil }), []string{"TMPDIR not kept"}},
		{"no trap", without(func(w *audit.DevshellWrapper) { w.ExitTrap = nil }), []string{"no EXIT trap"}},
		{"all missing", audit.Devshell{Wrapper: &audit.DevshellWrapper{Present: true}}, []string{
			"no wait for nix develop", "no mktemp --directory", "TMPDIR not kept", "no EXIT trap",
		}},
		{"unreadable", audit.Devshell{Wrapper: &audit.DevshellWrapper{Present: true, Unreadable: "symlink"}}, []string{"unreadable"}},
		{"absent while judged", audit.Devshell{Scripts: []audit.DevshellScript{wrapping("a.sh")}}, []string{"no .ci/in-devshell"}},
		{"absent while too many files", audit.Devshell{TooManyFiles: true}, []string{"no .ci/in-devshell"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rep := devshellReport(t, tc.d)
			cell := rep.Repos[0].Cell(rules.CheckDevshellTmpCleanup)
			if cell.Verdict != rules.Fail {
				t.Fatalf("verdict = %v %q, want fail", cell.Verdict, cell.Value)
			}
			want := []rules.RepoList[string]{{Repo: "alpha", Items: tc.items}}
			if diff := cmp.Diff(want, rep.DevshellWrapperProblems); diff != "" {
				t.Errorf("DevshellWrapperProblems differs (-want +got):\n%s", diff)
			}
		})
	}
}

// TestEvaluate_devshellTmpCleanup_wrapperAloneIsJudged: a wrapper with no
// scripts is judged, so a missing marker fails.
func TestEvaluate_devshellTmpCleanup_wrapperAloneIsJudged(t *testing.T) {
	t.Parallel()

	rep := devshellReport(t, audit.Devshell{Wrapper: fullWrapper()})
	wantPlainPass(t, rep.Repos[0].Cell(rules.CheckDevshellTmpCleanup))
	if got := rep.Repos[0].Cell(rules.CheckDevshellScripts); got.Verdict != rules.NA {
		t.Errorf("scripts row = %v, want n/a without scripts", got.Verdict)
	}

	w := fullWrapper()
	w.ExitTrap = nil
	rep = devshellReport(t, audit.Devshell{Wrapper: w})
	if got := rep.Repos[0].Cell(rules.CheckDevshellTmpCleanup); got.Verdict != rules.Fail {
		t.Errorf("verdict = %v, want fail", got.Verdict)
	}
}

// TestEvaluate_devshell_notRequiredIsNAAndUnlisted: a not_required override
// gives n/a on both rows, and detailLists leaves the repository out.
func TestEvaluate_devshell_notRequiredIsNAAndUnlisted(t *testing.T) {
	t.Parallel()

	r := fixture("alpha", "tools")
	r.Devshell = audit.Devshell{Scripts: []audit.DevshellScript{{Path: "a.sh"}}}
	r.Overrides = map[string]string{"devshell_scripts": rules.OverrideNotRequired, "devshell_tmp_cleanup": rules.OverrideNotRequired}
	rep := mustEvaluateOne(t, r)
	for _, c := range []rules.Check{rules.CheckDevshellScripts, rules.CheckDevshellTmpCleanup} {
		if got := rep.Repos[0].Cell(c); got.Verdict != rules.NA {
			t.Errorf("%s = %v %q, want n/a", c, got.Verdict, got.Value)
		}
	}
	if len(rep.DevshellScriptProblems) != 0 || len(rep.DevshellWrapperProblems) != 0 {
		t.Errorf("lists = %v %v, want none", rep.DevshellScriptProblems, rep.DevshellWrapperProblems)
	}
}
