package rules

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// activityWindow is how long before the snapshot the default branch's
// newest CI run may be for its caches to be judged. GitHub evicts a cache
// nothing has restored for seven days, so a branch idle for longer has lost
// its caches through no fault of its configuration, and judging them would
// report the idleness instead.
const activityWindow = 7 * 24 * time.Hour

// recentDefaultRun reports whether a workflow ran on the default branch
// within activityWindow of now, the snapshot's instant. A repository with no
// default-branch run recorded has none within it.
func recentDefaultRun(r audit.Repo, now time.Time) bool {
	last := r.CI.LastDefaultBranchRun
	return !last.IsZero() && now.Sub(last) <= activityWindow
}

// cacheFamily is the caches of one kind for one runner OS, such as every
// "nix-Linux-…" entry: the entries a run on that OS chooses among.
type cacheFamily struct {
	// Name is the family, the key's first two "-"-separated segments.
	Name string
	// Newest is the most recently created entry, the one the next run
	// restores; on a tie, the first in key order.
	Newest audit.Cache
	// Count is how many entries the family holds.
	Count int
}

// cacheFamilies groups the default branch's caches whose key starts with
// prefix into families, sorted by name. Keys follow the
// "<prefix>-<runner.os>-<hash>" convention, so the first two segments name
// the cache kind and the OS, and everything after is what changes from one
// save to the next. Only caches saved from the default branch count: a pull
// request's caches never serve the default branch, so they say nothing about
// whether it is cached.
func cacheFamilies(r audit.Repo, prefix string) []cacheFamily {
	ref := "refs/heads/" + r.DefaultBranch
	var out []cacheFamily
	for _, c := range r.Storage.Caches {
		if c.Ref != ref || !strings.HasPrefix(c.Key, prefix) {
			continue
		}
		name := familyName(c.Key)
		i := slices.IndexFunc(out, func(f cacheFamily) bool { return f.Name == name })
		if i < 0 {
			out = append(out, cacheFamily{Name: name, Newest: c, Count: 1})
			continue
		}
		out[i].Count++
		if c.CreatedAt.After(out[i].Newest.CreatedAt) {
			out[i].Newest = c
		}
	}
	slices.SortFunc(out, func(a, b cacheFamily) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// familyName is a cache key's first two "-"-separated segments, or the whole
// key when it has fewer.
func familyName(key string) string {
	parts := strings.SplitN(key, "-", 3)
	return strings.Join(parts[:min(len(parts), 2)], "-")
}

// formatBytes renders a size in decimal units, as GitHub's own storage
// settings count them: whole B, KB and MB, and GB to one place. Each unit is
// truncated rather than rounded, so a size never reads as the next unit up
// ("1000 KB") and never as more than it is.
func formatBytes(b int64) string {
	switch {
	case b < 1_000:
		return strconv.FormatInt(b, 10) + " B"
	case b < 1_000_000:
		return strconv.FormatInt(b/1_000, 10) + " KB"
	case b < 1_000_000_000:
		return strconv.FormatInt(b/1_000_000, 10) + " MB"
	default:
		tenths := b / 100_000_000
		return strconv.FormatInt(tenths/10, 10) + "." + strconv.FormatInt(tenths%10, 10) + " GB"
	}
}
