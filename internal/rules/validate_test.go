package rules_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

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
			name: "info against a row the type already shows",
			decl: rules.Declaration{
				Name: "alpha", Type: "tools",
				Overrides: map[string]string{"last_push": "info"},
			},
			contains: "already shows it as info",
		},
		{
			name: "info against a present-or-absent row the type already shows",
			decl: rules.Declaration{
				Name: "alpha", Type: "software",
				Overrides: map[string]string{"pr_template": "info"},
			},
			contains: "already shows it as info",
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
		{
			name: "a threshold equal to the type's, spelled differently",
			decl: rules.Declaration{
				Name: "alpha", Type: "tools",
				Overrides: map[string]string{"renovate_min_release_age": "1 week"},
			},
			contains: "already expects the same minimum release age",
		},
		{
			name: "not_required against a type that already says so",
			decl: rules.Declaration{
				Name: "alpha", Type: "content",
				Overrides: map[string]string{"renovate_min_release_age": rules.OverrideNotRequired},
			},
			contains: "already treats it as n/a",
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
		// A shorter threshold than the type's says something.
		{Name: "delta", Type: "tools", Overrides: map[string]string{"renovate_min_release_age": "3 days"}},
		// Tools holds the release age to a threshold, so excusing it says something.
		{Name: "echo", Type: "tools", Overrides: map[string]string{"renovate_min_release_age": rules.OverrideNotRequired}},
		// Content does not judge the release age, so a threshold says something.
		{Name: "foxtrot", Type: "content", Overrides: map[string]string{"renovate_min_release_age": "7 days"}},
		// Tools expects Renovate, so showing it without judging says something.
		{Name: "golf", Type: "tools", Overrides: map[string]string{"renovate": "info"}},
		// Software only shows a PR template, so requiring one says something.
		{Name: "hotel", Type: "software", Overrides: map[string]string{"pr_template": rules.OverrideRequired}},
		// Tools shows the last push, so hiding it says something.
		{Name: "india", Type: "tools", Overrides: map[string]string{"last_push": rules.OverrideNotRequired}},
		// A visibility word against a row the type decides unconditionally
		// needs live visibility; Validate judges it.
		{Name: "juliett", Type: "tools", Overrides: map[string]string{"renovate": "public"}},
		{Name: "kilo", Type: "content", Overrides: map[string]string{"renovate": "public_published"}},
	}
	if err := rules.ValidateOffline(standardTypes(), decls); err != nil {
		t.Errorf("ValidateOffline() error = %v, want nil", err)
	}
}

// TestValidateOffline_leavesVisibilityDependentRowsAlone pins that recipe-site's
// secret_scanning override, a public cell, is left to Validate; see
// decidesUnconditionally and ValidateOffline for why.
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

// TestValidate_judgesAVisibilityWordOverride pins that an override using a
// visibility word is dead exactly when it resolves, for this repository, to
// what the type already derives.
func TestValidate_judgesAVisibilityWordOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		repo     audit.Repo
		word     string
		contains string // empty when the override is live
	}{
		{
			"public where the type expects it, on a public repository",
			public(fixture("alpha", "tools")), "public", "already expects it",
		},
		{
			"public where the type expects it, on a private repository",
			fixture("alpha", "tools"), "public", "",
		},
		{
			"public_published where the type calls it n/a, on a private repository",
			fixture("alpha", "content"), "public_published", "already treats it as n/a",
		},
		{
			"public_published where the type calls it n/a, on a public published repository",
			published(public(fixture("alpha", "content"))), "public_published", "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := tc.repo
			r.Overrides = map[string]string{"renovate": tc.word}
			err := rules.Validate(&audit.Snapshot{Types: standardTypes(), Repos: []audit.Repo{r}})
			if tc.contains == "" {
				if err != nil {
					t.Errorf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() error = nil, want one mentioning %q", tc.contains)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("Validate() error = %q, want it to contain %q", err, tc.contains)
			}
		})
	}
}

func TestValidate_rejectsPublishedOnAPrivateRepository(t *testing.T) {
	t.Parallel()

	// published = true on a private repository has no answer; see Validate.
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

// TestCoverage: the account and repos.toml are compared name for name, and a
// disagreement is a fact to report rather than an error. Each list is sorted
// the way the report sorts, free of duplicates, and nil when empty, because a
// nil list is what keeps config_drift out of a clean account's audit.json.
func TestCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		discovered []string
		declared   []string
		want       audit.ConfigDrift
	}{
		{
			name:       "an exact match in any order is no drift",
			discovered: []string{"alpha", "bravo"},
			declared:   []string{"bravo", "alpha"},
			want:       audit.ConfigDrift{},
		},
		{
			name:       "a repository with no entry is unconfigured",
			discovered: []string{"alpha", "fresh"},
			declared:   []string{"alpha"},
			want:       audit.ConfigDrift{Unconfigured: []string{"fresh"}},
		},
		{
			name:       "an entry with no repository is stale",
			discovered: []string{"alpha"},
			declared:   []string{"alpha", "retired"},
			want:       audit.ConfigDrift{StaleEntries: []string{"retired"}},
		},
		{
			name:       "both directions at once",
			discovered: []string{"alpha", "fresh"},
			declared:   []string{"alpha", "retired"},
			want:       audit.ConfigDrift{Unconfigured: []string{"fresh"}, StaleEntries: []string{"retired"}},
		},
		{
			name:       "each list sorts case-insensitively",
			discovered: []string{"zulu", "Yankee", "xray"},
			declared:   []string{"Mike", "lima", "kilo"},
			want: audit.ConfigDrift{
				Unconfigured: []string{"xray", "Yankee", "zulu"},
				StaleEntries: []string{"kilo", "lima", "Mike"},
			},
		},
		{
			name:       "case differs",
			discovered: []string{"alpha"},
			declared:   []string{"Alpha"},
			want:       audit.ConfigDrift{Unconfigured: []string{"alpha"}, StaleEntries: []string{"Alpha"}},
		},
		{
			name:       "duplicates collapse",
			discovered: []string{"fresh", "alpha", "fresh"},
			declared:   []string{"retired", "alpha", "retired"},
			want:       audit.ConfigDrift{Unconfigured: []string{"fresh"}, StaleEntries: []string{"retired"}},
		},
		{
			name:       "nothing discovered",
			discovered: nil,
			declared:   []string{"alpha"},
			want:       audit.ConfigDrift{StaleEntries: []string{"alpha"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// No EquateEmpty: nil and empty must not be confused here.
			if diff := cmp.Diff(tc.want, rules.Coverage(tc.discovered, tc.declared)); diff != "" {
				t.Errorf("Coverage() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
