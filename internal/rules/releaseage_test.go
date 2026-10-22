package rules_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// withAge sets the collected Renovate facts on a fixture.
func withAge(r audit.Repo, value, errText string) audit.Repo {
	r.Renovate = audit.Renovate{MinReleaseAge: value, MinReleaseAgeError: errText}
	if value != "" {
		r.Renovate.MinReleaseAgeSource = r.Files.RenovateConfig
	}
	return r
}

func withOverride(r audit.Repo, value string) audit.Repo {
	r.Overrides = map[string]string{rules.CheckRenovateMinReleaseAge.String(): value}
	return r
}

func noRenovate(r audit.Repo) audit.Repo {
	r.Files.RenovateConfig = ""
	r.Renovate = audit.Renovate{}
	return r
}

// standard-types.toml: tools expects "7 days", content is not_required.
func TestEvaluate_minReleaseAge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		repo           audit.Repo
		want           rules.Verdict
		wantValue      string
		wantOverridden bool
	}{
		{"at the threshold passes", withAge(fixture("a", "tools"), "7 days", ""), rules.Pass, "7 days", false},
		{"another spelling of the threshold passes", withAge(fixture("a", "tools"), "1 week", ""), rules.Pass, "1 week", false},
		{"above the threshold passes", withAge(fixture("a", "tools"), "1 year", ""), rules.Pass, "1 year", false},
		{"below the threshold fails", withAge(fixture("a", "tools"), "3 days", ""), rules.Fail, "3 days", false},
		{"unset fails", withAge(fixture("a", "tools"), "", ""), rules.Fail, "none", false},
		{"unresolved fails", withAge(fixture("a", "tools"), "", "preset x: not found"), rules.Fail, "unresolved", false},
		{"an unparsable value is unresolved", withAge(fixture("a", "tools"), "7 dayz", ""), rules.Fail, "unresolved", false},
		{"a negative value is unresolved", withAge(fixture("a", "tools"), "-7 days", ""), rules.Fail, "unresolved", false},
		{"no Renovate config is n/a", noRenovate(fixture("a", "tools")), rules.NA, "", false},
		{"not_required shows the value", withAge(fixture("a", "content"), "3 days", ""), rules.NA, "3 days", false},
		{"not_required shows unresolved", withAge(fixture("a", "content"), "", "x"), rules.NA, "unresolved", false},
		{"override lowers the threshold", withOverride(withAge(fixture("a", "tools"), "3 days", ""), "3 days"), rules.Pass, "3 days", true},
		{"override raises the threshold", withOverride(withAge(fixture("a", "tools"), "7 days", ""), "14 days"), rules.Fail, "7 days", true},
		{"override not_required", withOverride(withAge(fixture("a", "tools"), "", ""), "not_required"), rules.NA, "none", true},
		{"override info", withOverride(withAge(fixture("a", "tools"), "3 days", ""), "info"), rules.Info, "3 days", true},
		{"override applies a threshold to not_required", withOverride(withAge(fixture("a", "content"), "3 days", ""), "7 days"), rules.Fail, "3 days", true},
		{"override without Renovate is n/a, never a gap", withOverride(noRenovate(fixture("a", "tools")), "14 days"), rules.NA, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cells, err := evaluateOne(tt.repo)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			got := cells[rules.CheckRenovateMinReleaseAge]
			if got.Verdict != tt.want || got.Value != tt.wantValue || got.Overridden != tt.wantOverridden {
				t.Errorf("cell = %v %q overridden=%t; want %v %q overridden=%t",
					got.Verdict, got.Value, got.Overridden, tt.want, tt.wantValue, tt.wantOverridden)
			}
			// The mark is shown beside the value, so "✗ 3 days" reads as a
			// gap and not as a plain fact.
			if !got.Mark || !got.Scalar {
				t.Errorf("cell Mark = %t, Scalar = %t; want both true", got.Mark, got.Scalar)
			}
		})
	}
}

func TestEvaluate_minReleaseAgeReachesTheWorklist(t *testing.T) {
	t.Parallel()

	short := withAge(fixture("short", "tools"), "3 days", "")
	fine := withAge(fixture("fine", "tools"), "7 days", "")
	excused := withOverride(noRenovate(fixture("excused", "tools")), "14 days")
	rep, err := rules.Evaluate(&audit.Snapshot{Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{excused, fine, short}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	var gap *rules.Gap
	for i := range rep.Gaps {
		if rep.Gaps[i].Check == rules.CheckRenovateMinReleaseAge {
			gap = &rep.Gaps[i]
		}
	}
	if gap == nil || !slices.Equal(gap.Repos, []string{"short"}) {
		t.Errorf("min-release-age gap = %+v, want exactly [short]", gap)
	}
	if !slices.Contains(rep.Overrides, rules.Override{Repo: "excused", Check: rules.CheckRenovateMinReleaseAge, Value: "14 days"}) {
		t.Errorf("Overrides = %+v, want the excused repository's override listed", rep.Overrides)
	}
}

func TestValidThresholdOverride(t *testing.T) {
	t.Parallel()

	for _, v := range []string{"7 days", "1 week", "not_required", "info"} {
		if !rules.ValidThresholdOverride(v) {
			t.Errorf("ValidThresholdOverride(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"required", "public", "public_published", "7 dayz", "7d", ""} {
		if rules.ValidThresholdOverride(v) {
			t.Errorf("ValidThresholdOverride(%q) = true, want false", v)
		}
	}
}

// TestValidateOffline_anyThresholdOverrideOnAnInfoRowIsDead: a type that only
// shows the release age never counts it as a gap, so no override can change
// what the report says — the same rule every informational row follows.
func TestValidateOffline_anyThresholdOverrideOnAnInfoRowIsDead(t *testing.T) {
	t.Parallel()

	types := rules.Types{"shown": table(map[string]string{"renovate_min_release_age": "info"})}
	for _, v := range []string{"14 days", rules.OverrideNotRequired} {
		decls := []rules.Declaration{{Name: "alpha", Type: "shown", Overrides: map[string]string{"renovate_min_release_age": v}}}
		err := rules.ValidateOffline(types, decls)
		if err == nil || !strings.Contains(err.Error(), "informational") {
			t.Errorf("ValidateOffline(override %q on an info row) = %v, want a dead-override error naming the row informational", v, err)
		}
	}
}
