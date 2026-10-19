package github

import (
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// checkCompleted is the one check-run status that means the run is over.
// Every other status (queued, waiting, in progress and the rest) means its
// result, and so the commit's wait, is not yet known.
const checkCompleted = "COMPLETED"

// botType is the GraphQL type of a GitHub App's account. A bot's GraphQL
// login does not carry the "[bot]" suffix its REST login and commit identity
// do, so the type is the only reliable mark.
const botType = "Bot"

// ci records the CI timing of every commit repoQuery read, in the order
// GitHub listed them. It records and selects nothing: bot-authored pull
// requests, commits no check ran on and commits still running are all kept,
// marked, for internal/rules to decide about.
func (r *repository) ci() audit.CI {
	var out audit.CI
	for _, pr := range r.MergedPullRequests.Nodes {
		// commits(last: 1) is the head commit. A merged pull request always has
		// one, but an empty list is an answer to record nothing from, not an
		// entry with no oid.
		if len(pr.Commits.Nodes) == 0 {
			continue
		}
		c := pr.Commits.Nodes[0].Commit.toAudit()
		c.PRNumber = pr.Number
		if pr.MergedAt != nil {
			c.MergedAt = pr.MergedAt.UTC()
		}
		// A null author is a deleted account, which no app owns.
		c.AuthorBot = pr.Author != nil && pr.Author.Type == botType
		out.PullRequests = append(out.PullRequests, c)
	}
	if r.DefaultBranchRef != nil && r.DefaultBranchRef.Target != nil && r.DefaultBranchRef.Target.RecentCommits != nil {
		for _, c := range r.DefaultBranchRef.Target.RecentCommits.Nodes {
			out.DefaultBranch = append(out.DefaultBranch, c.toAudit())
		}
	}
	return out
}

// toAudit reduces one commit's check suites to its wait and job durations.
//
// Only suites with at least one run count. An app that subscribes to check
// suites but never posts a run leaves its suite queued indefinitely and
// from the push; counting it would hold every commit open forever and
// stretch its wait back to whenever that suite happened to be created.
//
// The wait runs from the earliest counted suite's creation to the latest
// counted run's completion. That includes the time the first workflow sat
// queued for a runner, and counts parallel workflows once rather than summing
// them, which is the wait a push actually feels. It is recorded only for a
// complete commit, because for any other it would be a lower bound read as
// the answer.
func (c ciCommit) toAudit() audit.CICommit {
	out := audit.CICommit{OID: c.OID}
	complete := !c.CheckSuites.PageInfo.HasNextPage
	var counted int
	var earliest, latest time.Time
	for _, suite := range c.CheckSuites.Nodes {
		if len(suite.CheckRuns.Nodes) == 0 {
			continue
		}
		if suite.CheckRuns.PageInfo.HasNextPage {
			complete = false
		}
		if earliest.IsZero() || suite.CreatedAt.Before(earliest) {
			earliest = suite.CreatedAt
		}
		owner := suite.owner()
		for _, run := range suite.CheckRuns.Nodes {
			counted++
			// A run GitHub calls completed but gives no completion time cannot
			// be placed on the timeline, so the latest completion is as unknown
			// as it is for a run still going.
			if run.Status != checkCompleted || run.CompletedAt == nil {
				complete = false
				continue
			}
			if run.CompletedAt.After(latest) {
				latest = *run.CompletedAt
			}
			if run.StartedAt != nil {
				out.Jobs = append(out.Jobs, audit.CIJob{
					Label:   label(owner, run.Name),
					Seconds: seconds(run.CompletedAt.Sub(*run.StartedAt)),
				})
			}
		}
	}
	out.Complete = complete && counted > 0
	if out.Complete {
		out.WaitSeconds = seconds(latest.Sub(earliest))
	}
	return out
}

// owner is what a suite's runs are labelled by: the workflow for a GitHub
// Actions run, since one app runs every workflow and its name would say
// nothing, and the app for anything else. Empty when GitHub names neither.
func (s checkSuite) owner() string {
	if s.WorkflowRun != nil && s.WorkflowRun.Workflow != nil {
		return s.WorkflowRun.Workflow.Name
	}
	if s.App != nil {
		return s.App.Name
	}
	return ""
}

// label is "<owner> / <run>", or the run's name alone when there is no owner
// to qualify it.
func label(owner, run string) string {
	if owner == "" {
		return run
	}
	return owner + " / " + run
}

// seconds is a duration in whole seconds, which is all GitHub's check
// timestamps carry, and never less than zero. A run GitHub skips (a job whose
// if: condition is false) can carry a completion a second before its start,
// and a negative duration is not a fact about the run: it took no time.
func seconds(d time.Duration) int64 {
	return max(0, int64(d/time.Second))
}
