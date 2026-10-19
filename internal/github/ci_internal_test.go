package github

import (
	"encoding/json"
	"strings"
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

// onDefaultBranch wraps one commit's check_suites selection as the only
// recent default-branch commit.
func onDefaultBranch(suites string) string {
	return `{"default_branch_ref":{"name":"main","target":{"oid":"` + ciOID + `","recent_commits":{"nodes":[` +
		`{"oid":"` + ciOID + `","check_suites":` + suites + `}]}}}}`
}

// suites renders a check_suites selection from its nodes, untruncated.
func suites(nodes ...string) string {
	return `{"page_info":{"has_next_page":false},"nodes":[` + strings.Join(nodes, ",") + `]}`
}

// actionsSuite is a GitHub Actions suite for workflow "ci" with the given runs.
func actionsSuite(createdAt string, runs ...string) string {
	return `{"created_at":"` + createdAt + `","app":{"name":"GitHub Actions"},"workflow_run":{"workflow":{"name":"ci"}},` +
		`"check_runs":{"page_info":{"has_next_page":false},"nodes":[` + strings.Join(runs, ",") + `]}}`
}

// completedRun is a finished check run.
func completedRun(name, startedAt, completedAt string) string {
	return `{"name":"` + name + `","status":"COMPLETED","started_at":"` + startedAt + `","completed_at":"` + completedAt + `"}`
}

// onlyDefaultBranchCommit returns the single default-branch entry, failing
// when there is not exactly one.
func onlyDefaultBranchCommit(t *testing.T, ci audit.CI) audit.CICommit {
	t.Helper()

	if len(ci.DefaultBranch) != 1 {
		t.Fatalf("DefaultBranch has %d entries, want 1: %+v", len(ci.DefaultBranch), ci.DefaultBranch)
	}
	return ci.DefaultBranch[0]
}

// TestToAudit_ciWaitSpansParallelSuites: two workflows run side by side, so
// the wait is from the first suite's creation to the last run's completion,
// not the sum of the two, and the time the first workflow sat queued is in it.
func TestToAudit_ciWaitSpansParallelSuites(t *testing.T) {
	t.Parallel()

	body := onDefaultBranch(suites(
		actionsSuite("2026-10-01T10:00:00Z", completedRun("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z")),
		actionsSuite("2026-10-01T10:00:05Z", completedRun("test", "2026-10-01T10:00:30Z", "2026-10-01T10:07:30Z")),
	))
	got := onlyDefaultBranchCommit(t, decodeRepository(t, body).toAudit("web-app").CI)

	want := audit.CICommit{
		OID:         ciOID,
		Complete:    true,
		WaitSeconds: 450,
		Jobs:        []audit.CIJob{{Label: "ci / check", Seconds: 120}, {Label: "ci / test", Seconds: 420}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("default-branch commit mismatch (-want +got):\n%s", diff)
	}
}

// TestToAudit_ciIgnoresEmptySuites: an app that subscribes to checks but never
// posts one leaves a suite with no runs, created long before the real ones. It
// must neither stretch the wait nor hold the commit open.
func TestToAudit_ciIgnoresEmptySuites(t *testing.T) {
	t.Parallel()

	empty := `{"created_at":"2026-10-01T09:00:00Z","app":{"name":"Renovate"},"workflow_run":null,` +
		`"check_runs":{"page_info":{"has_next_page":false},"nodes":[]}}`
	body := onDefaultBranch(suites(
		empty,
		actionsSuite("2026-10-01T10:00:00Z", completedRun("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z")),
		actionsSuite("2026-10-01T10:00:05Z", completedRun("test", "2026-10-01T10:00:30Z", "2026-10-01T10:07:30Z")),
	))
	got := onlyDefaultBranchCommit(t, decodeRepository(t, body).toAudit("web-app").CI)

	if !got.Complete || got.WaitSeconds != 450 {
		t.Errorf("Complete = %t, WaitSeconds = %d, want true and 450", got.Complete, got.WaitSeconds)
	}
}

// TestToAudit_ciIncompleteRun: a run still going makes the commit incomplete,
// and an incomplete commit carries no wait, because the wait it would show is
// a lower bound rather than the wait. The finished run's duration is still a
// fact and is kept.
func TestToAudit_ciIncompleteRun(t *testing.T) {
	t.Parallel()

	body := onDefaultBranch(suites(actionsSuite("2026-10-01T10:00:00Z",
		completedRun("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z"),
		`{"name":"test","status":"IN_PROGRESS","started_at":"2026-10-01T10:01:00Z","completed_at":null}`,
	)))
	got := onlyDefaultBranchCommit(t, decodeRepository(t, body).toAudit("web-app").CI)

	want := audit.CICommit{OID: ciOID, Jobs: []audit.CIJob{{Label: "ci / check", Seconds: 120}}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("default-branch commit mismatch (-want +got):\n%s", diff)
	}
}

// TestToAudit_ciCompletedWithoutTimestamp: a run GitHub calls COMPLETED but
// gives no completion time cannot be placed on the timeline, so the latest
// completion is unknown and the commit is not complete. Its missing time must
// not read as an early finish.
func TestToAudit_ciCompletedWithoutTimestamp(t *testing.T) {
	t.Parallel()

	body := onDefaultBranch(suites(actionsSuite("2026-10-01T10:00:00Z",
		completedRun("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z"),
		`{"name":"test","status":"COMPLETED","started_at":null,"completed_at":null}`,
	)))
	got := onlyDefaultBranchCommit(t, decodeRepository(t, body).toAudit("web-app").CI)

	want := audit.CICommit{OID: ciOID, Jobs: []audit.CIJob{{Label: "ci / check", Seconds: 120}}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("default-branch commit mismatch (-want +got):\n%s", diff)
	}
}

// TestToAudit_ciSkippedRunTakesNoTime: GitHub stamps a skipped run's
// completion a second before its start. That is a run that took no time, not
// a negative one, and it still finishes the commit.
func TestToAudit_ciSkippedRunTakesNoTime(t *testing.T) {
	t.Parallel()

	body := onDefaultBranch(suites(actionsSuite("2026-10-01T10:00:00Z",
		completedRun("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z"),
		completedRun("lint", "2026-10-01T10:00:02Z", "2026-10-01T10:00:01Z"),
	)))
	got := onlyDefaultBranchCommit(t, decodeRepository(t, body).toAudit("web-app").CI)

	want := audit.CICommit{
		OID:         ciOID,
		Complete:    true,
		WaitSeconds: 180,
		Jobs:        []audit.CIJob{{Label: "ci / check", Seconds: 120}, {Label: "ci / lint", Seconds: 0}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("default-branch commit mismatch (-want +got):\n%s", diff)
	}
}

// TestToAudit_ciTruncatedIsIncomplete: a list cut short by the page size might
// hide the run that finished last, so either kind of truncation makes the
// commit incomplete even when every run shown has finished.
func TestToAudit_ciTruncatedIsIncomplete(t *testing.T) {
	t.Parallel()

	run := completedRun("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z")
	tests := []struct {
		name   string
		suites string
	}{
		{
			name: "the run list",
			suites: suites(`{"created_at":"2026-10-01T10:00:00Z","app":{"name":"GitHub Actions"},` +
				`"workflow_run":{"workflow":{"name":"ci"}},` +
				`"check_runs":{"page_info":{"has_next_page":true},"nodes":[` + run + `]}}`),
		},
		{
			name:   "the suite list",
			suites: `{"page_info":{"has_next_page":true},"nodes":[` + actionsSuite("2026-10-01T10:00:00Z", run) + `]}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := onlyDefaultBranchCommit(t, decodeRepository(t, onDefaultBranch(tc.suites)).toAudit("web-app").CI)
			if got.Complete || got.WaitSeconds != 0 {
				t.Errorf("Complete = %t, WaitSeconds = %d, want false and 0", got.Complete, got.WaitSeconds)
			}
		})
	}
}

// TestToAudit_ciNoRunsIsIncomplete: a commit no check ran on has nothing to
// wait for, which is not the same as a zero wait, so it is recorded but not
// complete.
func TestToAudit_ciNoRunsIsIncomplete(t *testing.T) {
	t.Parallel()

	got := onlyDefaultBranchCommit(t, decodeRepository(t, onDefaultBranch(suites())).toAudit("web-app").CI)
	if diff := cmp.Diff(audit.CICommit{OID: ciOID}, got); diff != "" {
		t.Errorf("default-branch commit mismatch (-want +got):\n%s", diff)
	}
}

// TestToAudit_ciJobLabels: an Actions run is named by its workflow, any other
// app's run by the app, and a run whose app GitHub does not name by its own
// name alone.
func TestToAudit_ciJobLabels(t *testing.T) {
	t.Parallel()

	run := func(name string) string {
		return completedRun(name, "2026-10-01T10:01:00Z", "2026-10-01T10:02:00Z")
	}
	other := func(app, name string) string {
		return `{"created_at":"2026-10-01T10:00:00Z","app":` + app + `,"workflow_run":null,` +
			`"check_runs":{"page_info":{"has_next_page":false},"nodes":[` + run(name) + `]}}`
	}
	body := onDefaultBranch(suites(
		actionsSuite("2026-10-01T10:00:00Z", run("check")),
		other(`{"name":"Codecov"}`, "codecov/patch"),
		other(`null`, "lint"),
	))
	got := onlyDefaultBranchCommit(t, decodeRepository(t, body).toAudit("web-app").CI)

	want := []audit.CIJob{
		{Label: "ci / check", Seconds: 60},
		{Label: "Codecov / codecov/patch", Seconds: 60},
		{Label: "lint", Seconds: 60},
	}
	if diff := cmp.Diff(want, got.Jobs); diff != "" {
		t.Errorf("Jobs mismatch (-want +got):\n%s", diff)
	}
}

// mergedPR renders one merged_pull_requests node whose head commit has one
// finished Actions run.
func mergedPR(number, mergedAt, author string) string {
	return `{"number":` + number + `,"merged_at":"` + mergedAt + `","author":` + author + `,"commits":{"nodes":[{"commit":` +
		`{"oid":"` + ciOID + `","check_suites":` +
		suites(actionsSuite("2026-10-01T10:00:00Z", completedRun("check", "2026-10-01T10:01:00Z", "2026-10-01T10:03:00Z"))) +
		`}}]}}`
}

// TestToAudit_ciBotAuthor: a pull request a GitHub App opened is marked, so the
// report can leave it out of the wait a person sits through; one whose author
// account was deleted is not a bot. The pull request's number and merge time
// ride along, the time normalised to UTC like every instant in audit.json, and
// the order is GitHub's.
func TestToAudit_ciBotAuthor(t *testing.T) {
	t.Parallel()

	body := `{"merged_pull_requests":{"nodes":[` +
		mergedPR("7", "2026-10-02T14:00:00+02:00", `{"type":"Bot"}`) + `,` +
		mergedPR("5", "2026-10-01T12:00:00Z", `{"type":"User"}`) + `,` +
		mergedPR("3", "2026-09-30T12:00:00Z", `null`) +
		`]}}`
	got := decodeRepository(t, body).toAudit("web-app").CI.PullRequests

	jobs := []audit.CIJob{{Label: "ci / check", Seconds: 120}}
	want := []audit.CICommit{
		{OID: ciOID, PRNumber: 7, MergedAt: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), AuthorBot: true, Complete: true, WaitSeconds: 180, Jobs: jobs},
		{OID: ciOID, PRNumber: 5, MergedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Complete: true, WaitSeconds: 180, Jobs: jobs},
		{OID: ciOID, PRNumber: 3, MergedAt: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), Complete: true, WaitSeconds: 180, Jobs: jobs},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("PullRequests mismatch (-want +got):\n%s", diff)
	}
	if got[0].MergedAt.Location() != time.UTC {
		t.Errorf("MergedAt location = %v, want UTC", got[0].MergedAt.Location())
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
