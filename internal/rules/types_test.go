package rules_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

func errorStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, err := range errs {
		out = append(out, err.Error())
	}
	return out
}

func TestTypeProblems_acceptsCompleteTables(t *testing.T) {
	t.Parallel()

	types := rules.Types{
		"strict": table(nil),
		"lax": table(map[string]string{
			"readme":      "not_required",
			"license":     "public",
			"homepage":    "public_published",
			"topics":      "info",
			"direct_push": "allowed",
			"branches":    "not_required",
			"last_push":   "info",
		}),
		// A type no repository uses is not a problem; nothing here knows the repositories.
		"unused-type_2": table(nil),
	}
	if got := rules.TypeProblems(types); len(got) != 0 {
		t.Errorf("TypeProblems() = %q, want none", errorStrings(got))
	}
}

func TestTypeProblems_rejects(t *testing.T) {
	t.Parallel()

	missingBranches := table(nil)
	delete(missingBranches, "branches")

	tests := []struct {
		name  string
		types rules.Types
		want  string
	}{
		{
			name:  "a missing check",
			types: rules.Types{"infra": missingBranches},
			want:  `types.infra: missing check "branches"`,
		},
		{
			name:  "an unknown check",
			types: rules.Types{"infra": table(map[string]string{"licence": "required"})},
			want:  `types.infra: unknown check "licence"`,
		},
		{
			name:  "a word that is not an expectation",
			types: rules.Types{"infra": table(map[string]string{"readme": "yes"})},
			want:  `types.infra: readme = "yes" (want one of required, not_required, public, public_published, info)`,
		},
		{
			name:  "an expectation on direct push",
			types: rules.Types{"infra": table(map[string]string{"direct_push": "required"})},
			want:  `types.infra: direct_push = "required" (want blocked or allowed)`,
		},
		{
			name:  "a direct-push word on another check",
			types: rules.Types{"infra": table(map[string]string{"license": "blocked"})},
			want:  `types.infra: license = "blocked" (want one of required, not_required, public, public_published, info)`,
		},
		{
			name:  "required on a value-only check",
			types: rules.Types{"infra": table(map[string]string{"last_push": "required"})},
			want:  `types.infra: last_push = "required" (want info or not_required)`,
		},
		{
			name:  "public on a value-only check",
			types: rules.Types{"infra": table(map[string]string{"open_prs": "public"})},
			want:  `types.infra: open_prs = "public" (want info or not_required)`,
		},
		{
			name:  "a type name outside the pattern",
			types: rules.Types{"Infra": table(nil)},
			want:  "types.Infra: type name must match [a-z0-9_-]+",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := errorStrings(rules.TypeProblems(tc.types))
			if diff := cmp.Diff([]string{tc.want}, got); diff != "" {
				t.Errorf("TypeProblems() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestTypeProblems_minReleaseAgeWords: the threshold check takes a repos.toml
// duration or one of the two words that switch the judgement off, and nothing
// else — a bare required carries no threshold.
func TestTypeProblems_minReleaseAgeWords(t *testing.T) {
	t.Parallel()

	for _, word := range []string{"7 days", "1 week", "48 hours", "not_required", "info"} {
		if p := rules.TypeProblems(rules.Types{"t": table(map[string]string{"renovate_min_release_age": word})}); len(p) != 0 {
			t.Errorf("renovate_min_release_age = %q: TypeProblems = %v, want none", word, p)
		}
	}
	for _, word := range []string{"required", "public", "public_published", "7 dayz", "7d", "0 days"} {
		p := rules.TypeProblems(rules.Types{"t": table(map[string]string{"renovate_min_release_age": word})})
		if len(p) != 1 || !strings.Contains(p[0].Error(), `want a duration like "7 days", not_required or info`) {
			t.Errorf("renovate_min_release_age = %q: TypeProblems = %v, want one problem naming the accepted forms", word, p)
		}
	}
}

// TestTypeProblems_reportsEveryProblemInAStableOrder: one run of `audit
// validate` shows the whole table, ordered by type name and then by check
// order, with unknown names after the checks.
func TestTypeProblems_reportsEveryProblemInAStableOrder(t *testing.T) {
	t.Parallel()

	bravo := table(map[string]string{"zzz": "required", "readme": "yes"})
	delete(bravo, "branches")
	types := rules.Types{
		"bravo": bravo,
		"alpha": {},
	}

	got := errorStrings(rules.TypeProblems(types))
	if n := len(rules.Checks()) + 3; len(got) != n {
		t.Fatalf("TypeProblems() returned %d problems, want %d: %q", len(got), n, got)
	}
	if !strings.HasPrefix(got[0], `types.alpha: missing check "readme"`) {
		t.Errorf("first problem = %q, want alpha's first missing check", got[0])
	}
	tail := got[len(got)-3:]
	want := []string{
		`types.bravo: readme = "yes" (want one of required, not_required, public, public_published, info)`,
		`types.bravo: missing check "branches"`,
		`types.bravo: unknown check "zzz"`,
	}
	if diff := cmp.Diff(want, tail); diff != "" {
		t.Errorf("bravo's problems mismatch (-want +got):\n%s", diff)
	}
}

// The type names below are deliberately none of the six standard types, so
// a pass proves the table was read rather than a built-in.

func TestEvaluate_readsTheTypesTable(t *testing.T) {
	t.Parallel()

	types := rules.Types{
		"strict": table(nil),
		"lax":    table(map[string]string{"flake_nix": "not_required", "direct_push": "allowed"}),
	}
	strict := fixture("alpha", "strict")
	strict.Files.FlakeNix = false
	lax := fixture("bravo", "lax")
	lax.Files.FlakeNix = false

	rep, err := rules.Evaluate(&audit.Snapshot{Types: types, Repos: []audit.Repo{strict, lax}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	cells := map[string]map[rules.Check]rules.Cell{}
	for _, r := range rep.Repos {
		cells[r.Repo.Name] = r.Cells
	}
	if got := cells["alpha"][rules.CheckFlakeNix].Verdict; got != rules.Fail {
		t.Errorf("strict flake_nix verdict = %v, want Fail", got)
	}
	if got := cells["bravo"][rules.CheckFlakeNix].Verdict; got != rules.NA {
		t.Errorf("lax flake_nix verdict = %v, want NA", got)
	}
	// The fixture blocks direct push; lax expects it allowed.
	if got := cells["alpha"][rules.CheckDirectPush].Verdict; got != rules.Pass {
		t.Errorf("strict direct_push verdict = %v, want Pass", got)
	}
	if got := cells["bravo"][rules.CheckDirectPush].Verdict; got != rules.Fail {
		t.Errorf("lax direct_push verdict = %v, want Fail", got)
	}
}

func TestEvaluate_rejectsARepositoryWhoseTypeTheTableLacks(t *testing.T) {
	t.Parallel()

	snap := &audit.Snapshot{Types: rules.Types{"strict": table(nil)}, Repos: []audit.Repo{fixture("alpha", "lax")}}
	_, err := rules.Evaluate(snap)
	if err == nil || !strings.Contains(err.Error(), `unknown type "lax"`) {
		t.Fatalf("Evaluate() error = %v, want an unknown-type error naming lax", err)
	}
}

func TestEvaluate_rejectsAnUnusableTypesTable(t *testing.T) {
	t.Parallel()

	snap := &audit.Snapshot{Types: rules.Types{"strict": {}}, Repos: []audit.Repo{fixture("alpha", "strict")}}
	_, err := rules.Evaluate(snap)
	if err == nil || !strings.Contains(err.Error(), `types.strict: missing check "readme"`) {
		t.Fatalf("Evaluate() error = %v, want the table's problems", err)
	}
}

// TestValidateOffline_judgesOverridesAgainstTheTable: the same override is dead
// for a type that already requires the check and live for one that does not.
func TestValidateOffline_judgesOverridesAgainstTheTable(t *testing.T) {
	t.Parallel()

	types := rules.Types{
		"strict": table(nil),
		"lax":    table(map[string]string{"flake_nix": "not_required", "direct_push": "allowed"}),
	}
	decls := []rules.Declaration{
		{Name: "alpha", Type: "strict", Overrides: map[string]string{"flake_nix": rules.OverrideRequired}},
		{Name: "bravo", Type: "lax", Overrides: map[string]string{"flake_nix": rules.OverrideRequired}},
		{Name: "charlie", Type: "lax", Overrides: map[string]string{"direct_push": "allowed"}},
	}
	err := rules.ValidateOffline(types, decls)
	if err == nil {
		t.Fatal("ValidateOffline() error = nil, want alpha and charlie reported")
	}
	msg := err.Error()
	for _, want := range []string{"alpha: override flake_nix", "strict already expects it", "charlie: override direct_push", "lax already expects direct push allowed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("ValidateOffline() error = %q, want it to contain %q", msg, want)
		}
	}
	if strings.Contains(msg, "bravo") {
		t.Errorf("ValidateOffline() error = %q, want bravo's live override accepted", msg)
	}
}

func TestValidateOffline_rejectsAnUnusableTypesTable(t *testing.T) {
	t.Parallel()

	err := rules.ValidateOffline(rules.Types{"strict": {}}, nil)
	if !errors.Is(err, rules.ErrConfig) {
		t.Errorf("ValidateOffline() error = %v, want it to wrap ErrConfig", err)
	}
}

func TestValidate_judgesOverridesAgainstTheTable(t *testing.T) {
	t.Parallel()

	types := rules.Types{"pubonly": table(map[string]string{"secret_scanning": "public"})}
	private := fixture("alpha", "pubonly")
	private.Overrides = map[string]string{"secret_scanning": rules.OverrideNotRequired}
	open := public(fixture("bravo", "pubonly"))
	open.Overrides = map[string]string{"secret_scanning": rules.OverrideNotRequired}

	err := rules.Validate(&audit.Snapshot{Types: types, Repos: []audit.Repo{private, open}})
	if err == nil || !strings.Contains(err.Error(), "alpha: override secret_scanning") {
		t.Fatalf("Validate() error = %v, want alpha's dead override", err)
	}
	if strings.Contains(err.Error(), "bravo") {
		t.Errorf("Validate() error = %q, want bravo's live override accepted", err)
	}
}

func TestValidate_rejectsAnUnusableTypesTable(t *testing.T) {
	t.Parallel()

	err := rules.Validate(&audit.Snapshot{Types: rules.Types{"strict": {}}})
	if !errors.Is(err, rules.ErrConfig) {
		t.Errorf("Validate() error = %v, want it to wrap ErrConfig", err)
	}
}
