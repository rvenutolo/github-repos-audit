package rules

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// observeGoCache judges whether the default branch's Go build and module
// caches are saved with something in them. Each runner OS restores its own
// family of "go-" entries (P4 in cacheFamilies), and only the newest entry of
// each is restored, so that is the one judged: a side job that builds nothing
// but saves the shared key last leaves an empty newest entry behind a full
// older one, and judging any entry would pass exactly that failure. The cell
// shows the size of the first family below cacheFloor, or else of the
// largest newest entry, or "none on main". There is no ceiling, unlike the
// Nix cache's: the standard sets none for Go caches, and neither half grows
// without bound the way a Nix store does. The build cache trims itself,
// dropping entries the toolchain has not used for days, and the module
// cache is bounded by the modules go.sum names.
//
// It is n/a without a root go.mod, which builds no Go module to cache, and
// when the default branch has not run CI within the activity window, since
// GitHub has then evicted the caches whatever the workflows do.
func observeGoCache(r audit.Repo, now time.Time) observation {
	if !r.Files.GoMod || !recentDefaultRun(r, now) {
		return observation{unknown: true}
	}
	families := cacheFamilies(r, "go-")
	if len(families) == 0 {
		return observation{value: "none on main", mark: true}
	}
	var largest int64
	for _, f := range families {
		size := f.Newest.SizeBytes
		if size < cacheFloor {
			return observation{value: "empty (" + formatBytes(size) + ")", mark: true}
		}
		largest = max(largest, size)
	}
	return observation{ok: true, value: formatBytes(largest), mark: true}
}

// observeCacheUsage judges whether the repository's Actions caches leave
// headroom under the storage limit GitHub reported for it. Past the limit
// GitHub evicts the entries restored longest ago to make room for each new
// save, so caches start pushing each other out and runs miss caches they
// expected to hit; 80% is far enough below it that one more save does not
// trigger that. The comparison is in integer bytes, usage*10 against
// limit*8, so exactly 80% passes and no rounding moves the boundary. The
// usage is a scalar, worth showing whether or not the check is required.
//
// It is n/a when no limit was read: storage is not collected for an empty
// repository, and a zero limit would fail every repository that holds a
// single cache.
func observeCacheUsage(r audit.Repo) observation {
	usage, limit := r.Storage.CacheUsageBytes, r.Storage.CacheLimitBytes
	if limit == 0 {
		return observation{unknown: true}
	}
	return observation{ok: usage*10 <= limit*8, value: formatBytes(usage), mark: true, scalar: true}
}

// observeArtifactStorage shows what the repository's unexpired artifacts
// hold: how many, and their total size. It is a plain fact and never judged:
// whether a build's outputs are worth keeping until they expire is the
// workflow's choice, and no size is wrong in itself, but artifacts share the
// account's Actions storage with every other repository, so the figure is
// worth seeing beside the caches. With no artifacts there is no size to
// show, so the cell is the bare count, which also covers a repository whose
// storage was not read.
func observeArtifactStorage(r audit.Repo) observation {
	count := r.Storage.ArtifactCount
	if count == 0 {
		return observation{value: "0", scalar: true}
	}
	return observation{value: strconv.Itoa(count) + " · " + formatBytes(r.Storage.ArtifactBytes), scalar: true}
}

// observeCancelSuperseded judges whether every pull-request workflow cancels
// the run a newer push to the same pull request supersedes. Without it each
// push to an open pull request queues another full run beside the one still
// going, whose result nobody will read, and both hold runners the next pull
// request waits for. The cell counts the workflows cancelMissing names; a
// pass carries no count, since there is nothing missing to count.
//
// It is n/a when no workflow can run on a pull request's synchronize, and
// none is unreadable: such a repository's runs are never superseded this way.
func observeCancelSuperseded(r audit.Repo) observation {
	missing, judged := cancelMissing(r)
	switch {
	case !judged:
		return observation{unknown: true}
	case len(missing) == 0:
		return observation{ok: true}
	}
	return observation{value: countOf(len(missing), "workflow"), mark: true}
}

// cancelMissing names, in path order, every pull-request workflow that does
// not cancel superseded runs, and every workflow it could not read as
// "unreadable <path>": an unreadable file may be a pull-request workflow
// without the setting, so it fails the check rather than passing unseen. It
// also reports whether anything was judged at all, which is what makes the
// check apply.
//
// A workflow counts when runsOnSynchronize: `on:` names pull_request or
// pull_request_target and that trigger can fire on a push to an open pull
// request into the default branch. A workflow whose pull-request triggers
// list only other types (closed, labeled, ...), or whose branch filters
// exclude the default branch, is never superseded that way on the pull
// requests the repository merges, so it is not judged.
// It cancels superseded runs when its workflow-level concurrency sets
// cancel-in-progress, or every job's job-level concurrency does. Set means
// present and not false, whether the literal or an expression that is only the
// word false: any other expression, typically one that cancels only
// pull-request runs, is a deliberate choice and counts. A
// workflow with no jobs has no job to cancel through, so only the
// workflow-level setting can pass it.
func cancelMissing(r audit.Repo) (missing []string, judged bool) {
	ws := slices.Clone(r.Workflows)
	slices.SortFunc(ws, func(a, b audit.Workflow) int { return strings.Compare(a.Path, b.Path) })
	for _, w := range ws {
		switch {
		case w.ParseError != "":
			missing = append(missing, "unreadable "+w.Path)
		case !runsOnSynchronize(w, r.DefaultBranch):
			continue
		case !cancelsSuperseded(w):
			missing = append(missing, w.Path)
		}
		judged = true
	}
	return missing, judged
}

// runsOnSynchronize reports whether a workflow has a pull_request or
// pull_request_target trigger that can run when a push updates an open pull
// request whose base is base. A trigger that lists no types (recorded as
// nil) gets GitHub's default set, opened, synchronize and reopened, which
// includes it. GitHub matches the trigger's branch filters against the base
// branch, so they must admit base, as branchAdmits reads them; its path
// filters are ignored, since they narrow when it runs, not for which base.
func runsOnSynchronize(w audit.Workflow, base string) bool {
	return (slices.Contains(w.Events, "pull_request") && synchronizes(w.PullRequestTypes) &&
		branchAdmits(w.PullRequestBranches, w.PullRequestBranchesIgnore, base)) ||
		(slices.Contains(w.Events, "pull_request_target") && synchronizes(w.PullRequestTargetTypes) &&
			branchAdmits(w.PullRequestTargetBranches, w.PullRequestTargetBranchesIgnore, base))
}

// synchronizes reports whether a trigger's recorded types include
// synchronize, with no recorded types meaning the default set.
func synchronizes(types []string) bool {
	return types == nil || slices.Contains(types, "synchronize")
}

// cancelsSuperseded reports whether a parsed workflow cancels superseded
// runs, at workflow level or in every one of its jobs; see cancelMissing.
func cancelsSuperseded(w audit.Workflow) bool {
	if cancelSet(w.Concurrency) {
		return true
	}
	if len(w.Jobs) == 0 {
		return false
	}
	for _, j := range w.Jobs {
		if !cancelSet(j.Concurrency) {
			return false
		}
	}
	return true
}

// falseExpression matches an expression that is only the word false: GitHub
// evaluates `${{ false }}` to the same boolean as the literal, with the word in
// any case and any (or no) space inside the braces. Anything else inside the
// braces is a real expression whose value is not known offline.
var falseExpression = regexp.MustCompile(`(?i)^\$\{\{\s*false\s*\}\}$`)

// cancelSet reports whether a concurrency setting sets cancel-in-progress to
// anything but false: the literal in any case YAML reads as that boolean, or
// an expression that is only the word false (see falseExpression).
func cancelSet(c audit.Concurrency) bool {
	v := strings.TrimSpace(c.CancelInProgress)
	return v != "" && !strings.EqualFold(v, "false") && !falseExpression.MatchString(v)
}

// observeJobTimeouts judges whether every job bounds its own run time with
// timeout-minutes. Without it a job that hangs, on a stuck network call or a
// test waiting on itself, holds its runner for GitHub's six-hour default
// before anything stops it, and every run queued behind it waits that long.
// The cell counts the jobs timeoutMissing names; a pass carries no count,
// since there is nothing missing to count.
//
// It is n/a when no workflow holds a job of its own and none is unreadable:
// there is then no run time to bound.
func observeJobTimeouts(r audit.Repo) observation {
	missing, judged := timeoutMissing(r)
	switch {
	case !judged:
		return observation{unknown: true}
	case len(missing) == 0:
		return observation{ok: true}
	}
	return observation{value: countOf(len(missing), "job"), mark: true}
}

// timeoutMissing names every job without timeout-minutes as "<workflow file
// name> / <job id>", and every workflow it could not read as "unreadable
// <path>": an unreadable file may hold such jobs, so it fails the check
// rather than passing unseen. Workflows go in path order and jobs in file
// order within each, so the list reads in the order a reader opening the
// files meets them. The file name stands for the workflow because every
// workflow lives in the one .github/workflows directory, so the directory
// adds nothing but width. It also reports whether anything was judged at
// all, which is what makes the check apply.
//
// A job that calls a reusable workflow (a job-level `uses:`) is left out:
// GitHub does not accept timeout-minutes on such a job, and its steps run in
// the called workflow, whose own jobs carry the bound. Any value counts as
// set, an expression included: an expression, such as one giving a slower
// runner longer, is a deliberate bound, and it cannot be evaluated without
// the run anyway.
func timeoutMissing(r audit.Repo) (missing []string, judged bool) {
	ws := slices.Clone(r.Workflows)
	slices.SortFunc(ws, func(a, b audit.Workflow) int { return strings.Compare(a.Path, b.Path) })
	for _, w := range ws {
		if w.ParseError != "" {
			missing = append(missing, "unreadable "+w.Path)
			judged = true
			continue
		}
		for _, j := range w.Jobs {
			if j.Reusable {
				continue
			}
			judged = true
			if j.TimeoutMinutes == "" {
				missing = append(missing, path.Base(w.Path)+" / "+j.ID)
			}
		}
	}
	return missing, judged
}

// countOf is a count of n things, the noun made plural unless n is 1: "1
// job", "3 jobs".
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// detailLists builds a list the report gives under a table: for each
// repository in report order whose cell for c shows a count, the entries
// items names for it. The list is the detail behind that count, so it holds
// a repository whose cell fails, and one whose type shows the check as info
// when items names something: an info cell still shows the count, and a
// count with nothing behind it would leave the reader to open every file.
// A repository that passes, or that is n/a, whether the check does not
// apply or a not_required type or override excuses it, is left off even
// when items would name something for it.
func detailLists(repos []RepoReport, c Check, items func(audit.Repo) ([]string, bool)) []RepoList[string] {
	var out []RepoList[string]
	for _, r := range repos {
		verdict := r.Cell(c).Verdict
		if verdict != Fail && verdict != Info {
			continue
		}
		missing, _ := items(r.Repo)
		if verdict == Info && len(missing) == 0 {
			continue
		}
		out = append(out, RepoList[string]{Repo: r.Repo.Name, Items: missing})
	}
	return out
}
