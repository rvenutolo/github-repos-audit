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
		{name: "ignore does not negate", ignore: []string{"!main"}, branch: "main", admitted: true},
		{name: "ignore misses", ignore: []string{"wip/*"}, branch: "main", admitted: true},
		{name: "literal dot", branches: []string{"main.x"}, branch: "mainax", admitted: false},
		{name: "escaped star is literal", branches: []string{`ma\*`}, branch: "main", admitted: false},
		{name: "escaped star matches itself", branches: []string{`ma\*`}, branch: "ma*", admitted: true},
		{name: "unclosed class matches nothing", branches: []string{"[main"}, branch: "[main", admitted: false},
		{name: "symbol in class matches nothing", branches: []string{"[^x]ain"}, branch: "main", admitted: false},
		{name: "trailing escape matches nothing", branches: []string{`main\`}, branch: `main\`, admitted: false},
		{name: "quantified star matches nothing", branches: []string{"*+"}, branch: "main", admitted: false},
		{name: "leading quantifier matches nothing", branches: []string{"?main"}, branch: "main", admitted: false},
		{name: "quantified quantifier matches nothing", branches: []string{"ma??in"}, branch: "main", admitted: false},
		{name: "star backtracks", branches: []string{"m*i*n"}, branch: "mainin", admitted: true},
		{name: "star needs the rest", branches: []string{"m*x"}, branch: "main", admitted: false},
		{name: "two stars need the rest", branches: []string{"m*a*x"}, branch: "maaaa", admitted: false},
		{name: "star matches nothing", branches: []string{"main*"}, branch: "main", admitted: true},
		{name: "double star then a segment", branches: []string{"**/main"}, branch: "a/b/main", admitted: true},
		{name: "double star then a segment needs the slash", branches: []string{"**/main"}, branch: "main", admitted: false},
		{name: "star within a segment", branches: []string{"feat/*/x"}, branch: "feat/a/b/x", admitted: false},
		{name: "whole name, not a prefix", branches: []string{"mai"}, branch: "main", admitted: false},
		{name: "whole name, not a suffix", branches: []string{"ain"}, branch: "main", admitted: false},
		{name: "optional character absent", branches: []string{"mainx?"}, branch: "main", admitted: true},
		{name: "repeated character needs one", branches: []string{"mainx+"}, branch: "main", admitted: false},
		{name: "repeated character backtracks", branches: []string{"ma+a"}, branch: "maaa", admitted: true},
		{name: "optional class", branches: []string{"main[0-9]?"}, branch: "main", admitted: true},
		{name: "digit range", branches: []string{"v[0-9]"}, branch: "v7", admitted: true},
		{name: "two ranges in a class", branches: []string{"[a-cx-z]ain"}, branch: "yain", admitted: true},
		{name: "a leading hyphen in a class is literal", branches: []string{"[-a]ain"}, branch: "-ain", admitted: true},
		{name: "a trailing hyphen in a class is literal", branches: []string{"[a-]ain"}, branch: "-ain", admitted: true},
		{name: "a hyphen after a range is literal", branches: []string{"[a-c-e]ain"}, branch: "-ain", admitted: true},
		{name: "a hyphen after a range leaves the next letter single", branches: []string{"[a-c-e]ain"}, branch: "dain", admitted: false},
		{name: "a hyphen as a range start", branches: []string{"[--a]ain"}, branch: "Main", admitted: true},
		{name: "a class is case-sensitive", branches: []string{"[a-z]ain"}, branch: "Main", admitted: false},
		{name: "escaped question mark is literal", branches: []string{`main\?`}, branch: "main?", admitted: true},
		{name: "escaped question mark is not optional", branches: []string{`mainx\?`}, branch: "main", admitted: false},
		{name: "escaped character can be quantified", branches: []string{`ma\*+in`}, branch: "ma**in", admitted: true},
		{name: "escaped letter is the letter", branches: []string{`m\ain`}, branch: "main", admitted: true},
		{name: "empty pattern matches no branch", branches: []string{""}, branch: "main", admitted: false},
		{name: "literal is case-sensitive", branches: []string{"Main"}, branch: "main", admitted: false},
		{name: "non-ASCII under a star", branches: []string{"ma*"}, branch: "maïn", admitted: true},
		{name: "non-ASCII quantified", branches: []string{"maï+n"}, branch: "maïïn", admitted: true},
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

// upToDateCell judges one tools repository, which standard-types.toml holds
// to branch_up_to_date = "required", after mutate has changed its branch
// facts, and returns the branch_up_to_date cell.
func upToDateCell(t *testing.T, mutate func(*audit.Repo)) rules.Cell {
	t.Helper()
	r := fixture("alpha", "tools")
	mutate(&r)
	return mustEvaluateOne(t, r).Repos[0].Cell(rules.CheckBranchUpToDate)
}

// TestBranchUpToDate_strictWithMergeGatePasses: the placeholder counts here,
// since making the strict flag bind a repository without CI is its job.
func TestBranchUpToDate_strictWithMergeGatePasses(t *testing.T) {
	t.Parallel()

	cell := upToDateCell(t, func(r *audit.Repo) {
		r.Branch.Strict = true
		r.Branch.RequiredChecks = []string{"merge-gate"}
	})
	wantCell(t, cell, rules.Pass, "")
}

// TestBranchUpToDate_notStrictFails: required checks that a stale branch may
// skip do not make a pull request prove it works against the default branch.
func TestBranchUpToDate_notStrictFails(t *testing.T) {
	t.Parallel()

	cell := upToDateCell(t, func(r *audit.Repo) {
		r.Branch.Strict = false
		r.Branch.RequiredChecks = []string{"merge-gate"}
	})
	wantCell(t, cell, rules.Fail, "")
}

// TestBranchUpToDate_noChecksFails: the strict flag only binds a required
// status checks rule that names at least one check.
func TestBranchUpToDate_noChecksFails(t *testing.T) {
	t.Parallel()

	cell := upToDateCell(t, func(r *audit.Repo) {
		r.Branch.Strict = true
		r.Branch.RequiredChecks = nil
	})
	wantCell(t, cell, rules.Fail, "")
}

// TestBranchUpToDate_directPushIsNA: a repository that allows direct pushes
// (the packages shape) has no merge to gate, so the check is withheld even
// though the type says required.
func TestBranchUpToDate_directPushIsNA(t *testing.T) {
	t.Parallel()

	cell := upToDateCell(t, func(r *audit.Repo) {
		r.Branch.Types = []string{"required_signatures"}
		r.Branch.Strict = false
		r.Branch.RequiredChecks = nil
	})
	if cell.Verdict != rules.NA {
		t.Errorf("verdict = %v, want NA under direct push", cell.Verdict)
	}
}

// TestBranchUpToDate_emptyRepositoryIsNA: an empty repository has no branch
// rules to read, which is no fact found wanting.
func TestBranchUpToDate_emptyRepositoryIsNA(t *testing.T) {
	t.Parallel()

	cell := upToDateCell(t, func(r *audit.Repo) {
		r.Empty = true
		r.Branch = audit.BranchRules{Known: false}
	})
	if cell.Verdict != rules.NA {
		t.Errorf("verdict = %v, want NA on an empty repository", cell.Verdict)
	}
}

// TestCIOnPR_branchFilters: GitHub matches a pull request trigger's branch
// filters against the pull request's base branch, so a workflow runs on
// pull requests into the default branch only when its filters admit that
// branch, read as push's are.
func TestCIOnPR_branchFilters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		branches       []string
		ignore         []string
		targetBranches []string
		want           rules.Verdict
	}{
		{"no filter", nil, nil, nil, rules.Pass},
		{"branches names the default", []string{"main"}, nil, nil, rules.Pass},
		{"branches pattern admits the default", []string{"ma*"}, nil, nil, rules.Pass},
		{"branches names only release branches", []string{"release/**"}, nil, nil, rules.Fail},
		{"branches negates the default", []string{"**", "!main"}, nil, nil, rules.Fail},
		{"branches-ignore names another branch", nil, []string{"wip/**"}, nil, rules.Pass},
		{"branches-ignore names the default", nil, []string{"main"}, nil, rules.Fail},
		{"pull_request_target admits it where pull_request does not", []string{"release/**"}, nil, []string{"main"}, rules.Pass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			events := []string{"pull_request"}
			if tt.targetBranches != nil {
				events = append(events, "pull_request_target")
			}
			w := audit.Workflow{
				Path: ".github/workflows/ci.yml", Events: events,
				PullRequestBranches: tt.branches, PullRequestBranchesIgnore: tt.ignore,
				PullRequestTargetBranches: tt.targetBranches,
			}
			wantCell(t, prCell(t, w), tt.want, "")
		})
	}
}

// TestCIOnPR_pullRequestTargetBranchFilters: pull_request_target's filters
// are judged on their own, so its branches-ignore excludes the default
// branch even when pull_request is absent.
func TestCIOnPR_pullRequestTargetBranchFilters(t *testing.T) {
	t.Parallel()

	w := audit.Workflow{
		Path: ".github/workflows/ci.yml", Events: []string{"pull_request_target"},
		PullRequestTargetBranchesIgnore: []string{"main"},
	}
	wantCell(t, prCell(t, w), rules.Fail, "")
	w.PullRequestTargetBranchesIgnore = []string{"wip/**"}
	wantCell(t, prCell(t, w), rules.Pass, "")
}

// TestCIOnPR_branchFiltersFollowTheDefaultBranch: the filters are matched
// against the repository's own default branch, not a fixed name.
func TestCIOnPR_branchFiltersFollowTheDefaultBranch(t *testing.T) {
	t.Parallel()

	w := audit.Workflow{
		Path: ".github/workflows/ci.yml", Events: []string{"pull_request", "push"},
		PullRequestBranches: []string{"main"},
	}
	pr, push := triggerCells(t, "trunk", w)
	wantCell(t, pr, rules.Fail, "")
	wantCell(t, push, rules.Fail, "")
	pr, push = triggerCells(t, "main", w)
	wantCell(t, pr, rules.Pass, "")
	wantCell(t, push, rules.Pass, "")
}

// TestCIOnPush_needsPullRequestsIntoTheDefaultBranch: ci_on_push asks for
// one workflow that runs on pull requests and on a push to the default
// branch, so a workflow whose pull request filters exclude the default
// branch does not satisfy it even when its push trigger admits it.
func TestCIOnPush_needsPullRequestsIntoTheDefaultBranch(t *testing.T) {
	t.Parallel()

	w := audit.Workflow{
		Path: ".github/workflows/ci.yml", Events: []string{"pull_request", "push"},
		PullRequestBranches: []string{"release/**"}, PushBranches: []string{"main"},
	}
	wantCell(t, pushCell(t, "main", w), rules.Fail, "")
}
