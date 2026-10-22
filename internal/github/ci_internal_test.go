package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// ciOID is a pattern oid in the fixture vocabulary's shape.
const ciOID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa00c1"

// decodeRepository decodes a repoQuery answer the way fetchRepository does,
// so these tests feed toAudit exactly the shape GitHub's JSON produces.
func decodeRepository(t *testing.T, body string) *repository {
	t.Helper()

	var r repository
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("unmarshal the fixture: %v", err)
	}
	return &r
}

// workflowRun decodes one entry of the runs list and the jobs list read for
// it, the two REST answers fetchCI pairs up, so timeCommit is fed exactly the
// shape GitHub's JSON produces.
func workflowRun(t *testing.T, run, jobs string) ciRun {
	t.Helper()

	var out ciRun
	if err := json.Unmarshal([]byte(run), &out.run); err != nil {
		t.Fatalf("unmarshal the run: %v", err)
	}
	if err := json.Unmarshal([]byte(jobs), &out.jobs); err != nil {
		t.Fatalf("unmarshal the jobs: %v", err)
	}
	return out
}

// run renders one workflow_runs entry for ciOID.
func run(name, status, createdAt string) string {
	return `{"id":4000001,"name":"` + name + `","head_sha":"` + ciOID + `","status":"` + status +
		`","created_at":"` + createdAt + `"}`
}

// jobList renders a jobs answer whose total_count is the number of jobs
// listed, which is what a list read in full looks like.
func jobList(jobs ...string) string {
	return `{"total_count":` + strconv.Itoa(len(jobs)) + `,"jobs":[` + strings.Join(jobs, ",") + `]}`
}

// completedJob is a finished job.
func completedJob(name, startedAt, completedAt string) string {
	return `{"name":"` + name + `","status":"completed","started_at":"` + startedAt + `","completed_at":"` + completedAt + `"}`
}

// TestTimeCommit_waitSpansParallelRuns: two workflows run side by side, so the
// wait is from the first run's creation to the last job's completion, not the
// sum of the two, and the time the first workflow sat queued is in it.
func TestTimeCommit_waitSpansParallelRuns(t *testing.T) {
	t.Parallel()

	got := timeCommit(audit.CICommit{OID: ciOID}, []ciRun{
		workflowRun(t, run("ci", "completed", "2026-10-01T10:00:00Z"),
			jobList(completedJob("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z"))),
		workflowRun(t, run("docs", "completed", "2026-10-01T10:00:05Z"),
			jobList(completedJob("docs", "2026-10-01T10:00:30Z", "2026-10-01T10:07:30Z"))),
	})

	want := audit.CICommit{
		OID:         ciOID,
		Complete:    true,
		WaitSeconds: 450,
		Jobs:        []audit.CIJob{{Label: "ci / check", Seconds: 120}, {Label: "docs / docs", Seconds: 420}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("commit mismatch (-want +got):\n%s", diff)
	}
}

// TestTimeCommit_inProgressRun: a run still going makes the commit
// incomplete, and an incomplete commit carries no wait, because the wait it
// would show is a lower bound rather than the wait. The finished job's
// duration is still a fact and is kept.
func TestTimeCommit_inProgressRun(t *testing.T) {
	t.Parallel()

	got := timeCommit(audit.CICommit{OID: ciOID}, []ciRun{
		workflowRun(t, run("ci", "in_progress", "2026-10-01T10:00:00Z"), jobList(
			completedJob("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z"),
			`{"name":"test","status":"in_progress","started_at":"2026-10-01T10:01:00Z","completed_at":null}`,
		)),
	})

	want := audit.CICommit{OID: ciOID, Jobs: []audit.CIJob{{Label: "ci / check", Seconds: 120}}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("commit mismatch (-want +got):\n%s", diff)
	}
}

// TestTimeCommit_queuedRunAmongFinishedOnes: one run still queued holds the
// whole commit open, however finished the others are.
func TestTimeCommit_queuedRunAmongFinishedOnes(t *testing.T) {
	t.Parallel()

	got := timeCommit(audit.CICommit{OID: ciOID}, []ciRun{
		workflowRun(t, run("ci", "completed", "2026-10-01T10:00:00Z"),
			jobList(completedJob("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z"))),
		workflowRun(t, run("docs", "queued", "2026-10-01T10:00:05Z"), jobList()),
	})

	if got.Complete || got.WaitSeconds != 0 {
		t.Errorf("Complete = %t, WaitSeconds = %d, want false and 0", got.Complete, got.WaitSeconds)
	}
}

// TestTimeCommit_completedJobWithoutTimestamp: a job GitHub calls completed
// but gives no completion time cannot be placed on the timeline, so the
// latest completion is unknown and the commit is not complete. Its missing
// time must not read as an early finish.
func TestTimeCommit_completedJobWithoutTimestamp(t *testing.T) {
	t.Parallel()

	got := timeCommit(audit.CICommit{OID: ciOID}, []ciRun{
		workflowRun(t, run("ci", "completed", "2026-10-01T10:00:00Z"), jobList(
			completedJob("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z"),
			`{"name":"test","status":"completed","started_at":null,"completed_at":null}`,
		)),
	})

	want := audit.CICommit{OID: ciOID, Jobs: []audit.CIJob{{Label: "ci / check", Seconds: 120}}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("commit mismatch (-want +got):\n%s", diff)
	}
}

// TestTimeCommit_unfinishedJobInFinishedRun: a job not yet completed has no
// completion to place, so even under a run GitHub calls completed it leaves
// the commit's end unknown.
func TestTimeCommit_unfinishedJobInFinishedRun(t *testing.T) {
	t.Parallel()

	got := timeCommit(audit.CICommit{OID: ciOID}, []ciRun{
		workflowRun(t, run("ci", "completed", "2026-10-01T10:00:00Z"), jobList(
			completedJob("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z"),
			`{"name":"test","status":"queued","started_at":null,"completed_at":null}`,
		)),
	})

	if got.Complete || got.WaitSeconds != 0 {
		t.Errorf("Complete = %t, WaitSeconds = %d, want false and 0", got.Complete, got.WaitSeconds)
	}
}

// TestTimeCommit_skippedJobTakesNoTime: GitHub stamps a skipped job's
// completion a second before its start. That is a job that took no time, not
// a negative one, and it still finishes the commit.
func TestTimeCommit_skippedJobTakesNoTime(t *testing.T) {
	t.Parallel()

	got := timeCommit(audit.CICommit{OID: ciOID}, []ciRun{
		workflowRun(t, run("ci", "completed", "2026-10-01T10:00:00Z"), jobList(
			completedJob("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z"),
			completedJob("lint", "2026-10-01T10:00:02Z", "2026-10-01T10:00:01Z"),
		)),
	})

	want := audit.CICommit{
		OID:         ciOID,
		Complete:    true,
		WaitSeconds: 180,
		Jobs:        []audit.CIJob{{Label: "ci / check", Seconds: 120}, {Label: "ci / lint", Seconds: 0}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("commit mismatch (-want +got):\n%s", diff)
	}
}

// TestTimeCommit_truncatedJobsIsIncomplete: a jobs list shorter than its
// total_count might hide the job that finished last, so the commit is
// incomplete even when every job shown has finished. The jobs shown are
// still facts and are kept.
func TestTimeCommit_truncatedJobsIsIncomplete(t *testing.T) {
	t.Parallel()

	got := timeCommit(audit.CICommit{OID: ciOID}, []ciRun{
		workflowRun(t, run("ci", "completed", "2026-10-01T10:00:00Z"),
			`{"total_count":2,"jobs":[`+completedJob("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z")+`]}`),
	})

	want := audit.CICommit{OID: ciOID, Jobs: []audit.CIJob{{Label: "ci / check", Seconds: 120}}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("commit mismatch (-want +got):\n%s", diff)
	}
}

// TestTimeCommit_noRunsIsIncomplete: a commit no workflow ran on has nothing
// to wait for, which is not the same as a zero wait, so it is recorded with no
// jobs and not complete.
func TestTimeCommit_noRunsIsIncomplete(t *testing.T) {
	t.Parallel()

	if diff := cmp.Diff(audit.CICommit{OID: ciOID}, timeCommit(audit.CICommit{OID: ciOID}, nil)); diff != "" {
		t.Errorf("commit mismatch (-want +got):\n%s", diff)
	}
}

// TestTimeCommit_finishedRunWithoutJobsIsIncomplete: a run that ended before
// any job started (a workflow file GitHub could not start, say) gives no
// completion to end the wait at, so it is not read as a zero wait.
func TestTimeCommit_finishedRunWithoutJobsIsIncomplete(t *testing.T) {
	t.Parallel()

	got := timeCommit(audit.CICommit{OID: ciOID}, []ciRun{
		workflowRun(t, run("ci", "completed", "2026-10-01T10:00:00Z"), jobList()),
	})
	if diff := cmp.Diff(audit.CICommit{OID: ciOID}, got); diff != "" {
		t.Errorf("commit mismatch (-want +got):\n%s", diff)
	}
}

// TestTimeCommit_rerunExtendsTheWait: the jobs list is the latest attempt's,
// while the run keeps its first creation time, so a re-run an hour later
// stretches the wait to cover it. A flaky job that needed a re-run reads as
// the wait it caused.
func TestTimeCommit_rerunExtendsTheWait(t *testing.T) {
	t.Parallel()

	got := timeCommit(audit.CICommit{OID: ciOID}, []ciRun{
		workflowRun(t, run("ci", "completed", "2026-10-01T10:00:00Z"),
			jobList(completedJob("test", "2026-10-01T11:00:00Z", "2026-10-01T11:02:00Z"))),
	})

	want := audit.CICommit{
		OID:         ciOID,
		Complete:    true,
		WaitSeconds: 3720,
		Jobs:        []audit.CIJob{{Label: "ci / test", Seconds: 120}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("commit mismatch (-want +got):\n%s", diff)
	}
}

// TestTimeCommit_jobLabels: a job is named by the workflow that ran it and its
// own name, so two workflows' same-named jobs stay apart.
func TestTimeCommit_jobLabels(t *testing.T) {
	t.Parallel()

	job := completedJob("check", "2026-10-01T10:01:00Z", "2026-10-01T10:02:00Z")
	got := timeCommit(audit.CICommit{OID: ciOID}, []ciRun{
		workflowRun(t, run("ci", "completed", "2026-10-01T10:00:00Z"), jobList(job)),
		workflowRun(t, run("docs", "completed", "2026-10-01T10:00:00Z"), jobList(job)),
	})

	want := []audit.CIJob{{Label: "ci / check", Seconds: 60}, {Label: "docs / check", Seconds: 60}}
	if diff := cmp.Diff(want, got.Jobs); diff != "" {
		t.Errorf("Jobs mismatch (-want +got):\n%s", diff)
	}
}

// TestTimeCommit_keepsThePullRequestFacts: timing a commit adds to what
// repoQuery recorded about it and never replaces it.
func TestTimeCommit_keepsThePullRequestFacts(t *testing.T) {
	t.Parallel()

	sampled := audit.CICommit{
		OID: ciOID, PRNumber: 7, MergedAt: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), AuthorBot: true,
	}
	got := timeCommit(sampled, []ciRun{
		workflowRun(t, run("ci", "completed", "2026-10-01T10:00:00Z"),
			jobList(completedJob("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z"))),
	})

	want := sampled
	want.Complete = true
	want.WaitSeconds = 180
	want.Jobs = []audit.CIJob{{Label: "ci / check", Seconds: 120}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("commit mismatch (-want +got):\n%s", diff)
	}
}

// mergedPR renders one merged_pull_requests node whose head commit is ciOID.
func mergedPR(number, mergedAt, author string) string {
	return `{"number":` + number + `,"merged_at":"` + mergedAt + `","author":` + author +
		`,"commits":{"nodes":[{"commit":{"oid":"` + ciOID + `"}}]}}`
}

// TestToAudit_ciBotAuthor: a pull request a GitHub App opened is marked, so the
// report can leave it out of the wait a person sits through; one whose author
// account was deleted is not a bot. The pull request's number and merge time
// ride along, the time normalised to UTC like every instant in audit.json, and
// the order is GitHub's. Nothing is timed yet: that is fetchCI's, over REST.
func TestToAudit_ciBotAuthor(t *testing.T) {
	t.Parallel()

	body := `{"merged_pull_requests":{"nodes":[` +
		mergedPR("7", "2026-10-02T14:00:00+02:00", `{"type":"Bot"}`) + `,` +
		mergedPR("5", "2026-10-01T12:00:00Z", `{"type":"User"}`) + `,` +
		mergedPR("3", "2026-09-30T12:00:00Z", `null`) +
		`]}}`
	got := decodeRepository(t, body).toAudit("web-app").CI.PullRequests

	want := []audit.CICommit{
		{OID: ciOID, PRNumber: 7, MergedAt: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), AuthorBot: true},
		{OID: ciOID, PRNumber: 5, MergedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)},
		{OID: ciOID, PRNumber: 3, MergedAt: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("PullRequests mismatch (-want +got):\n%s", diff)
	}
	if got[0].MergedAt.Location() != time.UTC {
		t.Errorf("MergedAt location = %v, want UTC", got[0].MergedAt.Location())
	}
}

// TestToAudit_ciDefaultBranchSample: the recent default-branch commits are
// recorded by oid, newest first, ready to be timed.
func TestToAudit_ciDefaultBranchSample(t *testing.T) {
	t.Parallel()

	const older = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa00c2"
	body := `{"default_branch_ref":{"name":"main","target":{"oid":"` + ciOID + `","recent_commits":{"nodes":[` +
		`{"oid":"` + ciOID + `"},{"oid":"` + older + `"}]}}}}`
	got := decodeRepository(t, body).toAudit("web-app").CI.DefaultBranch

	if diff := cmp.Diff([]audit.CICommit{{OID: ciOID}, {OID: older}}, got); diff != "" {
		t.Errorf("DefaultBranch mismatch (-want +got):\n%s", diff)
	}
}

// TestToAudit_ciPullRequestWithoutCommits: a merged pull request GitHub lists
// no commit for has no head commit to time, so it contributes no entry rather
// than one with an empty oid.
func TestToAudit_ciPullRequestWithoutCommits(t *testing.T) {
	t.Parallel()

	body := `{"merged_pull_requests":{"nodes":[{"number":4,"merged_at":"2026-10-01T12:00:00Z",` +
		`"author":{"type":"User"},"commits":{"nodes":[]}}]}}`
	if got := decodeRepository(t, body).toAudit("web-app").CI; len(got.PullRequests) != 0 {
		t.Errorf("PullRequests = %+v, want none", got.PullRequests)
	}
}

// TestToAudit_ciEmptyRepo: a repository with no commits has no default branch
// to read, and a default branch whose target is not a commit has no recent
// commits; neither is an error.
func TestToAudit_ciEmptyRepo(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		`{"default_branch_ref":null}`,
		`{"default_branch_ref":{"name":"main","target":null}}`,
		`{"default_branch_ref":{"name":"main","target":{"oid":"` + ciOID + `"}}}`,
	} {
		if got := decodeRepository(t, body).toAudit("blank-repo").CI; len(got.DefaultBranch) != 0 {
			t.Errorf("%s: DefaultBranch = %+v, want none", body, got.DefaultBranch)
		}
	}
}

// ciBase is the repository path fetchCI is handed in these tests.
const ciBase = "/repos/gh-owner/web-app"

// ciRepoID is the repository id every run in these tests belongs to.
const ciRepoID int64 = 1

// branchRun renders one workflow_runs entry on branch, on a commit no sample
// names, so fetchCI reads no jobs for it and only its recency is in play. The
// run belongs to repository ciRepoID; headRepoID is the repository its head
// commit came from, ciRepoID for a run of the repository's own, another id
// for a fork's pull request, and nil for one whose fork has been deleted,
// which GitHub sends as a null head_repository.
func branchRun(id int, branch, event, createdAt string, headRepoID *int64) string {
	head := "null"
	if headRepoID != nil {
		head = `{"id":` + strconv.FormatInt(*headRepoID, 10) + `}`
	}
	return `{"id":` + strconv.Itoa(id) + `,"name":"ci","head_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb00c1",` +
		`"head_branch":"` + branch + `","event":"` + event + `","status":"completed","created_at":"` + createdAt + `",` +
		`"repository":{"id":` + strconv.FormatInt(ciRepoID, 10) + `},"head_repository":` + head + `}`
}

// runsList renders a workflow_runs list of the given entries.
func runsList(runs ...string) storagePage {
	return storagePage{body: `{"total_count":` + strconv.Itoa(len(runs)) + `,"workflow_runs":[` + strings.Join(runs, ",") + `]}`}
}

// TestFetchCI_lastDefaultBranchRun: the recency is the newest run the
// branch-filtered list answers, whatever its event, read with one request of
// its own. The timing page here is a busy repository's, its whole page on
// another branch, which is exactly the case a recency read off that page
// would miss.
func TestFetchCI_lastDefaultBranchRun(t *testing.T) {
	t.Parallel()

	c, seen := newStorageClient(t, map[string]storagePage{
		ciBase + "/actions/runs": runsList(
			branchRun(4000004, "feature", "pull_request", "2026-10-20T09:00:00Z", new(ciRepoID)),
			branchRun(4000003, "feature", "push", "2026-10-20T08:00:00Z", new(ciRepoID)),
		),
		ciBase + "/actions/runs?branch=main": runsList(
			branchRun(4000002, "main", "schedule", "2026-10-19T06:00:00+02:00", new(ciRepoID)),
		),
	})
	ci := audit.CI{DefaultBranch: []audit.CICommit{{OID: ciOID}}}

	if err := c.fetchCI(t.Context(), ciBase, "main", &ci); err != nil {
		t.Fatalf("fetchCI() error = %v, want nil", err)
	}

	want := time.Date(2026, 10, 19, 4, 0, 0, 0, time.UTC)
	if !ci.LastDefaultBranchRun.Equal(want) || ci.LastDefaultBranchRun.Location() != time.UTC {
		t.Errorf("LastDefaultBranchRun = %v, want %v in UTC", ci.LastDefaultBranchRun, want)
	}
	if n := seen(ciBase + "/actions/runs"); n != 1 {
		t.Errorf("timing runs list read %d times, want once", n)
	}
	if n := seen(ciBase + "/actions/runs?branch=main"); n != 1 {
		t.Errorf("default-branch runs list read %d times, want once", n)
	}
}

// TestFetchCI_lastDefaultBranchRunAsksForOneEscapedPage: the branch filter is
// query-escaped, so a branch name holding characters a query gives meaning
// to still names the whole branch, and one full page is asked for, since the
// newest runs on a branch name can all be forks' pull requests.
func TestFetchCI_lastDefaultBranchRunAsksForOneEscapedPage(t *testing.T) {
	t.Parallel()

	const branch = "release/1.0+x&y=z #1"
	var mu sync.Mutex
	var gotBranch, gotPerPage string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Has("branch") {
			mu.Lock()
			gotBranch, gotPerPage = q.Get("branch"), q.Get("per_page")
			mu.Unlock()
		}
		if _, err := w.Write([]byte(`{"workflow_runs":[]}`)); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	c, err := New(Options{Owner: "gh-owner", Token: "token", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	ci := audit.CI{DefaultBranch: []audit.CICommit{{OID: ciOID}}}

	if err := c.fetchCI(t.Context(), ciBase, branch, &ci); err != nil {
		t.Fatalf("fetchCI() error = %v, want nil", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotBranch != branch {
		t.Errorf("branch filter = %q, want %q", gotBranch, branch)
	}
	if gotPerPage != "100" {
		t.Errorf("per_page = %q, want 100", gotPerPage)
	}
}

// TestFetchCI_lastDefaultBranchRunSkipsForks: the branch filter matches a
// run's head branch, so a fork's pull request from a branch it named main is
// listed among the default branch's runs. The recency is the newest run of
// the repository's own, skipping both a fork's run and one whose fork was
// deleted (a null head repository, which cannot be shown to be this one).
// The fork runs are the newest entries, so taking the first entry fails
// here. A page of fork runs alone leaves the recency zero.
func TestFetchCI_lastDefaultBranchRunSkipsForks(t *testing.T) {
	t.Parallel()

	const forkID int64 = 2
	tests := []struct {
		name string
		runs []string
		want time.Time
	}{
		{
			name: "newest own run after fork runs",
			runs: []string{
				branchRun(4000007, "main", "pull_request", "2026-10-20T09:00:00Z", new(forkID)),
				branchRun(4000006, "main", "pull_request", "2026-10-20T08:00:00Z", nil),
				branchRun(4000005, "main", "schedule", "2026-10-20T07:00:00Z", new(ciRepoID)),
			},
			want: time.Date(2026, 10, 20, 7, 0, 0, 0, time.UTC),
		},
		{
			name: "only fork runs",
			runs: []string{
				branchRun(4000007, "main", "pull_request", "2026-10-20T09:00:00Z", new(forkID)),
				branchRun(4000006, "main", "pull_request", "2026-10-20T08:00:00Z", nil),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := newStorageClient(t, map[string]storagePage{
				ciBase + "/actions/runs":             runsList(),
				ciBase + "/actions/runs?branch=main": runsList(tt.runs...),
			})
			ci := audit.CI{DefaultBranch: []audit.CICommit{{OID: ciOID}}}

			if err := c.fetchCI(t.Context(), ciBase, "main", &ci); err != nil {
				t.Fatalf("fetchCI() error = %v, want nil", err)
			}

			if !ci.LastDefaultBranchRun.Equal(tt.want) {
				t.Errorf("LastDefaultBranchRun = %v, want %v", ci.LastDefaultBranchRun, tt.want)
			}
		})
	}
}

// TestFetchCI_noDefaultBranchRuns: a default-branch runs list with nothing
// in it leaves the recency zero, which the JSON then omits.
func TestFetchCI_noDefaultBranchRuns(t *testing.T) {
	t.Parallel()

	c, _ := newStorageClient(t, map[string]storagePage{
		ciBase + "/actions/runs":             runsList(branchRun(4000001, "feature", "push", "2026-10-20T09:00:00Z", new(ciRepoID))),
		ciBase + "/actions/runs?branch=main": runsList(),
	})
	ci := audit.CI{DefaultBranch: []audit.CICommit{{OID: ciOID}}}

	if err := c.fetchCI(t.Context(), ciBase, "main", &ci); err != nil {
		t.Fatalf("fetchCI() error = %v, want nil", err)
	}

	if !ci.LastDefaultBranchRun.IsZero() {
		t.Errorf("LastDefaultBranchRun = %v, want zero", ci.LastDefaultBranchRun)
	}
}

// TestFetchCI_noDefaultBranchAsksNothing: with no default branch name there
// is no branch to filter on, so no recency read is made (the fake answers
// only the timing list) and the recency stays zero.
func TestFetchCI_noDefaultBranchAsksNothing(t *testing.T) {
	t.Parallel()

	c, _ := newStorageClient(t, map[string]storagePage{ciBase + "/actions/runs": runsList()})
	ci := audit.CI{DefaultBranch: []audit.CICommit{{OID: ciOID}}}

	if err := c.fetchCI(t.Context(), ciBase, "", &ci); err != nil {
		t.Fatalf("fetchCI() error = %v, want nil", err)
	}

	if !ci.LastDefaultBranchRun.IsZero() {
		t.Errorf("LastDefaultBranchRun = %v, want zero", ci.LastDefaultBranchRun)
	}
}

// TestFetchCI_defaultBranchRunsFailureAborts: the recency read is held to
// the same rule as every other read, so an unexpected status aborts rather
// than reading as a branch nothing ran on.
func TestFetchCI_defaultBranchRunsFailureAborts(t *testing.T) {
	t.Parallel()

	c, _ := newStorageClient(t, map[string]storagePage{
		ciBase + "/actions/runs":             runsList(),
		ciBase + "/actions/runs?branch=main": {status: http.StatusUnauthorized, body: `{"message":"Bad credentials"}`},
	})
	ci := audit.CI{DefaultBranch: []audit.CICommit{{OID: ciOID}}}

	if err := c.fetchCI(t.Context(), ciBase, "main", &ci); !errors.Is(err, ErrUnexpectedStatus) {
		t.Errorf("fetchCI() error = %v, want it to wrap ErrUnexpectedStatus", err)
	}
}

// TestFetchCI_emptyRepoReadsNoRuns: a repository with no commits has none
// sampled and no branch a workflow could have run on, so it costs no runs
// request (the fake answers nothing) and records no recency.
func TestFetchCI_emptyRepoReadsNoRuns(t *testing.T) {
	t.Parallel()

	c, _ := newStorageClient(t, map[string]storagePage{})
	var ci audit.CI

	if err := c.fetchCI(t.Context(), ciBase, "", &ci); err != nil {
		t.Fatalf("fetchCI() error = %v, want nil", err)
	}

	if !ci.LastDefaultBranchRun.IsZero() {
		t.Errorf("LastDefaultBranchRun = %v, want zero", ci.LastDefaultBranchRun)
	}
}
