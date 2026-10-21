package rules_test

import (
	"testing"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// goCache judges one tools repository, which standard-types.toml holds to
// go_cache = "required", as of cacheNow, and returns its go_cache cell.
func goCache(t *testing.T, r audit.Repo) rules.Cell {
	t.Helper()
	rep, err := rules.Evaluate(&audit.Snapshot{
		GeneratedAt: cacheNow, Types: standardTypes(), Identity: fixtureIdentity(), Hermetic: fixtureHermetic(),
		Repos: []audit.Repo{r},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	return rep.Repos[0].Cell(rules.CheckGoCache)
}

// goFixture is a Go module whose default branch ran CI an hour before
// cacheNow and holds the given caches, so go_cache is judged.
func goFixture(caches ...audit.Cache) audit.Repo {
	r := cachedFixture(caches...)
	r.Files.GoMod = true
	return r
}

// TestEvaluate_goCache_noRunsIsNA: with no default-branch run in the last
// seven days GitHub has evicted the caches whatever the workflows do, so
// there is nothing to judge.
func TestEvaluate_goCache_noRunsIsNA(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		last time.Time
		want rules.Verdict
	}{
		{"never ran", time.Time{}, rules.NA},
		{"ran 7 days and a second ago", cacheNow.Add(-7*24*time.Hour - time.Second), rules.NA},
		{"ran exactly 7 days ago", cacheNow.Add(-7 * 24 * time.Hour), rules.Pass},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := goFixture(mainCache("go-Linux-abc", 206_000_000, cacheNow.Add(-time.Hour)))
			r.CI.LastDefaultBranchRun = tc.last
			if got := goCache(t, r); got.Verdict != tc.want {
				t.Errorf("cell = %v %q, want %v", got.Verdict, got.Value, tc.want)
			}
		})
	}
}

// TestEvaluate_goCache_noGoModIsNA: a repository without a root go.mod
// builds no Go module, so it has no Go cache to keep, whatever caches it
// happens to hold.
func TestEvaluate_goCache_noGoModIsNA(t *testing.T) {
	t.Parallel()

	r := goFixture(mainCache("go-Linux-abc", 206_000_000, cacheNow.Add(-time.Hour)))
	r.Files.GoMod = false
	if got := goCache(t, r); got.Verdict != rules.NA {
		t.Errorf("cell = %v %q, want n/a", got.Verdict, got.Value)
	}
}

// TestEvaluate_goCache_noneOnMain: only a cache saved from the default
// branch serves it, so a pull request's Go cache, or a default-branch cache
// of another kind, leaves it with none.
func TestEvaluate_goCache_noneOnMain(t *testing.T) {
	t.Parallel()

	r := goFixture(
		audit.Cache{Key: "go-Linux-abc", Ref: "refs/pull/3/merge", SizeBytes: 206_000_000, CreatedAt: cacheNow.Add(-time.Hour)},
		mainCache("nix-Linux-abc", 206_000_000, cacheNow.Add(-time.Hour)),
	)
	wantCell(t, goCache(t, r), rules.Fail, "none on main")
}

// TestEvaluate_goCache_newestEntryJudged: the entry the next run restores is
// the family's newest, so a side job that saved an empty cache last fails
// the check however large the older entry is.
func TestEvaluate_goCache_newestEntryJudged(t *testing.T) {
	t.Parallel()

	older, newer := cacheNow.Add(-48*time.Hour), cacheNow.Add(-time.Hour)
	r := goFixture(mainCache("go-Linux-aaa", 206_000_000, older), mainCache("go-Linux-bbb", 430, newer))
	wantCell(t, goCache(t, r), rules.Fail, "empty (430 B)")

	r = goFixture(mainCache("go-Linux-aaa", 430, older), mainCache("go-Linux-bbb", 206_000_000, newer))
	wantCell(t, goCache(t, r), rules.Pass, "206 MB")
}

// TestEvaluate_goCache_floor: the newest entry must hold at least 1 MB, the
// floor inclusive; there is no ceiling, since a Go build cache is pruned by
// the toolchain itself.
func TestEvaluate_goCache_floor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		size    int64
		verdict rules.Verdict
		value   string
	}{
		{999_999, rules.Fail, "empty (999 KB)"},
		{1_000_000, rules.Pass, "1 MB"},
		{5_000_000_000, rules.Pass, "5.0 GB"},
	}
	for _, tc := range tests {
		r := goFixture(mainCache("go-Linux-abc", tc.size, cacheNow.Add(-time.Hour)))
		wantCell(t, goCache(t, r), tc.verdict, tc.value)
	}
}

// TestEvaluate_goCache_pass: with every family's newest entry above the
// floor, the cell shows the largest of them, and an older or a pull
// request's entry plays no part.
func TestEvaluate_goCache_pass(t *testing.T) {
	t.Parallel()

	r := goFixture(
		mainCache("go-Linux-aaa", 900_000_000, cacheNow.Add(-48*time.Hour)),
		mainCache("go-Linux-bbb", 120_000_000, cacheNow.Add(-time.Hour)),
		mainCache("go-macOS-ccc", 206_000_000, cacheNow.Add(-time.Hour)),
		audit.Cache{Key: "go-Linux-ddd", Ref: "refs/pull/3/merge", SizeBytes: 800_000_000, CreatedAt: cacheNow},
	)
	wantCell(t, goCache(t, r), rules.Pass, "206 MB")
}

// TestEvaluate_goCache_emptyFamilyFails: each runner OS restores its own
// family, so one empty family fails the check beside a healthy one, and the
// cell names the empty one.
func TestEvaluate_goCache_emptyFamilyFails(t *testing.T) {
	t.Parallel()

	r := goFixture(
		mainCache("go-Linux-aaa", 206_000_000, cacheNow.Add(-time.Hour)),
		mainCache("go-macOS-bbb", 430, cacheNow.Add(-time.Hour)),
	)
	wantCell(t, goCache(t, r), rules.Fail, "empty (430 B)")
}
