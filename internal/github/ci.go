package github

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// actionsCompleted is the one run and job status that means it is over.
// Every other status (queued, waiting, in_progress and the rest) means its
// result, and so the commit's wait, is not yet known.
const actionsCompleted = "completed"

// botType is the GraphQL type of a GitHub App's account. A bot's GraphQL
// login does not carry the "[bot]" suffix its REST login and commit identity
// do, so the type is the only reliable mark.
const botType = "Bot"

// prEvents and pushEvents are the run events that count toward a sampled
// commit's wait: the ones the change itself triggered. A pull request's head
// waits on its pull_request runs and on the runs the push to its branch set
// off, since some repositories run CI on push alone; a default-branch commit
// waits on its push runs and, in a merge queue, its merge_group runs. Every
// other event (schedule, workflow_dispatch, repository_dispatch, workflow_run,
// release and the rest) can land on the same commit hours or days later, and
// counting it would stretch the wait to cover time nobody spent waiting, so
// such a run is ignored outright: its jobs are not read and it decides
// neither the wait nor completeness. Functions rather than variables, so no
// caller can change the rule for the rest of the run.
func prEvents() []string   { return []string{"pull_request", "pull_request_target", "push"} }
func pushEvents() []string { return []string{"push", "merge_group"} }

// ciRunsPage and ciJobsPage are the page sizes of the runs list and of one
// run's jobs list, both the API's maximum. The sampled commits are recent, so
// one page of a repository's newest runs usually covers them. A sampled
// commit older than that page finds no run and reads as one no workflow ran
// on, the same answer as a commit nothing ran on; in a repository whose
// scheduled or bot-triggered runs are frequent, that can be several of the
// older sampled commits. Neither list is paged further: a jobs
// list longer than its page marks the commit incomplete instead (see
// timeCommit).
//
// The cost is one runs request per repository plus one jobs request per run
// on a sampled commit: with twenty sampled commits and a workflow or two on
// each, a few dozen GETs per repository, which a whole account's run spends
// comfortably inside the 5,000 requests an hour a token is allowed.
const (
	ciRunsPage = "100"
	ciJobsPage = "100"
)

// actionsRuns is the slice of the runs list this tool reads.
type actionsRuns struct {
	WorkflowRuns []actionsRun `json:"workflow_runs"`
}

// actionsRun is one workflow run. Name is the workflow's name. HeadBranch is
// the branch the run was on, which for a pull request's run is its head
// branch, so only a run on the default branch itself names it. CreatedAt is
// when the run was triggered, and a re-run keeps it, which is what lets a
// re-run extend the wait instead of starting a new one.
type actionsRun struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	HeadSHA    string    `json:"head_sha"`
	HeadBranch string    `json:"head_branch"`
	Event      string    `json:"event"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// actionsJobs is one run's jobs list. TotalCount is how many jobs the run
// has, which is how a list cut short by its page shows itself.
type actionsJobs struct {
	TotalCount int          `json:"total_count"`
	Jobs       []actionsJob `json:"jobs"`
}

// actionsJob is one job of a run's latest attempt. StartedAt and CompletedAt
// are null until the job gets that far.
type actionsJob struct {
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

// ciRun is one run on a sampled commit together with its jobs.
type ciRun struct {
	run  actionsRun
	jobs actionsJobs
}

// ci records which commits the CI timing samples, in the order GitHub listed
// them, with nothing yet timed: that is fetchCI's, over REST. It records and
// selects nothing: bot-authored pull requests are kept, marked, for
// internal/rules to decide about.
func (r *repository) ci() audit.CI {
	var out audit.CI
	for _, pr := range r.MergedPullRequests.Nodes {
		// commits(last: 1) is the head commit. A merged pull request always has
		// one, but an empty list is an answer to record nothing from, not an
		// entry with no oid.
		if len(pr.Commits.Nodes) == 0 {
			continue
		}
		c := audit.CICommit{OID: pr.Commits.Nodes[0].Commit.OID, PRNumber: pr.Number}
		if pr.MergedAt != nil {
			c.MergedAt = pr.MergedAt.UTC()
		}
		// A null author is a deleted account, which no app owns.
		c.AuthorBot = pr.Author != nil && pr.Author.Type == botType
		out.PullRequests = append(out.PullRequests, c)
	}
	if r.DefaultBranchRef != nil && r.DefaultBranchRef.Target != nil && r.DefaultBranchRef.Target.RecentCommits != nil {
		for _, c := range r.DefaultBranchRef.Target.RecentCommits.Nodes {
			out.DefaultBranch = append(out.DefaultBranch, audit.CICommit{OID: c.OID})
		}
	}
	return out
}

// fetchCI times every sampled commit in ci from the Actions REST API. The
// Actions API rather than GraphQL's check suites, because a fine-grained token
// has no permission that reads check suites, while Actions (read) covers
// this. The trade-off is that only GitHub Actions is timed: a check another
// app posts, such as Codecov's, is not a workflow run and does not count.
//
// Runs are matched to commits by head_sha and then by event: a commit counts
// only the runs its own change triggered (see prEvents and pushEvents), so a
// commit whose only runs are of other events reads as one no workflow ran on.
// The events differ by sample, so a commit sampled both as a pull request's
// head and on the default branch is judged by each list's own events.
// Actions disabled on a repository is a 200 with no runs, which leaves every
// commit untimed rather than failing.
//
// The same runs page also gives LastDefaultBranchRun: the newest creation
// among its runs on defaultBranch, of any event, since a scheduled or
// dispatched run keeps the branch's caches as warm as a push does. It costs
// no request of its own. A repository with no sampled commits is an empty
// one, with no branch any workflow could have run on, so it reads nothing
// and its recency stays zero.
// base is the repository's /repos/{owner}/{name} path, and defaultBranch is
// its live default branch name.
func (c *Client) fetchCI(ctx context.Context, base, defaultBranch string, ci *audit.CI) error {
	type sampledCommit struct {
		commit *audit.CICommit
		events []string
	}
	commits := make([]sampledCommit, 0, len(ci.PullRequests)+len(ci.DefaultBranch))
	for i := range ci.PullRequests {
		commits = append(commits, sampledCommit{&ci.PullRequests[i], prEvents()})
	}
	for i := range ci.DefaultBranch {
		commits = append(commits, sampledCommit{&ci.DefaultBranch[i], pushEvents()})
	}
	if len(commits) == 0 {
		return nil
	}
	// counts reports whether a run on sha with event counts toward any
	// sampled entry for that commit, which is when its jobs are worth a read.
	counts := func(sha, event string) bool {
		for _, s := range commits {
			if s.commit.OID == sha && slices.Contains(s.events, event) {
				return true
			}
		}
		return false
	}

	var list actionsRuns
	if _, err := c.getJSON(ctx, base+"/actions/runs?per_page="+ciRunsPage, &list); err != nil {
		return err
	}
	// The runs list holds each run once, so a run is read once however many
	// sampled entries share its commit.
	bySHA := map[string][]ciRun{}
	for _, run := range list.WorkflowRuns {
		if run.HeadBranch == defaultBranch && run.CreatedAt.After(ci.LastDefaultBranchRun) {
			ci.LastDefaultBranchRun = run.CreatedAt.UTC()
		}
		if !counts(run.HeadSHA, run.Event) {
			continue
		}
		var jobs actionsJobs
		path := base + "/actions/runs/" + strconv.FormatInt(run.ID, 10) + "/jobs?per_page=" + ciJobsPage
		if _, err := c.getJSON(ctx, path, &jobs); err != nil {
			return err
		}
		bySHA[run.HeadSHA] = append(bySHA[run.HeadSHA], ciRun{run: run, jobs: jobs})
	}
	for _, s := range commits {
		var runs []ciRun
		for _, r := range bySHA[s.commit.OID] {
			if slices.Contains(s.events, r.run.Event) {
				runs = append(runs, r)
			}
		}
		*s.commit = timeCommit(*s.commit, runs)
	}
	return nil
}

// timeCommit reduces one commit's workflow runs to its wait and job
// durations, keeping everything ci recorded about the commit.
//
// The wait runs from the earliest run's creation to the latest job's
// completion. That includes the time the first workflow sat queued for a
// runner, and counts parallel workflows once rather than summing them, which
// is the wait a push actually feels. It is recorded only for a complete
// commit, because for any other it would be a lower bound read as the answer.
// A commit is complete when it has at least one run, every run and every job
// is completed with its completion time known, no jobs list was cut short,
// and at least one job finished to end the wait at: a run that ended without
// starting a job leaves nothing to measure to.
func timeCommit(c audit.CICommit, runs []ciRun) audit.CICommit {
	complete := len(runs) > 0
	var earliest, latest time.Time
	for _, r := range runs {
		if r.run.Status != actionsCompleted || r.jobs.TotalCount > len(r.jobs.Jobs) {
			complete = false
		}
		if earliest.IsZero() || r.run.CreatedAt.Before(earliest) {
			earliest = r.run.CreatedAt
		}
		for _, job := range r.jobs.Jobs {
			// A job GitHub calls completed but gives no completion time cannot
			// be placed on the timeline, so the latest completion is as unknown
			// as it is for a job still going.
			if job.Status != actionsCompleted || job.CompletedAt == nil {
				complete = false
				continue
			}
			if job.CompletedAt.After(latest) {
				latest = *job.CompletedAt
			}
			if job.StartedAt != nil {
				c.Jobs = append(c.Jobs, audit.CIJob{
					Label:   r.run.Name + " / " + job.Name,
					Seconds: seconds(job.CompletedAt.Sub(*job.StartedAt)),
				})
			}
		}
	}
	c.Complete = complete && !latest.IsZero()
	if c.Complete {
		c.WaitSeconds = seconds(latest.Sub(earliest))
	}
	return c
}

// seconds is a duration in whole seconds, which is all GitHub's job
// timestamps carry, and never less than zero. A job GitHub skips (one whose
// if: condition is false) can carry a completion a second before its start,
// and a negative duration is not a fact about the job: it took no time.
func seconds(d time.Duration) int64 {
	return max(0, int64(d/time.Second))
}
