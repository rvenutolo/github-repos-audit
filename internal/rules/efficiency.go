package rules

import (
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
