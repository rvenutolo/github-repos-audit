package rules

import (
	"path"
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
// names the first family below cacheFloor, by name, or else the largest
// newest entry, or "none on main". There is no ceiling: the Go toolchain
// trims its own build cache, so it does not grow the way a Nix store does.
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
// It is n/a when no workflow is triggered by a pull request, and none is
// unreadable: such a repository's runs are never superseded this way.
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
// A workflow counts when `on:` names pull_request or pull_request_target.
// It cancels superseded runs when its workflow-level concurrency sets
// cancel-in-progress, or every job's job-level concurrency does. Set means
// present and not the literal "false": an expression, typically one that
// cancels only pull-request runs, is a deliberate choice and counts. A
// workflow with no jobs has no job to cancel through, so only the
// workflow-level setting can pass it.
func cancelMissing(r audit.Repo) (missing []string, judged bool) {
	ws := slices.Clone(r.Workflows)
	slices.SortFunc(ws, func(a, b audit.Workflow) int { return strings.Compare(a.Path, b.Path) })
	for _, w := range ws {
		switch {
		case w.ParseError != "":
			missing = append(missing, "unreadable "+w.Path)
		case !slices.Contains(w.Events, "pull_request") && !slices.Contains(w.Events, "pull_request_target"):
			continue
		case !cancelsSuperseded(w):
			missing = append(missing, w.Path)
		}
		judged = true
	}
	return missing, judged
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

// cancelSet reports whether a concurrency setting sets cancel-in-progress to
// anything but the literal "false".
func cancelSet(c audit.Concurrency) bool {
	return c.CancelInProgress != "" && c.CancelInProgress != "false"
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

// failingLists builds a list the report gives under a table: for each
// repository whose cell for c fails, in report order, the entries items
// names for it. The list is the detail behind the cell's count, so a
// repository that passes, or whose type or override does not judge the
// check, is left off even when items would name something for it.
func failingLists(repos []RepoReport, c Check, items func(audit.Repo) ([]string, bool)) []RepoList {
	var out []RepoList
	for _, r := range repos {
		if r.Cell(c).Verdict != Fail {
			continue
		}
		missing, _ := items(r.Repo)
		out = append(out, RepoList{Repo: r.Repo.Name, Items: missing})
	}
	return out
}
