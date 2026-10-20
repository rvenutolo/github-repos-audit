package rules

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// ciSampleSize is how many complete commits each mean is taken over. Five
// smooths one unusually slow or fast run without reaching so far back that
// the figure describes workflows that have since changed.
const ciSampleSize = 5

// ciTimingLines builds the CI timing listing: one line per repository with at
// least one sampled commit, the slowest pull request mean first.
//
// The pull request sample is the latest ciSampleSize merged pull requests
// whose head commit finished its workflow runs and was not opened by a bot. A
// bot's pull request (a dependency bump) runs a different, usually narrower
// set of jobs than the work a person waits on, so counting it would make a
// repository look faster than it feels. The main sample is the newest
// ciSampleSize default-branch commits that finished. A commit whose jobs are
// still running or were cut short has no honest duration, so it is skipped
// rather than counted short.
//
// Pull requests are ordered here by MergedAt, because the collector records
// them in the order GitHub listed them (last updated), which is not merge
// order.
//
// The slowest job is chosen from the jobs of the sampled commits only, so it
// describes the same runs the means do. A job that ran for zero seconds was
// skipped (the collector floors a skipped job's negative duration at zero),
// and averaging it in would make a job that is sometimes skipped read faster
// than it runs; such jobs are left out of that job's mean.
func ciTimingLines(repos []RepoReport) []CITimingLine {
	var out []CITimingLine
	for _, r := range repos {
		prs := make([]audit.CICommit, 0, len(r.Repo.CI.PullRequests))
		for _, c := range r.Repo.CI.PullRequests {
			if c.Complete && !c.AuthorBot {
				prs = append(prs, c)
			}
		}
		slices.SortStableFunc(prs, func(a, b audit.CICommit) int { return b.MergedAt.Compare(a.MergedAt) })
		prs = prs[:min(len(prs), ciSampleSize)]

		var main []audit.CICommit
		for _, c := range r.Repo.CI.DefaultBranch {
			if c.Complete {
				main = append(main, c)
			}
		}
		main = main[:min(len(main), ciSampleSize)]

		if len(prs) == 0 && len(main) == 0 {
			continue
		}
		line := CITimingLine{Repo: r.Repo.Name, HasPR: len(prs) > 0, HasMain: len(main) > 0}
		line.PR = meanWait(prs)
		line.Main = meanWait(main)
		line.SlowestJob, line.SlowestMean = slowestJob(prs, main)
		out = append(out, line)
	}
	slices.SortFunc(out, func(a, b CITimingLine) int {
		if a.HasPR != b.HasPR {
			if a.HasPR {
				return -1
			}
			return 1
		}
		// Longest first; a repository with no pull request sample is ordered
		// by its main mean instead, after every one that has one.
		x, y := a.Main, b.Main
		if a.HasPR {
			x, y = a.PR, b.PR
		}
		if c := cmp.Compare(y, x); c != 0 {
			return c
		}
		return compareNames(a.Repo, b.Repo)
	})
	return out
}

// meanSeconds is the mean of total over n samples as a whole-second duration,
// rounded half up; zero when n is zero.
func meanSeconds(total int64, n int) time.Duration {
	if n == 0 {
		return 0
	}
	return time.Duration((total+int64(n)/2)/int64(n)) * time.Second
}

func meanWait(commits []audit.CICommit) time.Duration {
	var total int64
	for _, c := range commits {
		total += c.WaitSeconds
	}
	return meanSeconds(total, len(commits))
}

// slowestJob returns the label with the highest mean duration across the
// given commits, ties broken by label ascending, or "" when no job ran.
func slowestJob(samples ...[]audit.CICommit) (string, time.Duration) {
	type acc struct {
		total int64
		n     int
	}
	byLabel := map[string]*acc{}
	for _, commits := range samples {
		for _, c := range commits {
			for _, j := range c.Jobs {
				if j.Seconds == 0 {
					continue
				}
				a := byLabel[j.Label]
				if a == nil {
					a = &acc{}
					byLabel[j.Label] = a
				}
				a.total += j.Seconds
				a.n++
			}
		}
	}
	var best string
	var bestMean time.Duration
	for label, a := range byLabel {
		m := meanSeconds(a.total, a.n)
		if m > bestMean || (m == bestMean && strings.Compare(label, best) < 0) {
			best, bestMean = label, m
		}
	}
	return best, bestMean
}
