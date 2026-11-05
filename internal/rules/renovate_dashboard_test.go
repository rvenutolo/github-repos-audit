package rules_test

import (
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

func TestObserve_renovateDashboard(t *testing.T) {
	t.Parallel()

	withDashboard := func(r audit.Repo, d audit.RenovateDashboard) audit.Repo {
		r.RenovateDashboard = d
		return r
	}
	issuesOff := func(r audit.Repo) audit.Repo {
		r.Settings.HasIssues = false
		r.RenovateDashboard = audit.RenovateDashboard{}
		return r
	}
	tests := []struct {
		name      string
		repo      audit.Repo
		want      rules.Verdict
		wantValue string
	}{
		{"no Renovate config is n/a", noRenovate(fixture("a", "tools")), rules.NA, ""},
		{"open dashboard passes", withDashboard(fixture("a", "tools"), audit.RenovateDashboard{Number: 7, State: "OPEN"}), rules.Pass, "#7"},
		{"no dashboard fails", withDashboard(fixture("a", "tools"), audit.RenovateDashboard{}), rules.Fail, "no dashboard"},
		{"closed dashboard fails", withDashboard(fixture("a", "tools"), audit.RenovateDashboard{Number: 4, State: "CLOSED"}), rules.Fail, "closed #4"},
		{"issues off fails", issuesOff(fixture("a", "tools")), rules.Fail, "issues off"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cells, err := evaluateOne(tt.repo)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			got := cells[rules.CheckRenovateDashboard]
			if got.Verdict != tt.want || got.Value != tt.wantValue {
				t.Errorf("cell = %v %q; want %v %q", got.Verdict, got.Value, tt.want, tt.wantValue)
			}
			if wantMark := tt.want != rules.NA; got.Mark != wantMark {
				t.Errorf("Mark = %v, want %v", got.Mark, wantMark)
			}
		})
	}
}
