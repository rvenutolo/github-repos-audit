package rules_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// cacheListsReport evaluates the given repositories as of cacheNow.
func cacheListsReport(t *testing.T, repos ...audit.Repo) *rules.Report {
	t.Helper()
	rep, err := rules.Evaluate(&audit.Snapshot{
		GeneratedAt: cacheNow, Types: standardTypes(), Identity: fixtureIdentity(),
		Repos: repos,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	return rep
}

// prCache is a cache entry saved from pull request n's merge ref.
func prCache(key string, size int64, n string) audit.Cache {
	return audit.Cache{Key: key, Ref: "refs/pull/" + n + "/merge", SizeBytes: size, CreatedAt: cacheNow}
}

// TestCacheLists_classifiesByPrefix: a key under go- or nix- belongs to the
// tool's own cache checks and is on neither list; a key under one of the
// prefixes the common actions save their own state under is action-owned;
// anything else is unrecognised. Each list is in report order, its items
// sorted by key and then ref whatever order they were collected in, each
// with its size, and the lists are shown whatever any check concluded:
// bravo's type judges no cache check and is listed all the same.
func TestCacheLists_classifiesByPrefix(t *testing.T) {
	t.Parallel()

	alpha := fixture("alpha", "tools")
	alpha.Storage.Caches = []audit.Cache{
		mainCache("zeta-cache", 2_000_000, cacheNow),
		mainCache("go-Linux-abc", 95_000_000, cacheNow),
		mainCache("trivy-db-01", 61_000_000, cacheNow),
		prCache("custom-01", 900, "7"),
		mainCache("custom-01", 1_500, cacheNow),
		mainCache("harden-runner-cacheKey-01", 12, cacheNow),
		mainCache("nix-Linux-abc", 206_000_000, cacheNow),
		mainCache("determinatesystem-nix-installer-01", 3_000, cacheNow),
		mainCache("gitleaks-01", 40_000, cacheNow),
		// Starting "nix" without the dash is not the tool's prefix.
		mainCache("nixpkgs-01", 5, cacheNow),
	}
	bravo := fixture("bravo", "content")
	bravo.Storage.Caches = []audit.Cache{mainCache("node-cache", 1_000_000_000, cacheNow)}
	charlie := fixture("charlie", "tools")
	charlie.Storage.Caches = []audit.Cache{mainCache("go-Linux-abc", 95_000_000, cacheNow)}

	rep := cacheListsReport(t, charlie, bravo, alpha)

	wantUnrecognised := []rules.RepoList[rules.CacheItem]{
		{Repo: "alpha", Items: []rules.CacheItem{
			{Key: "custom-01", Size: "1 KB", Ref: "main"},
			{Key: "custom-01", Size: "900 B", Ref: "PR 7"},
			{Key: "nixpkgs-01", Size: "5 B", Ref: "main"},
			{Key: "zeta-cache", Size: "2 MB", Ref: "main"},
		}},
		{Repo: "bravo", Items: []rules.CacheItem{{Key: "node-cache", Size: "1.0 GB", Ref: "main"}}},
	}
	if diff := cmp.Diff(wantUnrecognised, rep.UnrecognisedCaches); diff != "" {
		t.Errorf("UnrecognisedCaches differs (-want +got):\n%s", diff)
	}
	wantActionOwned := []rules.RepoList[rules.CacheItem]{{Repo: "alpha", Items: []rules.CacheItem{
		{Key: "determinatesystem-nix-installer-01", Size: "3 KB", Ref: "main"},
		{Key: "gitleaks-01", Size: "40 KB", Ref: "main"},
		{Key: "harden-runner-cacheKey-01", Size: "12 B", Ref: "main"},
		{Key: "trivy-db-01", Size: "61 MB", Ref: "main"},
	}}}
	if diff := cmp.Diff(wantActionOwned, rep.ActionOwnedCaches); diff != "" {
		t.Errorf("ActionOwnedCaches differs (-want +got):\n%s", diff)
	}
}

// TestCacheLists_trivyActionKeys: trivy-action saves its databases under
// keys starting "cache-trivy-", which are action-owned like the shorter
// "trivy-" form, not unrecognised.
func TestCacheLists_trivyActionKeys(t *testing.T) {
	t.Parallel()

	alpha := fixture("alpha", "tools")
	alpha.Storage.Caches = []audit.Cache{
		mainCache("cache-trivy-01", 61_000_000, cacheNow),
		mainCache("cache-other-01", 1_000, cacheNow),
	}

	rep := cacheListsReport(t, alpha)
	wantOwned := []rules.RepoList[rules.CacheItem]{{Repo: "alpha", Items: []rules.CacheItem{{Key: "cache-trivy-01", Size: "61 MB", Ref: "main"}}}}
	if diff := cmp.Diff(wantOwned, rep.ActionOwnedCaches); diff != "" {
		t.Errorf("ActionOwnedCaches differs (-want +got):\n%s", diff)
	}
	wantUnrecognised := []rules.RepoList[rules.CacheItem]{{Repo: "alpha", Items: []rules.CacheItem{{Key: "cache-other-01", Size: "1 KB", Ref: "main"}}}}
	if diff := cmp.Diff(wantUnrecognised, rep.UnrecognisedCaches); diff != "" {
		t.Errorf("UnrecognisedCaches differs (-want +got):\n%s", diff)
	}
}

// TestCacheLists_namesTheRef: each item names the ref its cache was saved
// from, so the same key saved from several refs, or saved again, reads as
// separate entries rather than one key repeated. A branch is shown by its
// name, a pull request's merge or head ref as "PR <n>", and any other ref
// as GitHub gave it. Items sort by key and then by the ref as GitHub gave
// it.
func TestCacheLists_namesTheRef(t *testing.T) {
	t.Parallel()

	at := func(ref string, size int64) audit.Cache {
		return audit.Cache{Key: "custom-01", Ref: ref, SizeBytes: size, CreatedAt: cacheNow}
	}
	alpha := fixture("alpha", "tools")
	alpha.Storage.Caches = []audit.Cache{
		at("refs/pull/12/merge", 1),
		at("refs/heads/main", 2),
		at("refs/heads/feature/x", 3),
		at("refs/pull/12/head", 4),
		at("refs/tags/v1.0.0", 5),
		at("refs/pull/abc/merge", 6),
		at("refs/pull/13/other", 7),
		at("refs/heads/main", 8),
	}

	rep := cacheListsReport(t, alpha)
	want := []rules.RepoList[rules.CacheItem]{{Repo: "alpha", Items: []rules.CacheItem{
		{Key: "custom-01", Size: "3 B", Ref: "feature/x"},
		{Key: "custom-01", Size: "2 B", Ref: "main"},
		{Key: "custom-01", Size: "8 B", Ref: "main"},
		{Key: "custom-01", Size: "4 B", Ref: "PR 12"},
		{Key: "custom-01", Size: "1 B", Ref: "PR 12"},
		{Key: "custom-01", Size: "7 B", Ref: "refs/pull/13/other"},
		{Key: "custom-01", Size: "6 B", Ref: "refs/pull/abc/merge"},
		{Key: "custom-01", Size: "5 B", Ref: "refs/tags/v1.0.0"},
	}}}
	if diff := cmp.Diff(want, rep.UnrecognisedCaches); diff != "" {
		t.Errorf("UnrecognisedCaches differs (-want +got):\n%s", diff)
	}
}

// TestCacheLists_noCachesNoLists: a repository with no caches outside the
// tool's prefixes is on neither key list, and none at all leaves every list
// empty.
func TestCacheLists_noCachesNoLists(t *testing.T) {
	t.Parallel()

	rep := cacheListsReport(t, fixture("alpha", "tools"), cachedFixture(mainCache("go-Linux-abc", 95_000_000, cacheNow)))
	if rep.UnrecognisedCaches != nil || rep.ActionOwnedCaches != nil || rep.PRCaches != nil {
		t.Errorf("lists = %v, %v, %v, want all nil", rep.UnrecognisedCaches, rep.ActionOwnedCaches, rep.PRCaches)
	}
}

// TestCacheLists_prCachesSummed: a repository's Go and Nix caches saved
// from pull-request refs are counted and summed into one item; caches from
// the default branch, and pull-request caches under any other prefix, are
// not counted.
func TestCacheLists_prCachesSummed(t *testing.T) {
	t.Parallel()

	alpha := fixture("alpha", "tools")
	alpha.Storage.Caches = []audit.Cache{
		prCache("go-Linux-abc", 95_000_000, "3"),
		prCache("nix-Linux-abc", 206_000_000, "4"),
		prCache("nix-macOS-abc", 1_000_000, "4"),
		prCache("custom-01", 900_000_000, "4"),
		mainCache("nix-Linux-def", 500_000_000, cacheNow),
	}
	bravo := fixture("bravo", "tools")
	bravo.Storage.Caches = []audit.Cache{prCache("go-Linux-abc", 430, "9")}
	charlie := fixture("charlie", "tools")
	charlie.Storage.Caches = []audit.Cache{mainCache("go-Linux-abc", 95_000_000, cacheNow)}

	rep := cacheListsReport(t, charlie, bravo, alpha)
	want := []rules.RepoList[string]{
		{Repo: "alpha", Items: []string{"3 caches · 302 MB"}},
		{Repo: "bravo", Items: []string{"1 cache · 430 B"}},
	}
	if diff := cmp.Diff(want, rep.PRCaches); diff != "" {
		t.Errorf("PRCaches differs (-want +got):\n%s", diff)
	}
}
