package rules_test

import (
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// triggerCells judges one tools repository, which standard-types.toml holds
// to ci_on_pr and ci_on_push = "required", on the given default branch and
// holding the given workflows, and returns its two trigger cells.
func triggerCells(t *testing.T, branch string, workflows ...audit.Workflow) (pr, push rules.Cell) {
	t.Helper()
	r := fixture("alpha", "tools")
	r.DefaultBranch = branch
	r.Workflows = workflows
	rep := mustEvaluateOne(t, r)
	return rep.Repos[0].Cell(rules.CheckCIOnPR), rep.Repos[0].Cell(rules.CheckCIOnPush)
}

// prCell is the ci_on_pr cell of a repository whose default branch is main.
func prCell(t *testing.T, workflows ...audit.Workflow) rules.Cell {
	t.Helper()
	pr, _ := triggerCells(t, "main", workflows...)
	return pr
}

// pushCell is the ci_on_push cell of a repository on the given default
// branch.
func pushCell(t *testing.T, branch string, workflows ...audit.Workflow) rules.Cell {
	t.Helper()
	_, push := triggerCells(t, branch, workflows...)
	return push
}

// unreadable is a workflow file at path that could not be parsed.
func unreadable(path string) audit.Workflow {
	return audit.Workflow{Path: path, ParseError: "yaml: line 1: did not find expected key"}
}

// TestCIOnPR_passesOnPullRequestWorkflow: a workflow that runs on
// pull_request, with GitHub's default types, runs when a pull request is
// opened or updated.
func TestCIOnPR_passesOnPullRequestWorkflow(t *testing.T) {
	t.Parallel()

	wantCell(t, prCell(t, audit.Workflow{Path: ".github/workflows/ci.yml", Events: []string{"pull_request"}}), rules.Pass, "")
}

// TestCIOnPR_ignoresMergeGate: the always-green placeholder runs on pull
// requests but tests nothing, so a repository holding only it has no CI on
// pull requests, and the cell is a bare cross: nothing was unreadable.
func TestCIOnPR_ignoresMergeGate(t *testing.T) {
	t.Parallel()

	gate := audit.Workflow{Path: ".github/workflows/merge-gate.yml", Events: []string{"pull_request"}}
	pr, push := triggerCells(t, "main", gate)
	wantCell(t, pr, rules.Fail, "")
	wantCell(t, push, rules.Fail, "")
}

// TestCIOnPR_closedOnlyIsNotCI: a pull_request trigger limited to closed runs
// after the pull request is done with, never on what it proposes.
func TestCIOnPR_closedOnlyIsNotCI(t *testing.T) {
	t.Parallel()

	w := audit.Workflow{Path: ".github/workflows/ci.yml", Events: []string{"pull_request"}, PullRequestTypes: []string{"closed"}}
	wantCell(t, prCell(t, w), rules.Fail, "")
}

// TestCIOnPR_namesUnreadable: with nothing readable passing, an unreadable
// workflow may be the CI, so the cell names it rather than claiming there is
// none: the first in path order, by base name.
func TestCIOnPR_namesUnreadable(t *testing.T) {
	t.Parallel()

	pr, push := triggerCells(t, "main", unreadable(".github/workflows/b.yml"), unreadable(".github/workflows/a.yml"))
	wantCell(t, pr, rules.Fail, "unreadable a.yml")
	wantCell(t, push, rules.Fail, "unreadable a.yml")
}

// TestCIOnPR_unreadableMergeGateIsNotNamed: the placeholder never counts as
// CI, so an unreadable one is not a candidate either.
func TestCIOnPR_unreadableMergeGateIsNotNamed(t *testing.T) {
	t.Parallel()

	wantCell(t, prCell(t, unreadable(".github/workflows/merge-gate.yml")), rules.Fail, "")
}

// TestCIOnPR_readableBeatsUnreadable: one workflow that qualifies is enough;
// an unreadable neighbour cannot take that away.
func TestCIOnPR_readableBeatsUnreadable(t *testing.T) {
	t.Parallel()

	wantCell(t, prCell(t,
		unreadable(".github/workflows/a.yml"),
		audit.Workflow{Path: ".github/workflows/ci.yml", Events: []string{"pull_request"}},
	), rules.Pass, "")
}

// TestCIOnPR_pullRequestTargetCounts: pull_request_target also runs when a
// pull request is updated.
func TestCIOnPR_pullRequestTargetCounts(t *testing.T) {
	t.Parallel()

	wantCell(t, prCell(t, audit.Workflow{Path: ".github/workflows/ci.yml", Events: []string{"pull_request_target"}}), rules.Pass, "")
}

// TestCIOnPush_needsBothTriggersOnOneWorkflow: a release workflow on push
// beside a CI workflow on pull_request is not CI on the default branch; the
// push trigger has to belong to the workflow that tests pull requests.
func TestCIOnPush_needsBothTriggersOnOneWorkflow(t *testing.T) {
	t.Parallel()

	wantCell(t, pushCell(t, "main",
		audit.Workflow{Path: ".github/workflows/release.yml", Events: []string{"push"}},
		audit.Workflow{Path: ".github/workflows/ci.yml", Events: []string{"pull_request"}},
	), rules.Fail, "")
}

// TestCIOnPush_passes: the usual CI shape, pull_request plus push to main.
func TestCIOnPush_passes(t *testing.T) {
	t.Parallel()

	w := audit.Workflow{Path: ".github/workflows/ci.yml", Events: []string{"pull_request", "push"}, PushBranches: []string{"main"}}
	wantCell(t, pushCell(t, "main", w), rules.Pass, "")
}

// TestCIOnPush_otherDefaultBranch: the filter is matched against the
// repository's own default branch, so branches: [main] misses a master
// repository, and a bare push reaches it.
func TestCIOnPush_otherDefaultBranch(t *testing.T) {
	t.Parallel()

	filtered := audit.Workflow{Path: ".github/workflows/ci.yml", Events: []string{"pull_request", "push"}, PushBranches: []string{"main"}}
	wantCell(t, pushCell(t, "master", filtered), rules.Fail, "")

	bare := audit.Workflow{Path: ".github/workflows/ci.yml", Events: []string{"pull_request", "push"}}
	wantCell(t, pushCell(t, "master", bare), rules.Pass, "")
}

// TestCIOnPush_tagsOnly: a push trigger with only a tag filter runs for tags
// and never for a branch push.
func TestCIOnPush_tagsOnly(t *testing.T) {
	t.Parallel()

	w := audit.Workflow{Path: ".github/workflows/ci.yml", Events: []string{"pull_request", "push"}, PushTagFilter: true}
	wantCell(t, pushCell(t, "main", w), rules.Fail, "")

	// A branch filter beside the tag filter runs on matching branches too.
	w.PushBranches = []string{"main"}
	wantCell(t, pushCell(t, "main", w), rules.Pass, "")
}

// TestCIOnPush_mergeGroupDoesNotCount: a merge queue run saves its cache
// under a ref pull requests cannot read, so it is not CI on the default
// branch.
func TestCIOnPush_mergeGroupDoesNotCount(t *testing.T) {
	t.Parallel()

	w := audit.Workflow{Path: ".github/workflows/ci.yml", Events: []string{"merge_group", "pull_request"}}
	wantCell(t, pushCell(t, "main", w), rules.Fail, "")
}

// TestCIOnPush_mergeGateDoesNotCount: the placeholder with both triggers is
// still not CI.
func TestCIOnPush_mergeGateDoesNotCount(t *testing.T) {
	t.Parallel()

	w := audit.Workflow{Path: ".github/workflows/merge-gate.yml", Events: []string{"pull_request", "push"}}
	wantCell(t, pushCell(t, "main", w), rules.Fail, "")
}

// TestCIOnPush_branchFilters pins GitHub's filter pattern syntax: * stops at
// a slash and ** does not, ? and + qualify the character before them, []
// is a character class, every other character is literal, and in branches: a
// ! pattern excludes, the last matching pattern deciding.
func TestCIOnPush_branchFilters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		branches []string
		ignore   []string
		branch   string
		admitted bool
	}{
		{name: "double star", branches: []string{"**"}, branch: "main", admitted: true},
		{name: "negated after match", branches: []string{"**", "!main"}, branch: "main", admitted: false},
		{name: "readmitted after negation", branches: []string{"!main", "main"}, branch: "main", admitted: true},
		{name: "star suffix", branches: []string{"ma*"}, branch: "main", admitted: true},
		{name: "star stops at slash", branches: []string{"*"}, branch: "feat/x", admitted: false},
		{name: "double star crosses slash", branches: []string{"**"}, branch: "feat/x", admitted: true},
		{name: "optional character", branches: []string{"mai?n"}, branch: "main", admitted: true},
		{name: "repeated character", branches: []string{"ma+in"}, branch: "maaain", admitted: true},
		{name: "character class", branches: []string{"[mt]ain"}, branch: "main", admitted: true},
		{name: "character range", branches: []string{"[a-z]ain"}, branch: "main", admitted: true},
		{name: "quantified class", branches: []string{"[mt]+ain"}, branch: "mmain", admitted: true},
		{name: "reversed range matches nothing", branches: []string{"[z-a]ain"}, branch: "main", admitted: false},
		{name: "non-ASCII literal", branches: []string{"maïn"}, branch: "maïn", admitted: true},
		{name: "other prefix", branches: []string{"release/**"}, branch: "main", admitted: false},
		{name: "only negations", branches: []string{"!dev"}, branch: "main", admitted: false},
		{name: "ignored", ignore: []string{"main"}, branch: "main", admitted: false},
		{name: "ignore misses", ignore: []string{"wip/*"}, branch: "main", admitted: true},
		{name: "literal dot", branches: []string{"main.x"}, branch: "mainax", admitted: false},
		{name: "escaped star is literal", branches: []string{`ma\*`}, branch: "main", admitted: false},
		{name: "escaped star matches itself", branches: []string{`ma\*`}, branch: "ma*", admitted: true},
		{name: "unclosed class matches nothing", branches: []string{"[main"}, branch: "[main", admitted: false},
		{name: "symbol in class matches nothing", branches: []string{"[^x]ain"}, branch: "main", admitted: false},
		{name: "trailing escape matches nothing", branches: []string{`main\`}, branch: `main\`, admitted: false},
		{name: "quantified star matches nothing", branches: []string{"*+"}, branch: "main", admitted: false},
		{name: "leading quantifier matches nothing", branches: []string{"?main"}, branch: "main", admitted: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := audit.Workflow{
				Path:               ".github/workflows/ci.yml",
				Events:             []string{"pull_request", "push"},
				PushBranches:       tt.branches,
				PushBranchesIgnore: tt.ignore,
			}
			want := rules.Fail
			if tt.admitted {
				want = rules.Pass
			}
			wantCell(t, pushCell(t, tt.branch, w), want, "")
		})
	}
}
