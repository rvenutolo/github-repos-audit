package rules_test

import (
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

const (
	ciPath     = ".github/workflows/ci.yml"
	setupPath  = ".github/actions/setup/action.yml"
	mainGate   = "github.ref == 'refs/heads/main'"
	cacheNix   = "nix-community/cache-nix-action@v7"
	cacheSave  = "actions/cache/save@v6"
	cacheFull  = "actions/cache@v6"
	cacheRestr = "actions/cache/restore@v6"
)

// saveWorkflow is a workflow that runs on pull requests, with one job of the
// given steps under the given job-level if.
func saveWorkflow(jobIf string, uses ...audit.StepUse) audit.Workflow {
	return audit.Workflow{
		Path:   ciPath,
		Events: []string{"pull_request", "push"},
		Jobs:   []audit.Job{{ID: "check", If: jobIf, Uses: uses}},
	}
}

// cacheSaveCell judges one tools repository, whose default branch is main
// and which standard-types.toml holds to cache_save_default_branch =
// "required", and returns its cache_save_default_branch cell.
func cacheSaveCell(t *testing.T, workflows []audit.Workflow, actions ...audit.CompositeAction) rules.Cell {
	t.Helper()
	r := fixture("alpha", "tools")
	r.Workflows = workflows
	r.Actions = actions
	rep := mustEvaluateOne(t, r)
	return rep.Repos[0].Cell(rules.CheckCacheSaveDefaultBranch)
}

func wantPass(t *testing.T, got rules.Cell) {
	t.Helper()
	if got.Verdict != rules.Pass || got.Value != "" {
		t.Errorf("cell = %v %q, want pass", got.Verdict, got.Value)
	}
}

func wantFail(t *testing.T, got rules.Cell, value string) {
	t.Helper()
	if got.Verdict != rules.Fail || got.Value != value {
		t.Errorf("cell = %v %q, want fail %q", got.Verdict, got.Value, value)
	}
}

const ciSaves = "saves off the default branch in " + ciPath

// TestEvaluate_cacheSave_naWithoutCacheSteps: with no judged cache step in a
// workflow that can run off the default branch there is nothing to judge.
func TestEvaluate_cacheSave_naWithoutCacheSteps(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		ws   []audit.Workflow
	}{
		{"no workflows", nil},
		{"no steps", []audit.Workflow{saveWorkflow("")}},
		{"other action", []audit.Workflow{saveWorkflow("", audit.StepUse{Action: "actions/checkout@v5"})}},
		{"restore only", []audit.Workflow{saveWorkflow("", audit.StepUse{Action: cacheRestr})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := cacheSaveCell(t, tc.ws); got.Verdict != rules.NA {
				t.Errorf("cell = %v %q, want n/a", got.Verdict, got.Value)
			}
		})
	}
}

// TestEvaluate_cacheSave_ungatedSaversFail: every judged action saves on a
// pull request unless something stops it.
func TestEvaluate_cacheSave_ungatedSaversFail(t *testing.T) {
	t.Parallel()

	for _, u := range []audit.StepUse{
		{Action: cacheFull},
		{Action: cacheSave},
		{Action: cacheNix},
		{Action: cacheNix, With: map[string]string{"save": "true"}},
		{Action: "DeterminateSystems/magic-nix-cache-action@v13"},
		{Action: "actions/setup-go@v6"},
		{Action: "actions/setup-node@v5", With: map[string]string{"cache": "npm"}},
		{Action: "actions/setup-python@v6", With: map[string]string{"cache": "pip"}},
		// A misspelt save input leaves save unset, which saves.
		{Action: cacheNix, With: map[string]string{"saves": "false"}},
	} {
		t.Run(u.Action, func(t *testing.T) {
			t.Parallel()

			wantFail(t, cacheSaveCell(t, []audit.Workflow{saveWorkflow("", u)}), ciSaves)
		})
	}
}

// TestEvaluate_cacheSave_savingTurnedOffPasses: an input that turns saving
// off, or a setup action not asked to cache, saves nothing.
func TestEvaluate_cacheSave_savingTurnedOffPasses(t *testing.T) {
	t.Parallel()

	for _, u := range []audit.StepUse{
		{Action: cacheNix, With: map[string]string{"save": "false"}},
		{Action: "actions/setup-go@v6", With: map[string]string{"cache": "false"}},
		{Action: "actions/setup-node@v5"},
		{Action: "actions/setup-python@v6", With: map[string]string{"cache": ""}},
	} {
		t.Run(u.Action, func(t *testing.T) {
			t.Parallel()

			wantPass(t, cacheSaveCell(t, []audit.Workflow{saveWorkflow("", u, audit.StepUse{Action: cacheSave, If: mainGate})}))
		})
	}
}

// TestEvaluate_cacheSave_gates: a step saves only on the default branch when
// an enclosing if, or its save input, is a chain of && terms one of which
// pins the ref to the default branch. Anything else, an event-name test
// included, fails.
func TestEvaluate_cacheSave_gates(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		expr string
		pass bool
	}{
		{mainGate, true},
		{"${{ github.ref == 'refs/heads/main' }}", true},
		{"'refs/heads/main' == github.ref", true},
		{"github.ref_name == 'main'", true},
		{"github.event_name != 'pull_request' && github.ref == 'refs/heads/main'", true},
		{"always() && github.ref == 'refs/heads/main'", true},
		{"${{ success() && github.ref_name == 'main' }}", true},
		{"github.event_name != 'pull_request'", false},
		{"github.ref == 'refs/heads/dev'", false},
		{"github.ref_name == 'refs/heads/main'", false},
		{"github.ref == 'main'", false},
		{"github.ref == 'refs/heads/main' || github.event_name == 'schedule'", false},
		{"(github.ref == 'refs/heads/main')", false},
		{"!cancelled() && contains(github.ref, 'main')", false},
		{"always()", false},
		{"true", false},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			for _, place := range []struct {
				name string
				w    audit.Workflow
			}{
				{"step if", saveWorkflow("", audit.StepUse{Action: cacheSave, If: tc.expr})},
				{"job if", saveWorkflow(tc.expr, audit.StepUse{Action: cacheFull})},
				{"save input", saveWorkflow("", audit.StepUse{Action: cacheNix, With: map[string]string{"save": tc.expr}})},
			} {
				got := cacheSaveCell(t, []audit.Workflow{place.w})
				if tc.pass {
					if got.Verdict != rules.Pass {
						t.Errorf("%s: cell = %v %q, want pass", place.name, got.Verdict, got.Value)
					}
				} else if got.Verdict != rules.Fail || got.Value != ciSaves {
					t.Errorf("%s: cell = %v %q, want fail %q", place.name, got.Verdict, got.Value, ciSaves)
				}
			}
		})
	}
}

// TestEvaluate_cacheSave_otherDefaultBranch: the gate is compared with the
// repository's own default branch, not with main.
func TestEvaluate_cacheSave_otherDefaultBranch(t *testing.T) {
	t.Parallel()

	r := fixture("alpha", "tools")
	r.DefaultBranch = "trunk"
	r.Workflows = []audit.Workflow{saveWorkflow("", audit.StepUse{Action: cacheSave, If: mainGate})}
	rep := mustEvaluateOne(t, r)
	wantFail(t, rep.Repos[0].Cell(rules.CheckCacheSaveDefaultBranch), ciSaves)

	r.Workflows = []audit.Workflow{saveWorkflow("", audit.StepUse{Action: cacheSave, If: "github.ref == 'refs/heads/trunk'"})}
	rep = mustEvaluateOne(t, r)
	wantPass(t, rep.Repos[0].Cell(rules.CheckCacheSaveDefaultBranch))
}

// TestEvaluate_cacheSave_scope: a workflow whose every trigger runs only on
// the default branch cannot save anywhere else, so its steps are not
// judged; any trigger that can run on another ref brings it into scope.
func TestEvaluate_cacheSave_scope(t *testing.T) {
	t.Parallel()

	step := audit.StepUse{Action: cacheSave}
	for _, tc := range []struct {
		name    string
		w       audit.Workflow
		inScope bool
	}{
		{"schedule", audit.Workflow{Events: []string{"schedule"}}, false},
		{"push to main", audit.Workflow{Events: []string{"push"}, PushBranches: []string{"main"}}, false},
		{"schedule and push to main", audit.Workflow{Events: []string{"push", "schedule"}, PushBranches: []string{"main"}}, false},
		{"dispatch", audit.Workflow{Events: []string{"schedule", "workflow_dispatch"}}, true},
		{"any push", audit.Workflow{Events: []string{"push"}}, true},
		{"push glob", audit.Workflow{Events: []string{"push"}, PushBranches: []string{"ma*"}}, true},
		{"push ignore", audit.Workflow{Events: []string{"push"}, PushBranchesIgnore: []string{"dev"}}, true},
		{"push tags", audit.Workflow{Events: []string{"push"}, PushBranches: []string{"main"}, PushTagFilter: true}, true},
		{"merge queue", audit.Workflow{Events: []string{"merge_group"}}, true},
		{"reusable", audit.Workflow{Events: []string{"workflow_call"}}, true},
		{"no trigger", audit.Workflow{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := tc.w
			w.Path = ciPath
			w.Jobs = []audit.Job{{ID: "check", Uses: []audit.StepUse{step}}}
			got := cacheSaveCell(t, []audit.Workflow{w})
			if tc.inScope {
				wantFail(t, got, ciSaves)
			} else if got.Verdict != rules.NA {
				t.Errorf("cell = %v %q, want n/a", got.Verdict, got.Value)
			}
		})
	}
}

// TestEvaluate_cacheSave_composite: a composite action's steps are judged
// where they are written, and the workflow step using it gates them as its
// job does. A composite used by a composite is followed too, and a cycle
// ends.
func TestEvaluate_cacheSave_composite(t *testing.T) {
	t.Parallel()

	setup := audit.CompositeAction{Path: setupPath, Uses: []audit.StepUse{{Action: cacheNix}, {Action: "./.github/actions/setup"}}}
	use := audit.StepUse{Action: "./.github/actions/setup"}
	wantFail(t, cacheSaveCell(t, []audit.Workflow{saveWorkflow("", use)}, setup), "saves off the default branch in "+setupPath)

	gatedUse := audit.StepUse{Action: "./.github/actions/setup", If: mainGate}
	wantPass(t, cacheSaveCell(t, []audit.Workflow{saveWorkflow("", gatedUse)}, setup))

	outer := audit.CompositeAction{Path: ".github/actions/outer/action.yml", Uses: []audit.StepUse{use}}
	wantFail(t, cacheSaveCell(t, []audit.Workflow{saveWorkflow("", audit.StepUse{Action: "./.github/actions/outer"})}, outer, setup),
		"saves off the default branch in "+setupPath)
}

// TestEvaluate_cacheSave_unreadable: an unreadable workflow may be one that
// saves off the default branch, and so may an unreadable composite a
// workflow uses; both fail, after the files that do save, each once in path
// order.
func TestEvaluate_cacheSave_unreadable(t *testing.T) {
	t.Parallel()

	ws := []audit.Workflow{
		saveWorkflow("", audit.StepUse{Action: cacheFull}, audit.StepUse{Action: "./.github/actions/setup"}, audit.StepUse{Action: cacheSave}),
		{Path: ".github/workflows/broken.yml", ParseError: "bad"},
	}
	got := cacheSaveCell(t, ws, audit.CompositeAction{Path: setupPath, ParseError: "bad"})
	wantFail(t, got, ciSaves+", unreadable .github/actions/setup/action.yml, unreadable .github/workflows/broken.yml")
}
