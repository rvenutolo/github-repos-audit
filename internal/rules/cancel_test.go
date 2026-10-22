package rules_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// cancelReport judges one tools repository, which standard-types.toml holds
// to ci_cancel_superseded = "required", holding the given workflows.
func cancelReport(t *testing.T, workflows ...audit.Workflow) *rules.Report {
	t.Helper()
	r := fixture("alpha", "tools")
	r.Workflows = workflows
	rep, err := rules.Evaluate(&audit.Snapshot{
		GeneratedAt: cacheNow, Types: standardTypes(), Identity: fixtureIdentity(), Hermetic: fixtureHermetic(),
		Repos: []audit.Repo{r},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	return rep
}

// cancelCell is the ci_cancel_superseded cell of cancelReport.
func cancelCell(t *testing.T, workflows ...audit.Workflow) rules.Cell {
	t.Helper()
	return cancelReport(t, workflows...).Repos[0].Cell(rules.CheckCICancelSuperseded)
}

// cancelling is a concurrency setting whose cancel-in-progress is v, as
// written.
func cancelling(v string) audit.Concurrency {
	return audit.Concurrency{Set: true, CancelInProgress: v}
}

// prWorkflow is a workflow at path triggered by pull_request, with the given
// workflow-level concurrency and jobs.
func prWorkflow(path string, c audit.Concurrency, jobs ...audit.Job) audit.Workflow {
	return audit.Workflow{Path: path, Events: []string{"pull_request", "push"}, Concurrency: c, Jobs: jobs}
}

// wantPlainPass asserts a pass that carries no value: there is nothing
// missing to count.
func wantPlainPass(t *testing.T, got rules.Cell) {
	t.Helper()
	if got.Verdict != rules.Pass || got.Value != "" {
		t.Errorf("cell = %v %q, want a plain pass", got.Verdict, got.Value)
	}
}

// TestEvaluate_cancelSuperseded_workflowLevel: a workflow-level
// cancel-in-progress cancels every job a newer push supersedes, so it passes
// whatever the jobs set; without it, and with no job setting it, it fails.
func TestEvaluate_cancelSuperseded_workflowLevel(t *testing.T) {
	t.Parallel()

	wantPlainPass(t, cancelCell(t, prWorkflow(".github/workflows/ci.yml", cancelling("true"), audit.Job{ID: "check"})))

	got := cancelCell(t, prWorkflow(".github/workflows/ci.yml", audit.Concurrency{Set: true}, audit.Job{ID: "check"}))
	wantCell(t, got, rules.Fail, "1 workflow")
}

// TestEvaluate_cancelSuperseded_everyJobLevel: without a workflow-level
// setting, the workflow passes only when every job cancels its own
// superseded run; one job that does not keeps running to the end.
func TestEvaluate_cancelSuperseded_everyJobLevel(t *testing.T) {
	t.Parallel()

	every := prWorkflow(".github/workflows/ci.yml", audit.Concurrency{},
		audit.Job{ID: "check", Concurrency: cancelling("true")},
		audit.Job{ID: "lint", Concurrency: cancelling("true")},
	)
	wantPlainPass(t, cancelCell(t, every))

	oneLacks := prWorkflow(".github/workflows/ci.yml", audit.Concurrency{},
		audit.Job{ID: "check", Concurrency: cancelling("true")},
		audit.Job{ID: "lint"},
	)
	wantCell(t, cancelCell(t, oneLacks), rules.Fail, "1 workflow")

	noJobs := prWorkflow(".github/workflows/ci.yml", audit.Concurrency{})
	wantCell(t, cancelCell(t, noJobs), rules.Fail, "1 workflow")
}

// TestEvaluate_cancelSuperseded_literalFalseFails: cancel-in-progress: false
// is written but cancels nothing, at either level, in any case YAML reads
// as the same boolean.
func TestEvaluate_cancelSuperseded_literalFalseFails(t *testing.T) {
	t.Parallel()

	for _, v := range []string{"false", "False", "FALSE"} {
		wf := prWorkflow(".github/workflows/ci.yml", cancelling(v), audit.Job{ID: "check"})
		wantCell(t, cancelCell(t, wf), rules.Fail, "1 workflow")

		job := prWorkflow(".github/workflows/ci.yml", audit.Concurrency{}, audit.Job{ID: "check", Concurrency: cancelling(v)})
		wantCell(t, cancelCell(t, job), rules.Fail, "1 workflow")
	}
}

// TestEvaluate_cancelSuperseded_falseExpressionFails: an expression that is
// only the word false, in any case with any spacing inside the braces, is the
// literal false written another way.
func TestEvaluate_cancelSuperseded_falseExpressionFails(t *testing.T) {
	t.Parallel()

	for _, v := range []string{"${{ false }}", "${{false}}", "${{ FALSE }}", "${{\tFalse  }}", " ${{ false }} "} {
		wf := prWorkflow(".github/workflows/ci.yml", cancelling(v), audit.Job{ID: "check"})
		wantCell(t, cancelCell(t, wf), rules.Fail, "1 workflow")

		job := prWorkflow(".github/workflows/ci.yml", audit.Concurrency{}, audit.Job{ID: "check", Concurrency: cancelling(v)})
		wantCell(t, cancelCell(t, job), rules.Fail, "1 workflow")
	}
}

// TestEvaluate_cancelSuperseded_expressionCounts: an expression, typically
// cancelling only pull-request runs, is a deliberate setting, so it counts
// as set at either level.
func TestEvaluate_cancelSuperseded_expressionCounts(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{"${{ github.event_name == 'pull_request' }}", "${{ !false }}", "${{ false || true }}"} {
		wantPlainPass(t, cancelCell(t, prWorkflow(".github/workflows/ci.yml", cancelling(expr), audit.Job{ID: "check"})))
		wantPlainPass(t, cancelCell(t,
			prWorkflow(".github/workflows/ci.yml", audit.Concurrency{}, audit.Job{ID: "check", Concurrency: cancelling(expr)})))
	}
}

// TestEvaluate_cancelSuperseded_pushOnlyIsNA: only pull-request workflows
// are judged, so a repository whose workflows run on other events alone, or
// that has none, has nothing to judge; pull_request_target counts like
// pull_request.
func TestEvaluate_cancelSuperseded_pushOnlyIsNA(t *testing.T) {
	t.Parallel()

	push := audit.Workflow{Path: ".github/workflows/release.yml", Events: []string{"push", "schedule"}, Jobs: []audit.Job{{ID: "release"}}}
	if got := cancelCell(t, push); got.Verdict != rules.NA {
		t.Errorf("push-only cell = %v %q, want n/a", got.Verdict, got.Value)
	}
	if got := cancelCell(t); got.Verdict != rules.NA {
		t.Errorf("no-workflow cell = %v %q, want n/a", got.Verdict, got.Value)
	}

	target := audit.Workflow{Path: ".github/workflows/label.yml", Events: []string{"pull_request_target"}, Jobs: []audit.Job{{ID: "label"}}}
	wantCell(t, cancelCell(t, push, target), rules.Fail, "1 workflow")
}

// TestEvaluate_cancelSuperseded_unreadableWorkflowFails: a workflow that
// does not parse may well be a pull-request workflow without the setting,
// so it fails the check rather than passing unseen, and the list names it
// as unreadable.
func TestEvaluate_cancelSuperseded_unreadableWorkflowFails(t *testing.T) {
	t.Parallel()

	rep := cancelReport(t, audit.Workflow{Path: ".github/workflows/broken.yml", ParseError: "invalid YAML"})
	wantCell(t, rep.Repos[0].Cell(rules.CheckCICancelSuperseded), rules.Fail, "1 workflow")
	want := []rules.RepoList[string]{{Repo: "alpha", Items: []string{"unreadable .github/workflows/broken.yml"}}}
	if diff := cmp.Diff(want, rep.CancelMissing); diff != "" {
		t.Errorf("CancelMissing differs (-want +got):\n%s", diff)
	}
}

// TestEvaluate_cancelSuperseded_list: the list names each failing
// repository's offending workflows in path order, whatever order they were
// collected in, and leaves out the workflows that pass.
func TestEvaluate_cancelSuperseded_list(t *testing.T) {
	t.Parallel()

	rep := cancelReport(t,
		prWorkflow(".github/workflows/z.yml", audit.Concurrency{}, audit.Job{ID: "check"}),
		prWorkflow(".github/workflows/m.yml", cancelling("true"), audit.Job{ID: "check"}),
		audit.Workflow{Path: ".github/workflows/b.yml", ParseError: "invalid YAML"},
		prWorkflow(".github/workflows/a.yml", cancelling("false"), audit.Job{ID: "check"}),
	)
	wantCell(t, rep.Repos[0].Cell(rules.CheckCICancelSuperseded), rules.Fail, "3 workflows")
	want := []rules.RepoList[string]{{Repo: "alpha", Items: []string{
		".github/workflows/a.yml", "unreadable .github/workflows/b.yml", ".github/workflows/z.yml",
	}}}
	if diff := cmp.Diff(want, rep.CancelMissing); diff != "" {
		t.Errorf("CancelMissing differs (-want +got):\n%s", diff)
	}
}

// TestEvaluate_cancelSuperseded_listedWhenFailingOrInfo: the list is the
// detail behind a cell's count, so it names a repository whose cell fails,
// or whose type shows the check as info and the cell counts something. A
// repository that passes, an info one with nothing to count, and one
// excused by not_required are not on it.
func TestEvaluate_cancelSuperseded_listedWhenFailingOrInfo(t *testing.T) {
	t.Parallel()

	missing := func(name, typ string) audit.Repo {
		r := fixture(name, rules.Type(typ))
		r.Workflows = []audit.Workflow{prWorkflow(".github/workflows/ci.yml", audit.Concurrency{}, audit.Job{ID: "check"})}
		return r
	}
	passing := fixture("alpha", "tools")
	passing.Workflows = []audit.Workflow{prWorkflow(".github/workflows/ci.yml", cancelling("true"), audit.Job{ID: "check"})}
	excused := missing("bravo", "tools")
	excused.Overrides = map[string]string{"ci_cancel_superseded": rules.OverrideNotRequired}
	infoPassing := fixture("charlie", "content")
	infoPassing.Workflows = passing.Workflows
	rep, err := rules.Evaluate(&audit.Snapshot{
		GeneratedAt: cacheNow, Types: typesWith("content", rules.CheckCICancelSuperseded, "info"),
		Identity: fixtureIdentity(), Hermetic: fixtureHermetic(),
		Repos: []audit.Repo{passing, excused, infoPassing, missing("delta", "content"), missing("echo", "tools")},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	wantCell(t, rep.Repos[3].Cell(rules.CheckCICancelSuperseded), rules.Info, "1 workflow")
	want := []rules.RepoList[string]{
		{Repo: "delta", Items: []string{".github/workflows/ci.yml"}},
		{Repo: "echo", Items: []string{".github/workflows/ci.yml"}},
	}
	if diff := cmp.Diff(want, rep.CancelMissing); diff != "" {
		t.Errorf("CancelMissing differs (-want +got):\n%s", diff)
	}
}

// TestEvaluate_cancelSuperseded_typesWithoutSynchronize: a pull-request
// trigger whose listed types exclude synchronize never sees a push to an open
// pull request, so nothing is superseded through it and the workflow is not
// judged; one that lists synchronize, or no types at all (GitHub's default
// set includes it), is.
func TestEvaluate_cancelSuperseded_typesWithoutSynchronize(t *testing.T) {
	t.Parallel()

	push := audit.Workflow{Path: ".github/workflows/release.yml", Events: []string{"push"}, Jobs: []audit.Job{{ID: "release"}}}
	closedOnly := prWorkflow(".github/workflows/cleanup.yml", audit.Concurrency{}, audit.Job{ID: "clean"})
	closedOnly.PullRequestTypes = []string{"closed"}
	if got := cancelCell(t, push, closedOnly); got.Verdict != rules.NA {
		t.Errorf("closed-only cell = %v %q, want n/a", got.Verdict, got.Value)
	}

	withSync := prWorkflow(".github/workflows/ci.yml", audit.Concurrency{}, audit.Job{ID: "check"})
	withSync.PullRequestTypes = []string{"opened", "synchronize"}
	wantCell(t, cancelCell(t, push, withSync), rules.Fail, "1 workflow")

	targetClosed := audit.Workflow{
		Path: ".github/workflows/label.yml", Events: []string{"pull_request", "pull_request_target"},
		PullRequestTargetTypes: []string{"closed"}, Jobs: []audit.Job{{ID: "label"}},
	}
	wantCell(t, cancelCell(t, targetClosed), rules.Fail, "1 workflow")

	targetSync := audit.Workflow{
		Path: ".github/workflows/label.yml", Events: []string{"pull_request_target"},
		PullRequestTargetTypes: []string{"synchronize"}, Jobs: []audit.Job{{ID: "label"}},
	}
	wantCell(t, cancelCell(t, targetSync), rules.Fail, "1 workflow")
}

// TestEvaluate_cancelSuperseded_unreadableBesideUnjudged: an unreadable
// workflow still fails the check when the only readable pull-request
// workflow cannot be superseded.
func TestEvaluate_cancelSuperseded_unreadableBesideUnjudged(t *testing.T) {
	t.Parallel()

	closedOnly := prWorkflow(".github/workflows/cleanup.yml", audit.Concurrency{}, audit.Job{ID: "clean"})
	closedOnly.PullRequestTypes = []string{"closed"}
	rep := cancelReport(t, closedOnly, audit.Workflow{Path: ".github/workflows/broken.yml", ParseError: "invalid YAML"})
	wantCell(t, rep.Repos[0].Cell(rules.CheckCICancelSuperseded), rules.Fail, "1 workflow")
	want := []rules.RepoList[string]{{Repo: "alpha", Items: []string{"unreadable .github/workflows/broken.yml"}}}
	if diff := cmp.Diff(want, rep.CancelMissing); diff != "" {
		t.Errorf("CancelMissing differs (-want +got):\n%s", diff)
	}
}
