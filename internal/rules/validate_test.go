package rules_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

func TestValidateOffline_rejectsADeadOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		decl     rules.Declaration
		contains string
	}{
		{
			name: "required against a row the type already expects",
			decl: rules.Declaration{
				Name: "alpha", Type: "tools",
				Overrides: map[string]string{"renovate": rules.OverrideRequired},
			},
			contains: "already expects it",
		},
		{
			name: "not_required against a row the type already calls n/a",
			decl: rules.Declaration{
				Name: "alpha", Type: "content",
				Overrides: map[string]string{"flake_nix": rules.OverrideNotRequired},
			},
			contains: "already treats it as n/a",
		},
		{
			name: "any override against an informational row",
			decl: rules.Declaration{
				Name: "alpha", Type: "tools",
				Overrides: map[string]string{"last_push": rules.OverrideRequired},
			},
			contains: "informational",
		},
		{
			name: "a direct-push value the type already expects",
			decl: rules.Declaration{
				Name: "alpha", Type: "content",
				Overrides: map[string]string{"direct_push": "allowed"},
			},
			contains: "already expects direct push allowed",
		},
		{
			name: "required on direct push, which is always judged",
			decl: rules.Declaration{
				Name: "alpha", Type: "tools",
				Overrides: map[string]string{"direct_push": rules.OverrideRequired},
			},
			contains: "changes nothing",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := rules.ValidateOffline(standardTypes(), []rules.Declaration{tc.decl})
			if err == nil {
				t.Fatalf("ValidateOffline() error = nil, want one mentioning %q", tc.contains)
			}
			if !errors.Is(err, rules.ErrConfig) {
				t.Errorf("ValidateOffline() error = %v, want it to wrap ErrConfig", err)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("ValidateOffline() error = %q, want it to contain %q", err, tc.contains)
			}
		})
	}
}

func TestValidateOffline_acceptsALiveOverride(t *testing.T) {
	t.Parallel()

	decls := []rules.Declaration{
		// Content does not expect Renovate, so requiring it says something.
		{Name: "alpha", Type: "content", Overrides: map[string]string{"renovate": rules.OverrideRequired}},
		// Tools expects a flake, so excusing it says something.
		{Name: "bravo", Type: "tools", Overrides: map[string]string{"flake_nix": rules.OverrideNotRequired}},
		// Tools expects direct push blocked, so allowing it says something.
		{Name: "charlie", Type: "tools", Overrides: map[string]string{"direct_push": "allowed"}},
	}
	if err := rules.ValidateOffline(standardTypes(), decls); err != nil {
		t.Errorf("ValidateOffline() error = %v, want nil", err)
	}
}

// TestValidateOffline_leavesVisibilityDependentRowsAlone is the reason the
// validation is split in two: whether recipe-site's secret_scanning
// override is a no-op depends on whether the repository is public, and
// visibility is never declared.
func TestValidateOffline_leavesVisibilityDependentRowsAlone(t *testing.T) {
	t.Parallel()

	decls := []rules.Declaration{{
		Name: "recipe-site", Type: "content",
		Overrides: map[string]string{"secret_scanning": rules.OverrideNotRequired},
	}}
	if err := rules.ValidateOffline(standardTypes(), decls); err != nil {
		t.Errorf("ValidateOffline() error = %v, want nil; this needs live visibility", err)
	}
}

func TestValidate_rejectsADeadOverrideOnceVisibilityIsKnown(t *testing.T) {
	t.Parallel()

	// Private, so secret scanning is already n/a and the override says nothing.
	r := fixture("recipe-site", "content")
	r.Overrides = map[string]string{"secret_scanning": rules.OverrideNotRequired}

	err := rules.Validate(&audit.Snapshot{Types: standardTypes(), Repos: []audit.Repo{r}})
	if err == nil {
		t.Fatal("Validate() error = nil, want a dead-override error")
	}
	if !strings.Contains(err.Error(), "already treats it as n/a") {
		t.Errorf("Validate() error = %q, want a dead-override explanation", err)
	}
}

func TestValidate_acceptsTheSameOverrideOnAPublicRepository(t *testing.T) {
	t.Parallel()

	r := public(fixture("recipe-site", "content"))
	r.Overrides = map[string]string{"secret_scanning": rules.OverrideNotRequired}

	if err := rules.Validate(&audit.Snapshot{Types: standardTypes(), Repos: []audit.Repo{r}}); err != nil {
		t.Errorf("Validate() error = %v, want nil", err)
	}
}

func TestValidate_rejectsPublishedOnAPrivateRepository(t *testing.T) {
	t.Parallel()

	// The homepage and topics rules are defined for public-and-published or
	// for neither, so this combination has no answer.
	r := fixture("alpha", "software")
	r.Published = true

	err := rules.Validate(&audit.Snapshot{Types: standardTypes(), Repos: []audit.Repo{r}})
	if err == nil {
		t.Fatal("Validate() error = nil, want a published-but-private error")
	}
	if !strings.Contains(err.Error(), "published = true") {
		t.Errorf("Validate() error = %q, want it to name the combination", err)
	}
}

func TestCheckCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		discovered []string
		declared   []string
		contains   string
	}{
		{
			name:       "a new repository has no entry",
			discovered: []string{"alpha", "go-linter"},
			declared:   []string{"alpha"},
			contains:   "no entry for go-linter",
		},
		{
			name:       "an entry names a repository that is gone",
			discovered: []string{"alpha"},
			declared:   []string{"alpha", "deleted"},
			contains:   "no longer exists",
		},
		{
			name:       "both directions at once",
			discovered: []string{"alpha", "fresh"},
			declared:   []string{"alpha", "deleted"},
			contains:   "fresh",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := rules.CheckCoverage(tc.discovered, tc.declared)
			if err == nil {
				t.Fatalf("CheckCoverage() error = nil, want one mentioning %q", tc.contains)
			}
			if !errors.Is(err, rules.ErrConfig) {
				t.Errorf("CheckCoverage() error = %v, want it to wrap ErrConfig", err)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("CheckCoverage() error = %q, want it to contain %q", err, tc.contains)
			}
		})
	}
}

func TestCheckCoverage_acceptsAnExactMatch(t *testing.T) {
	t.Parallel()

	if err := rules.CheckCoverage([]string{"alpha", "bravo"}, []string{"bravo", "alpha"}); err != nil {
		t.Errorf("CheckCoverage() error = %v, want nil", err)
	}
}

func TestVerdict_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		v    rules.Verdict
		want string
	}{
		{name: "pass", v: rules.Pass, want: "pass"},
		{name: "fail", v: rules.Fail, want: "fail"},
		{name: "not applicable", v: rules.NA, want: "n/a"},
		{name: "informational", v: rules.Info, want: "info"},
		{name: "out of range", v: rules.Verdict(99), want: "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.v.String(); got != tc.want {
				t.Errorf("Verdict(%d).String() = %q, want %q", int(tc.v), got, tc.want)
			}
		})
	}
}

// TestValidateOffline_skipsWhatIsNotItsToJudge covers the three ways a
// declaration carries nothing this validation can rule on. Each is silence
// rather than an error: an unknown type and an unknown override key are the
// parser's errors to report, and "direct push not required" genuinely turns
// the row off rather than saying nothing.
func TestValidateOffline_skipsWhatIsNotItsToJudge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		decl rules.Declaration
	}{
		{
			name: "an unknown type is not in the table",
			decl: rules.Declaration{
				Name: "alpha", Type: rules.Type("houseplant"),
				Overrides: map[string]string{"renovate": rules.OverrideRequired},
			},
		},
		{
			name: "an unknown override key names no check",
			decl: rules.Declaration{
				Name: "bravo", Type: "tools",
				Overrides: map[string]string{"not_a_check": rules.OverrideRequired},
			},
		},
		{
			name: "excusing direct push turns the row off",
			decl: rules.Declaration{
				Name: "charlie", Type: "tools",
				Overrides: map[string]string{"direct_push": rules.OverrideNotRequired},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := rules.ValidateOffline(standardTypes(), []rules.Declaration{tc.decl}); err != nil {
				t.Errorf("ValidateOffline() error = %v, want nil", err)
			}
		})
	}
}

// TestValidate_skipsWhatIsNotItsToJudge is TestValidateOffline's counterpart
// on the live side. The unconditional-row case is the division of labour
// itself: a dead override against a ✓ row is ValidateOffline's error to
// report, and reporting it twice would double every such message.
func TestValidate_skipsWhatIsNotItsToJudge(t *testing.T) {
	t.Parallel()

	unknownType := fixture("alpha", "tools")
	unknownType.Type = "houseplant"
	unknownType.Overrides = map[string]string{"renovate": rules.OverrideRequired}

	unknownKey := fixture("bravo", "tools")
	unknownKey.Overrides = map[string]string{"not_a_check": rules.OverrideRequired}

	// Tools already expects Renovate, so this override is dead — but against
	// an unconditional row, which is ValidateOffline's half of the job.
	unconditional := fixture("charlie", "tools")
	unconditional.Overrides = map[string]string{"renovate": rules.OverrideRequired}

	tests := []struct {
		name string
		repo audit.Repo
	}{
		{"an unknown type is not in the table", unknownType},
		{"an unknown override key names no check", unknownKey},
		{"an unconditional row is ValidateOffline's job", unconditional},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := rules.Validate(&audit.Snapshot{Types: standardTypes(), Repos: []audit.Repo{tc.repo}}); err != nil {
				t.Errorf("Validate() error = %v, want nil", err)
			}
		})
	}
}
