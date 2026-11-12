package rules_test

import (
	"slices"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

func TestObserveRenovateBatching(t *testing.T) {
	t.Parallel()

	withBatching := func(batching, errText string) audit.Repo {
		r := fixture("a", "tools")
		r.Renovate = audit.Renovate{Batching: batching, MinReleaseAgeError: errText}
		return r
	}
	tests := []struct {
		name      string
		repo      audit.Repo
		want      rules.Verdict
		wantValue string
	}{
		{"no Renovate config is n/a", noRenovate(fixture("a", "tools")), rules.NA, ""},
		{"ok passes with no text", withBatching("ok", ""), rules.Pass, ""},
		{"no_group", withBatching("no_group", ""), rules.Fail, "no group"},
		{"digests_ungrouped", withBatching("digests_ungrouped", ""), rules.Fail, "digests ungrouped"},
		{"majors_grouped", withBatching("majors_grouped", ""), rules.Fail, "majors grouped"},
		{"split_group", withBatching("split_group", ""), rules.Fail, "split group"},
		{"no_schedule", withBatching("no_schedule", ""), rules.Fail, "no schedule"},
		{"too_often", withBatching("too_often", ""), rules.Fail, "too often"},
		{"unreadable_schedule", withBatching("unreadable_schedule", ""), rules.Fail, "unreadable schedule"},
		{"lock_files_off_schedule", withBatching("lock_files_off_schedule", ""), rules.Fail, "lock files off-schedule"},
		{"no verdict and no reason fails bare", withBatching("", ""), rules.Fail, ""},
		{"unresolved fails and says so", withBatching("", "preset x: not found"), rules.Fail, "unresolved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cells, err := evaluateOne(tt.repo)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			got := cells[rules.CheckRenovateBatching]
			if got.Verdict != tt.want || got.Value != tt.wantValue {
				t.Errorf("cell = %v %q; want %v %q", got.Verdict, got.Value, tt.want, tt.wantValue)
			}
			if wantMark := tt.want != rules.NA; got.Mark != wantMark {
				t.Errorf("Mark = %v, want %v", got.Mark, wantMark)
			}
		})
	}
}

func TestEvaluate_renovateBatchingReachesTheWorklist(t *testing.T) {
	t.Parallel()

	often := fixture("often", "tools")
	often.Renovate.Batching = "too_often"
	fine := fixture("fine", "tools")
	fine.Renovate.Batching = "ok"
	excused := fixture("excused", "tools")
	excused.Renovate.Batching = "too_often"
	excused.Overrides = map[string]string{rules.CheckRenovateBatching.String(): rules.OverrideNotRequired}

	full, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{often, fine, excused}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	var gap *rules.Gap
	for i := range full.Gaps {
		if full.Gaps[i].Check == rules.CheckRenovateBatching {
			gap = &full.Gaps[i]
		}
	}
	if gap == nil {
		t.Fatalf("Gaps = %+v, want a renovate_batching gap", full.Gaps)
	}
	const label = "Renovate updates are not batched weekly"
	if gap.Label != label || !slices.Equal(gap.Repos, []string{"often"}) {
		t.Errorf("gap = %+v, want label %q and repos [often]", *gap, label)
	}
}
