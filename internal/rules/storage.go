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

// cacheFloor is the least the newest entry of a cache family must hold, for
// Go and Nix caches alike. Below it the entry holds nothing worth restoring:
// a save that ran before anything was built, a side job that built nothing
// but saved the shared key first, or a store garbage-collected to nothing.
// Restoring such an entry costs a run its cache while looking like a hit.
const cacheFloor = 1_000_000

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

// actionOwnedPrefixes are the key prefixes common actions save their own
// state under: harden-runner's egress policy cache, the Determinate Nix
// installer's download, and the gitleaks and Trivy databases. They are
// neither the tool's Go and Nix caches nor something a workflow author named,
// so they are listed apart from the unrecognised caches: their size is set by
// the action, and the only lever over it is whether the action runs at all.
// A function rather than a variable, so nothing can append to the list.
func actionOwnedPrefixes() []string {
	return []string{"harden-runner-cacheKey", "determinatesystem-nix-installer-", "gitleaks-", "trivy-"}
}

// toolCache reports whether a key is under one of the prefixes the go_cache
// and nix_cache checks judge.
func toolCache(key string) bool {
	return strings.HasPrefix(key, "go-") || strings.HasPrefix(key, "nix-")
}

// actionOwned reports whether a key is under one of actionOwnedPrefixes.
func actionOwned(key string) bool {
	return slices.ContainsFunc(actionOwnedPrefixes(), func(p string) bool { return strings.HasPrefix(key, p) })
}

// cacheLists builds the two lists of caches the cache checks do not judge,
// for each repository in report order that holds any, from every ref:
// unrecognised caches, under neither the tool's prefixes nor an action's,
// and action-owned caches, under actionOwnedPrefixes. An unrecognised cache
// is one a workflow saves under a key the "<prefix>-<runner.os>-…"
// convention does not cover, so nothing checks that it holds anything or
// stays bounded, and it shares the repository's cache storage with the
// caches that are checked. Both lists are shown and never judged: whatever
// any check concluded about the repository, its caches are listed.
//
// Each item is "<key> (<size>)", sorted by key and then by ref, so a key
// saved from two refs is listed twice, once per entry. The renderer reads
// the size back from after the last " (", which a size never contains, so a
// key holding " (" itself is still split in the right place.
func cacheLists(repos []RepoReport) (unrecognised, owned []RepoList) {
	for _, r := range repos {
		caches := slices.Clone(r.Repo.Storage.Caches)
		slices.SortFunc(caches, func(a, b audit.Cache) int {
			if c := strings.Compare(a.Key, b.Key); c != 0 {
				return c
			}
			return strings.Compare(a.Ref, b.Ref)
		})
		var u, o []string
		for _, c := range caches {
			item := c.Key + " (" + formatBytes(c.SizeBytes) + ")"
			switch {
			case toolCache(c.Key):
			case actionOwned(c.Key):
				o = append(o, item)
			default:
				u = append(u, item)
			}
		}
		if u != nil {
			unrecognised = append(unrecognised, RepoList{Repo: r.Repo.Name, Items: u})
		}
		if o != nil {
			owned = append(owned, RepoList{Repo: r.Repo.Name, Items: o})
		}
	}
	return unrecognised, owned
}

// prCaches is, for each repository in report order with any, how many Go and
// Nix caches pull-request refs saved and their total size, as one item
// "<n> caches · <size>". A pull request's caches are scoped to its own ref:
// they never serve the default branch, which is why cacheFamilies leaves them
// out, yet they count against the same storage limit until GitHub evicts
// them, so a workflow that saves on every pull request crowds out the caches
// the default branch restores. Only the tool's prefixes are counted, since
// those are the caches a workflow chose to save; an action's own state saved
// from a pull request is the action's doing. Shown and never judged.
func prCaches(repos []RepoReport) []RepoList {
	var out []RepoList
	for _, r := range repos {
		var n int
		var size int64
		for _, c := range r.Repo.Storage.Caches {
			if toolCache(c.Key) && strings.HasPrefix(c.Ref, "refs/pull/") {
				n++
				size += c.SizeBytes
			}
		}
		if n > 0 {
			out = append(out, RepoList{Repo: r.Repo.Name, Items: []string{countOf(n, "cache") + " · " + formatBytes(size)}})
		}
	}
	return out
}
