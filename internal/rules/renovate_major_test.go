package rules_test

import (
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

func TestObserve_renovateAutomergeMajor(t *testing.T) {
	t.Parallel()

	withMajor := func(r audit.Repo, major bool, errText string) audit.Repo {
		r.Renovate = audit.Renovate{AutomergeMajor: major, MinReleaseAgeError: errText}
		return r
	}
	tests := []struct {
		name      string
		repo      audit.Repo
		want      rules.Verdict
		wantValue string
	}{
		{"no Renovate config is n/a", noRenovate(fixture("a", "tools")), rules.NA, ""},
		{"majors auto-merged passes", withMajor(fixture("a", "tools"), true, ""), rules.Pass, ""},
		{"majors held fails", withMajor(fixture("a", "tools"), false, ""), rules.Fail, ""},
		{"unresolved fails and says so", withMajor(fixture("a", "tools"), false, "preset x: not found"), rules.Fail, "unresolved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cells, err := evaluateOne(tt.repo)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			got := cells[rules.CheckRenovateAutomergeMajor]
			if got.Verdict != tt.want || got.Value != tt.wantValue {
				t.Errorf("cell = %v %q; want %v %q", got.Verdict, got.Value, tt.want, tt.wantValue)
			}
		})
	}
}
