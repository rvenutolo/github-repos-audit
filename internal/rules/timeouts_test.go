package rules_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// timeoutReport judges one tools repository, which standard-types.toml holds
// to job_timeouts = "required", holding the given workflows.
func timeoutReport(t *testing.T, workflows ...audit.Workflow) *rules.Report {
	t.Helper()
	r := fixture("alpha", "tools")
	r.Workflows = workflows
	rep := mustEvaluateOne(t, r)
	return rep
}

// timeoutCell is the job_timeouts cell of timeoutReport.
func timeoutCell(t *testing.T, workflows ...audit.Workflow) rules.Cell {
	t.Helper()
	return timeoutReport(t, workflows...).Repos[0].Cell(rules.CheckJobTimeouts)
}

// jobsWorkflow is a push workflow at path holding the given jobs.
func jobsWorkflow(path string, jobs ...audit.Job) audit.Workflow {
	return audit.Workflow{Path: path, Events: []string{"push"}, Jobs: jobs}
}

// TestEvaluate_jobTimeouts_everyJobSet: a repository whose every job sets
// timeout-minutes passes with nothing to count; one job without it fails,
// and the cell counts it in the singular.
func TestEvaluate_jobTimeouts_everyJobSet(t *testing.T) {
	t.Parallel()

	wantPlainPass(t, timeoutCell(t,
		jobsWorkflow(".github/workflows/ci.yml", audit.Job{ID: "check", TimeoutMinutes: "15"}, audit.Job{ID: "lint", TimeoutMinutes: "5"}),
	))

	got := timeoutCell(t,
		jobsWorkflow(".github/workflows/ci.yml", audit.Job{ID: "check", TimeoutMinutes: "15"}, audit.Job{ID: "lint"}),
	)
	wantCell(t, got, rules.Fail, "1 job")
}

// TestEvaluate_jobTimeouts_zeroCounts: timeout-minutes: 0 is set, so it
// passes; the check asks that a timeout be declared, not that it be sensible.
func TestEvaluate_jobTimeouts_zeroCounts(t *testing.T) {
	t.Parallel()

	wantPlainPass(t, timeoutCell(t,
		jobsWorkflow(".github/workflows/ci.yml", audit.Job{ID: "check", TimeoutMinutes: "0"}),
	))
}

// TestEvaluate_jobTimeouts_reusableJobExcluded: a job that calls a reusable
// workflow cannot set timeout-minutes itself, since its steps run in the
// called file, so it is neither counted nor enough on its own to make the
// check apply.
func TestEvaluate_jobTimeouts_reusableJobExcluded(t *testing.T) {
	t.Parallel()

	wantPlainPass(t, timeoutCell(t,
		jobsWorkflow(".github/workflows/ci.yml", audit.Job{ID: "check", TimeoutMinutes: "15"}, audit.Job{ID: "docs", Reusable: true}),
	))

	if got := timeoutCell(t, jobsWorkflow(".github/workflows/ci.yml", audit.Job{ID: "docs", Reusable: true})); got.Verdict != rules.NA {
		t.Errorf("reusable-only cell = %v %q, want n/a", got.Verdict, got.Value)
	}
}

// TestEvaluate_jobTimeouts_expressionCounts: an expression for
// timeout-minutes, such as one that gives a slower runner longer, is a
// deliberate bound, so it counts as set.
func TestEvaluate_jobTimeouts_expressionCounts(t *testing.T) {
	t.Parallel()

	wantPlainPass(t, timeoutCell(t,
		jobsWorkflow(".github/workflows/ci.yml", audit.Job{ID: "check", TimeoutMinutes: "${{ matrix.os == 'macos-latest' && 30 || 15 }}"}),
	))
}

// TestEvaluate_jobTimeouts_noWorkflowsIsNA: a repository with no workflow,
// or with workflows that hold no job, has nothing to bound.
func TestEvaluate_jobTimeouts_noWorkflowsIsNA(t *testing.T) {
	t.Parallel()

	if got := timeoutCell(t); got.Verdict != rules.NA {
		t.Errorf("no-workflow cell = %v %q, want n/a", got.Verdict, got.Value)
	}
	if got := timeoutCell(t, jobsWorkflow(".github/workflows/ci.yml")); got.Verdict != rules.NA {
		t.Errorf("no-job cell = %v %q, want n/a", got.Verdict, got.Value)
	}
}

// TestEvaluate_jobTimeouts_unreadable: a workflow that does not parse may
// hold jobs without a timeout, so it fails the check rather than passing
// unseen, and the list names it as unreadable by its full path.
func TestEvaluate_jobTimeouts_unreadable(t *testing.T) {
	t.Parallel()

	rep := timeoutReport(t, audit.Workflow{Path: ".github/workflows/broken.yml", ParseError: "invalid YAML"})
	wantCell(t, rep.Repos[0].Cell(rules.CheckJobTimeouts), rules.Fail, "1 job")
	want := []rules.RepoList[string]{{Repo: "alpha", Items: []string{"unreadable .github/workflows/broken.yml"}}}
	if diff := cmp.Diff(want, rep.TimeoutMissing); diff != "" {
		t.Errorf("TimeoutMissing differs (-want +got):\n%s", diff)
	}
}

// TestEvaluate_jobTimeouts_list: the list names each offending job as
// "<workflow file name> / <job id>", workflows in path order whatever order
// they were collected in and jobs in file order within each, beside the
// unreadable workflows in their place, and leaves out the jobs that pass.
func TestEvaluate_jobTimeouts_list(t *testing.T) {
	t.Parallel()

	rep := timeoutReport(t,
		jobsWorkflow(".github/workflows/z.yml", audit.Job{ID: "test"}, audit.Job{ID: "check"}),
		audit.Workflow{Path: ".github/workflows/b.yml", ParseError: "invalid YAML"},
		jobsWorkflow(".github/workflows/a.yml", audit.Job{ID: "lint"}, audit.Job{ID: "check", TimeoutMinutes: "10"}),
	)
	wantCell(t, rep.Repos[0].Cell(rules.CheckJobTimeouts), rules.Fail, "4 jobs")
	want := []rules.RepoList[string]{{Repo: "alpha", Items: []string{
		"a.yml / lint", "unreadable .github/workflows/b.yml", "z.yml / test", "z.yml / check",
	}}}
	if diff := cmp.Diff(want, rep.TimeoutMissing); diff != "" {
		t.Errorf("TimeoutMissing differs (-want +got):\n%s", diff)
	}
}

// TestEvaluate_jobTimeouts_listedWhenFailingOrInfo: the list is the detail
// behind a cell's count, so it names a repository whose cell fails, or whose
// type shows the check as info and the cell counts something. A repository
// that passes, an info one with nothing to count, and one excused by
// not_required are not on it.
func TestEvaluate_jobTimeouts_listedWhenFailingOrInfo(t *testing.T) {
	t.Parallel()

	missing := func(name, typ string) audit.Repo {
		r := fixture(name, rules.Type(typ))
		r.Workflows = []audit.Workflow{jobsWorkflow(".github/workflows/ci.yml", audit.Job{ID: "check"})}
		return r
	}
	passing := fixture("alpha", "tools")
	passing.Workflows = []audit.Workflow{jobsWorkflow(".github/workflows/ci.yml", audit.Job{ID: "check", TimeoutMinutes: "15"})}
	excused := missing("bravo", "tools")
	excused.Overrides = map[string]string{"job_timeouts": rules.OverrideNotRequired}
	infoPassing := fixture("charlie", "content")
	infoPassing.Workflows = passing.Workflows
	rep, err := rules.Evaluate(&audit.Snapshot{
		GeneratedAt: cacheNow, Types: typesWith("content", rules.CheckJobTimeouts, "info"),
		Identity: fixtureIdentity(),
		Repos:    []audit.Repo{passing, excused, infoPassing, missing("delta", "content"), missing("echo", "tools")},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	wantCell(t, rep.Repos[3].Cell(rules.CheckJobTimeouts), rules.Info, "1 job")
	want := []rules.RepoList[string]{
		{Repo: "delta", Items: []string{"ci.yml / check"}},
		{Repo: "echo", Items: []string{"ci.yml / check"}},
	}
	if diff := cmp.Diff(want, rep.TimeoutMissing); diff != "" {
		t.Errorf("TimeoutMissing differs (-want +got):\n%s", diff)
	}
}
