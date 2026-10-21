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
		GeneratedAt: cacheNow, Types: standardTypes(), Identity: fixtureIdentity(), Hermetic: fixtureHermetic(),
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

	wantUnrecognised := []rules.RepoList{
		{Repo: "alpha", Items: []string{"custom-01 (1 KB)", "custom-01 (900 B)", "nixpkgs-01 (5 B)", "zeta-cache (2 MB)"}},
		{Repo: "bravo", Items: []string{"node-cache (1.0 GB)"}},
	}
	if diff := cmp.Diff(wantUnrecognised, rep.UnrecognisedCaches); diff != "" {
		t.Errorf("UnrecognisedCaches differs (-want +got):\n%s", diff)
	}
	wantActionOwned := []rules.RepoList{{Repo: "alpha", Items: []string{
		"determinatesystem-nix-installer-01 (3 KB)", "gitleaks-01 (40 KB)", "harden-runner-cacheKey-01 (12 B)", "trivy-db-01 (61 MB)",
	}}}
	if diff := cmp.Diff(wantActionOwned, rep.ActionOwnedCaches); diff != "" {
		t.Errorf("ActionOwnedCaches differs (-want +got):\n%s", diff)
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
	want := []rules.RepoList{
		{Repo: "alpha", Items: []string{"3 caches · 302 MB"}},
		{Repo: "bravo", Items: []string{"1 cache · 430 B"}},
	}
	if diff := cmp.Diff(want, rep.PRCaches); diff != "" {
		t.Errorf("PRCaches differs (-want +got):\n%s", diff)
	}
}
