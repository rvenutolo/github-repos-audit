package rules_test

import (
	"testing"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// cacheNow is the snapshot instant the nix_cache tests judge against.
var cacheNow = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// cacheNixAction is a pinned cache-nix-action reference, as a workflow step
// writes it.
const cacheNixAction = "nix-community/cache-nix-action@0123456789abcdef0123456789abcdef01234567"

// nixCache judges one tools repository, which standard-types.toml holds to
// nix_cache = "required", as of cacheNow, and returns its nix_cache cell.
func nixCache(t *testing.T, r audit.Repo) rules.Cell {
	t.Helper()
	rep, err := rules.Evaluate(&audit.Snapshot{
		GeneratedAt: cacheNow, Types: standardTypes(), Identity: fixtureIdentity(), Hermetic: fixtureHermetic(),
		Repos: []audit.Repo{r},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	return rep.Repos[0].Cell(rules.CheckNixCache)
}

// mainCache is a cache entry saved from the fixture's default branch.
func mainCache(key string, size int64, created time.Time) audit.Cache {
	return audit.Cache{Key: key, Ref: "refs/heads/main", SizeBytes: size, CreatedAt: created}
}

// cachedFixture is a tools fixture whose default branch ran CI an hour before
// cacheNow and holds the given caches, so nix_cache is judged.
func cachedFixture(caches ...audit.Cache) audit.Repo {
	r := fixture("alpha", "tools")
	r.CI.LastDefaultBranchRun = cacheNow.Add(-time.Hour)
	r.Storage.Caches = caches
	return r
}

// cacheWorkflow is one workflow whose one job, with the given permissions,
// runs the given steps.
func cacheWorkflow(wf, job audit.Permissions, steps ...audit.StepUse) audit.Workflow {
	return audit.Workflow{
		Path:        ".github/workflows/ci.yml",
		Events:      []string{"push"},
		Permissions: wf,
		Jobs:        []audit.Job{{ID: "check", Permissions: job, Uses: steps}},
	}
}

// goodCacheStep is a cache-nix-action step that bounds the store and purges
// by a selector, so it raises no problem of its own.
func goodCacheStep() audit.StepUse {
	return audit.StepUse{Action: cacheNixAction, With: map[string]string{
		"gc-max-store-size-linux": "1G",
		"purge":                   "true",
		"purge-prefixes":          "nix-Linux-",
	}}
}

// actionsWrite is a job- or workflow-level `permissions:` granting exactly
// actions: write.
func actionsWrite() audit.Permissions {
	return audit.Permissions{Set: true, Scopes: map[string]string{"actions": "write"}}
}

func wantCell(t *testing.T, got rules.Cell, verdict rules.Verdict, value string) {
	t.Helper()
	if got.Verdict != verdict || got.Value != value || !got.Mark {
		t.Errorf("cell = %v %q mark=%t, want a marked %v %q", got.Verdict, got.Value, got.Mark, verdict, value)
	}
}

// TestEvaluate_nixCache_noRecentRunIsNA: a cache is only expected where CI
// has run on the default branch in the last seven days, since caches expire
// when nothing uses them; nor is one expected without a flake.nix.
func TestEvaluate_nixCache_noRecentRunIsNA(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*audit.Repo)
		want   rules.Verdict
	}{
		{"never ran", func(r *audit.Repo) { r.CI.LastDefaultBranchRun = time.Time{} }, rules.NA},
		{"ran 7 days and a second ago", func(r *audit.Repo) { r.CI.LastDefaultBranchRun = cacheNow.Add(-7*24*time.Hour - time.Second) }, rules.NA},
		{"ran exactly 7 days ago", func(r *audit.Repo) { r.CI.LastDefaultBranchRun = cacheNow.Add(-7 * 24 * time.Hour) }, rules.Pass},
		{"no flake.nix", func(r *audit.Repo) { r.Files.FlakeNix = false }, rules.NA},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := cachedFixture(mainCache("nix-Linux-abc", 206_000_000, cacheNow.Add(-time.Hour)))
			tc.mutate(&r)
			if got := nixCache(t, r); got.Verdict != tc.want {
				t.Errorf("cell = %v %q, want %v", got.Verdict, got.Value, tc.want)
			}
		})
	}
}

// TestEvaluate_nixCache_noneOnMain: only a cache saved from the default
// branch serves it, so a pull request's Nix cache, or a default-branch cache
// of another kind, leaves it with none.
func TestEvaluate_nixCache_noneOnMain(t *testing.T) {
	t.Parallel()

	r := cachedFixture(
		audit.Cache{Key: "nix-Linux-abc", Ref: "refs/pull/3/merge", SizeBytes: 206_000_000, CreatedAt: cacheNow.Add(-time.Hour)},
		mainCache("go-Linux-abc", 50_000_000, cacheNow.Add(-time.Hour)),
	)
	wantCell(t, nixCache(t, r), rules.Fail, "none on main")
}

// TestEvaluate_nixCache_newestEntryJudged: the entry the next run restores is
// the family's newest, so it alone is judged, whatever older entries hold.
func TestEvaluate_nixCache_newestEntryJudged(t *testing.T) {
	t.Parallel()

	older, newer := cacheNow.Add(-48*time.Hour), cacheNow.Add(-time.Hour)
	r := cachedFixture(mainCache("nix-Linux-aaa", 430, older), mainCache("nix-Linux-bbb", 206_000_000, newer))
	wantCell(t, nixCache(t, r), rules.Pass, "206 MB")

	r = cachedFixture(mainCache("nix-Linux-aaa", 206_000_000, older), mainCache("nix-Linux-bbb", 430, newer))
	wantCell(t, nixCache(t, r), rules.Fail, "empty (430 B)")
}

// TestEvaluate_nixCache_ceiling: the newest entry must hold at least 1 MB, so
// it holds a store at all, and at most 1.5 GB, past which restoring it costs
// more than building; both bounds are inclusive.
func TestEvaluate_nixCache_ceiling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		size    int64
		verdict rules.Verdict
		value   string
	}{
		{1_500_000_000, rules.Pass, "1.5 GB"},
		{1_500_000_001, rules.Fail, "1.6 GB"},
		{1_400_000_001, rules.Pass, "1.4 GB"},
		{1_000_000, rules.Pass, "1 MB"},
		{999_999, rules.Fail, "empty (999 KB)"},
	}
	for _, tc := range tests {
		r := cachedFixture(mainCache("nix-Linux-abc", tc.size, cacheNow.Add(-time.Hour)))
		got := nixCache(t, r)
		if got.Verdict != tc.verdict || got.Value != tc.value {
			t.Errorf("%d B: cell = %v %q, want %v %q", tc.size, got.Verdict, got.Value, tc.verdict, tc.value)
		}
	}
}

// TestEvaluate_nixCache_perOSFamilies: each runner OS restores its own
// family, so each is judged; the cell names the one that fails, or with all
// in range the largest.
func TestEvaluate_nixCache_perOSFamilies(t *testing.T) {
	t.Parallel()

	created := cacheNow.Add(-time.Hour)
	r := cachedFixture(mainCache("nix-Linux-abc", 206_000_000, created), mainCache("nix-macOS-abc", 430, created))
	wantCell(t, nixCache(t, r), rules.Fail, "empty (430 B)")

	r = cachedFixture(mainCache("nix-Linux-abc", 2_200_000_000, created), mainCache("nix-macOS-abc", 300_000_000, created))
	wantCell(t, nixCache(t, r), rules.Fail, "2.2 GB")

	r = cachedFixture(mainCache("nix-Linux-abc", 206_000_000, created), mainCache("nix-macOS-abc", 300_000_000, created))
	wantCell(t, nixCache(t, r), rules.Pass, "300 MB")
}

// TestEvaluate_nixCache_settingsThroughComposite: a job that uses a local
// composite runs its cache-nix-action step, so the step's settings are
// judged there, under the calling job's permissions.
func TestEvaluate_nixCache_settingsThroughComposite(t *testing.T) {
	t.Parallel()

	r := cachedFixture(mainCache("nix-Linux-abc", 206_000_000, cacheNow.Add(-time.Hour)))
	r.Workflows = []audit.Workflow{cacheWorkflow(
		audit.Permissions{},
		audit.Permissions{Set: true, Scopes: map[string]string{"contents": "read"}},
		audit.StepUse{Action: "$/.github/actions/setup"},
	)}
	r.Actions = []audit.CompositeAction{{
		Path: ".github/actions/setup/action.yml",
		Uses: []audit.StepUse{{Action: cacheNixAction, With: map[string]string{"gc-max-store-size-linux": "1G", "purge": "true"}}},
	}}
	wantCell(t, nixCache(t, r), rules.Fail, "206 MB, no purge selector, no actions: write")
}

// TestEvaluate_nixCache_eachSetting: each missing setting is named once, in a
// fixed order, however many steps lack it, and a step with every setting and
// actions: write passes.
func TestEvaluate_nixCache_eachSetting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		with map[string]string
		want string
	}{
		{"every setting", goodCacheStep().With, "206 MB"},
		{"the macOS store bound", map[string]string{"gc-max-store-size-macos": "1G", "purge": "true", "purge-created": "0"}, "206 MB"},
		{"the shared store bound", map[string]string{"gc-max-store-size": "1G", "purge": "true", "purge-last-accessed": "0"}, "206 MB"},
		{"the primary-key selector", map[string]string{"gc-max-store-size": "1G", "purge": "true", "purge-primary-key": "never"}, "206 MB"},
		{"no store bound", map[string]string{"purge": "true", "purge-prefixes": "nix-"}, "206 MB, no gc-max-store-size"},
		{"an empty store bound", map[string]string{"gc-max-store-size": "", "purge": "true", "purge-prefixes": "nix-"}, "206 MB, no gc-max-store-size"},
		{"purge off", map[string]string{"gc-max-store-size": "1G", "purge": "false", "purge-prefixes": "nix-"}, "206 MB, no purge"},
		// YAML reads True and TRUE as the same boolean as true.
		{"purge in capitals", map[string]string{"gc-max-store-size": "1G", "purge": "TRUE", "purge-prefixes": "nix-"}, "206 MB"},
		{"purge capitalised", map[string]string{"gc-max-store-size": "1G", "purge": "True", "purge-prefixes": "nix-"}, "206 MB"},
		{"nothing", nil, "206 MB, no gc-max-store-size, no purge, no purge selector"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := cachedFixture(mainCache("nix-Linux-abc", 206_000_000, cacheNow.Add(-time.Hour)))
			step := audit.StepUse{Action: cacheNixAction, With: tc.with}
			// Two lacking steps name each problem once.
			r.Workflows = []audit.Workflow{cacheWorkflow(audit.Permissions{}, actionsWrite(), step, step)}
			verdict := rules.Fail
			if tc.want == "206 MB" {
				verdict = rules.Pass
			}
			wantCell(t, nixCache(t, r), verdict, tc.want)
		})
	}
}

// TestEvaluate_nixCache_actionNameAnyCase: GitHub matches an action's owner
// and repository in any case, so a step naming cache-nix-action in other
// capitals is the same action and is judged; a longer repository name, or a
// path inside the repository, is a different action and is not.
func TestEvaluate_nixCache_actionNameAnyCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action, want string
	}{
		{"Nix-Community/Cache-Nix-Action@v6", "206 MB, no gc-max-store-size, no purge, no purge selector"},
		{"nix-community/cache-nix-action-fork@v6", "206 MB"},
		{"nix-community/cache-nix-action/restore@v6", "206 MB"},
	}
	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			t.Parallel()

			r := cachedFixture(mainCache("nix-Linux-abc", 206_000_000, cacheNow.Add(-time.Hour)))
			r.Workflows = []audit.Workflow{cacheWorkflow(audit.Permissions{}, actionsWrite(), audit.StepUse{Action: tc.action})}
			verdict := rules.Fail
			if tc.want == "206 MB" {
				verdict = rules.Pass
			}
			wantCell(t, nixCache(t, r), verdict, tc.want)
		})
	}
}

// TestEvaluate_nixCache_workflowPermissionsInherited: a job without its own
// `permissions:` takes the workflow's, and with neither the repository's
// default; whichever applies must grant actions: write, which purging needs.
func TestEvaluate_nixCache_workflowPermissionsInherited(t *testing.T) {
	t.Parallel()

	set := func(scopes map[string]string) audit.Permissions { return audit.Permissions{Set: true, Scopes: scopes} }
	all := func(word string) audit.Permissions { return audit.Permissions{Set: true, All: word} }
	tests := []struct {
		name     string
		workflow audit.Permissions
		job      audit.Permissions
		def      string
		ok       bool
	}{
		{"the workflow grants it", actionsWrite(), audit.Permissions{}, "read", true},
		{"the workflow grants everything", all("write-all"), audit.Permissions{}, "read", true},
		{"the workflow reads everything", all("read-all"), audit.Permissions{}, "write", false},
		{"the workflow reads actions", set(map[string]string{"actions": "read"}), audit.Permissions{}, "write", false},
		{"the job overrides a granting workflow", actionsWrite(), set(map[string]string{}), "write", false},
		{"the job grants it", set(map[string]string{"contents": "read"}), actionsWrite(), "read", true},
		{"the repository default writes", audit.Permissions{}, audit.Permissions{}, "write", true},
		{"the repository default reads", audit.Permissions{}, audit.Permissions{}, "read", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := cachedFixture(mainCache("nix-Linux-abc", 206_000_000, cacheNow.Add(-time.Hour)))
			r.Settings.DefaultWorkflowPermissions = tc.def
			r.Workflows = []audit.Workflow{cacheWorkflow(tc.workflow, tc.job, goodCacheStep())}
			if tc.ok {
				wantCell(t, nixCache(t, r), rules.Pass, "206 MB")
			} else {
				wantCell(t, nixCache(t, r), rules.Fail, "206 MB, no actions: write")
			}
		})
	}
}

// TestEvaluate_nixCache_unreadableComposite: a workflow, or a composite a job
// uses, that could not be read may hold a misconfigured cache step, so each
// is named once, in path order, and the cell fails.
func TestEvaluate_nixCache_unreadableComposite(t *testing.T) {
	t.Parallel()

	r := cachedFixture(mainCache("nix-Linux-abc", 206_000_000, cacheNow.Add(-time.Hour)))
	setup := audit.StepUse{Action: "./.github/actions/setup"}
	r.Workflows = []audit.Workflow{
		cacheWorkflow(audit.Permissions{}, actionsWrite(), setup, setup, goodCacheStep()),
		{Path: ".github/workflows/a.yml", ParseError: "invalid YAML"},
	}
	r.Actions = []audit.CompositeAction{
		{Path: ".github/actions/setup/action.yml", ParseError: "invalid YAML"},
		// Used by no job, so it cannot affect the cache.
		{Path: ".github/actions/unused/action.yml", ParseError: "invalid YAML"},
	}
	wantCell(t, nixCache(t, r), rules.Fail,
		"206 MB, unreadable .github/actions/setup/action.yml, unreadable .github/workflows/a.yml")

	// With no cache the problems still follow.
	r.Storage.Caches = nil
	wantCell(t, nixCache(t, r), rules.Fail,
		"none on main, unreadable .github/actions/setup/action.yml, unreadable .github/workflows/a.yml")
}

// TestFormatBytes: sizes are decimal, whole numbers of B, KB and MB, and GB
// to one place, shown in the nix_cache cell.
func TestFormatBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		size int64
		want string
	}{
		{430, "empty (430 B)"},
		{999, "empty (999 B)"},
		{1_000, "empty (1 KB)"},
		{12_345, "empty (12 KB)"},
		{206_789_000, "206 MB"},
		{999_999_999, "999 MB"},
		{1_000_000_000, "1.0 GB"},
		{1_499_999_999, "1.4 GB"},
	}
	for _, tc := range tests {
		r := cachedFixture(mainCache("nix-Linux-abc", tc.size, cacheNow.Add(-time.Hour)))
		if got := nixCache(t, r); got.Value != tc.want {
			t.Errorf("%d B: value = %q, want %q", tc.size, got.Value, tc.want)
		}
	}
}

// TestFormatBytesUp: a size that fails the ceiling shows its GB tenths
// rounded up, so it never reads the same as the passing bound; whole tenths
// are unchanged. Seen through the nix_cache cell, the public surface.
func TestFormatBytesUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		size int64
		want string
	}{
		{1_500_000_001, "1.6 GB"},
		{1_599_999_999, "1.6 GB"},
		{1_600_000_000, "1.6 GB"},
		{2_000_000_000, "2.0 GB"},
		{2_000_000_001, "2.1 GB"},
		{2_249_999_999, "2.3 GB"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			r := cachedFixture(mainCache("nix-Linux-abc", tc.size, cacheNow.Add(-time.Hour)))
			got := nixCache(t, r)
			if got.Verdict != rules.Fail || got.Value != tc.want {
				t.Errorf("%d B: cell = %v %q, want Fail %q", tc.size, got.Verdict, got.Value, tc.want)
			}
		})
	}
}

// TestEvaluate_nixCache_reachesTheWorklist: a missing cache is a gap under
// its own label.
func TestEvaluate_nixCache_reachesTheWorklist(t *testing.T) {
	t.Parallel()

	r := cachedFixture()
	rep, err := rules.Evaluate(&audit.Snapshot{
		GeneratedAt: cacheNow, Types: standardTypes(), Identity: fixtureIdentity(), Hermetic: fixtureHermetic(),
		Repos: []audit.Repo{r},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	const label = "Nix cache missing, empty, oversized or misconfigured"
	for _, g := range rep.Gaps {
		if g.Label == label && len(g.Repos) == 1 && g.Repos[0] == "alpha" {
			return
		}
	}
	t.Errorf("Gaps = %+v, want alpha under %q", rep.Gaps, label)
}
