package rules_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// The identities the git_identity tests are built from: the fixture
// vocabulary internal/fixturevocab allows, never a real person.
var (
	idCanonical  = audit.Identity{Name: "Pat Example", Email: "pat@example.com"}
	idUpperEmail = audit.Identity{Name: "Pat Example", Email: "Pat@Example.com"}
	idOtherEmail = audit.Identity{Name: "Pat Example", Email: "pat@example.org"}
	idLongName   = audit.Identity{Name: "Patrick Example", Email: "patrick@example.org"}
	idLowerName  = audit.Identity{Name: "Pat example", Email: "pat@example.com"}
	idStranger   = audit.Identity{Name: "Robin Other", Email: "robin@example.org"}
	idBot        = audit.Identity{Name: "renovate[bot]", Email: "29139614+renovate[bot]@users.noreply.github.com"}
	idGitHub     = audit.Identity{Name: "GitHub", Email: "noreply@github.com"}

	idNoreply          = audit.Identity{Name: "Pat Example", Email: "12345+pat@users.noreply.github.com"}
	idUpperNoreply     = audit.Identity{Name: "Pat Example", Email: "12345+Pat@users.noreply.github.com"}
	idLowerNoreplyName = audit.Identity{Name: "Pat example", Email: "12345+pat@users.noreply.github.com"}
)

// withIdentities sets a fixture's collected identities, sorted the way the
// collector records them: by name, then email, byte order.
func withIdentities(r audit.Repo, ids ...audit.Identity) audit.Repo {
	sorted := slices.Clone(ids)
	slices.SortFunc(sorted, func(a, b audit.Identity) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Email, b.Email)
	})
	r.Identities = sorted
	return r
}

// identityTypes is standard-types.toml with git_identity at word for tools.
func identityTypes(word string) rules.Types {
	types := make(rules.Types, len(standardTypes()))
	for name, entries := range standardTypes() {
		// standardTypes is shared read-only, so each table is copied.
		types[name] = maps.Clone(entries)
	}
	types["tools"][rules.CheckGitIdentity.String()] = word
	return types
}

// TestEvaluate_gitIdentity judges a tools repository, which standard-types.toml
// holds to git_identity = "required", against the fixture standard.
func TestEvaluate_gitIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		repo      audit.Repo
		match     string // empty: the fixture standard's
		want      rules.Verdict
		wantValue string
	}{
		{"all canonical passes", withIdentities(fixture("a", "tools"), idCanonical, idBot), "", rules.Pass, ""},
		{"a case-only email difference is the same identity", withIdentities(fixture("a", "tools"), idCanonical, idUpperEmail), "", rules.Pass, ""},
		{"consistently wrong fails", withIdentities(fixture("a", "tools"), idOtherEmail), "", rules.Fail, "1 wrong"},
		{"mixed fails", withIdentities(fixture("a", "tools"), idCanonical, idLongName), "", rules.Fail, "1 wrong"},
		{"two wrong", withIdentities(fixture("a", "tools"), idOtherEmail, idLongName), "", rules.Fail, "2 wrong"},
		{"a name differing only in case is a different identity", withIdentities(fixture("a", "tools"), idLowerName), "(?i) example <", rules.Fail, "1 wrong"},
		{"only other people is n/a", withIdentities(fixture("a", "tools"), idStranger, idGitHub), "", rules.NA, ""},
		{"an empty repository is n/a", func() audit.Repo { r := fixture("a", "tools"); r.Empty = true; return r }(), "", rules.NA, ""},
		{"canonical and accepted pass", withIdentities(fixture("a", "tools"), idCanonical, idNoreply), "", rules.Pass, ""},
		{"only accepted passes", withIdentities(fixture("a", "tools"), idNoreply), "", rules.Pass, ""},
		{"an accepted address differing only in case is accepted", withIdentities(fixture("a", "tools"), idUpperNoreply), "", rules.Pass, ""},
		{"an accepted name differing in case is wrong", withIdentities(fixture("a", "tools"), idLowerNoreplyName), "(?i) example <", rules.Fail, "1 wrong"},
		{"accepted beside a wrong one fails by the wrong one", withIdentities(fixture("a", "tools"), idNoreply, idOtherEmail), "", rules.Fail, "1 wrong"},
		{"canonical, accepted and wrong count only the wrong one", withIdentities(fixture("a", "tools"), idCanonical, idNoreply, idLongName), "", rules.Fail, "1 wrong"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			snap := &audit.Snapshot{Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{tt.repo}}
			if tt.match != "" {
				snap.Identity.Match = tt.match
			}
			rep, err := rules.Evaluate(snap)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			got := rep.Repos[0].Cell(rules.CheckGitIdentity)
			if got.Verdict != tt.want || got.Value != tt.wantValue {
				t.Errorf("cell = %v %q; want %v %q", got.Verdict, got.Value, tt.want, tt.wantValue)
			}
			// The tick or cross sits beside the count, so "✗ 1 wrong" reads as
			// a gap rather than as a plain fact.
			if tt.want != rules.NA && !got.Mark {
				t.Errorf("cell Mark = false, want true")
			}
			if got.Scalar {
				t.Errorf("cell Scalar = true, want false: an n/a identity cell carries no fact to show")
			}
		})
	}
}

func TestEvaluate_gitIdentityOverrideNotRequiredStillLists(t *testing.T) {
	t.Parallel()

	r := withIdentities(fixture("alpha", "tools"), idOtherEmail)
	r.Overrides = map[string]string{rules.CheckGitIdentity.String(): rules.OverrideNotRequired}
	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{r}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	got := rep.Repos[0].Cell(rules.CheckGitIdentity)
	if got.Verdict != rules.NA || !got.Overridden {
		t.Errorf("cell = %v overridden=%t; want n/a overridden=true", got.Verdict, got.Overridden)
	}
	// The listing is the working list for a history rewrite: excusing the
	// verdict does not make the wrong identity disappear from the history.
	if len(rep.Identities) != 1 || rep.Identities[0].Repo != "alpha" {
		t.Errorf("Identities = %+v, want one line for alpha", rep.Identities)
	}
}

func TestEvaluate_gitIdentityInfo(t *testing.T) {
	t.Parallel()

	r := withIdentities(fixture("alpha", "tools"), idOtherEmail)
	rep, err := rules.Evaluate(&audit.Snapshot{Types: identityTypes("info"), Identity: fixtureIdentity(), Repos: []audit.Repo{r}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	if got := rep.Repos[0].Cell(rules.CheckGitIdentity); got.Verdict != rules.Info || got.Value != "1 wrong" {
		t.Errorf("cell = %v %q; want info %q", got.Verdict, got.Value, "1 wrong")
	}
	if len(rep.Gaps) != 0 {
		t.Errorf("Gaps = %+v, want none: an informational row is never a gap", rep.Gaps)
	}
}

func TestEvaluate_gitIdentityWithoutAStandard(t *testing.T) {
	t.Parallel()

	r := withIdentities(fixture("alpha", "tools"), idOtherEmail)

	_, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Repos: []audit.Repo{r}})
	if err == nil || !strings.Contains(err.Error(), "git_identity") {
		t.Errorf("Evaluate(judged, no standard) error = %v, want one naming git_identity", err)
	}

	rep, err := rules.Evaluate(&audit.Snapshot{Types: identityTypes(rules.OverrideNotRequired), Repos: []audit.Repo{r}})
	if err != nil {
		t.Fatalf("Evaluate(not judged, no standard) error = %v, want nil", err)
	}
	if got := rep.Repos[0].Cell(rules.CheckGitIdentity); got.Verdict != rules.NA {
		t.Errorf("cell = %v, want n/a", got.Verdict)
	}
	if len(rep.Identities) != 0 {
		t.Errorf("Identities = %+v, want none without a standard", rep.Identities)
	}
}

func TestEvaluate_gitIdentityRejectsAnUnusableStandard(t *testing.T) {
	t.Parallel()

	snap := &audit.Snapshot{
		Types:    standardTypes(),
		Identity: audit.IdentityStandard{Canonical: "Pat Example", Match: " Example <"},
		Repos:    []audit.Repo{fixture("alpha", "tools")},
	}
	if _, err := rules.Evaluate(snap); err == nil || !strings.Contains(err.Error(), "identity.canonical") {
		t.Errorf("Evaluate(malformed standard) error = %v, want one naming identity.canonical", err)
	}

	onlyAccepted := &audit.Snapshot{
		Types:    standardTypes(),
		Identity: audit.IdentityStandard{Accepted: []string{"Pat Example <12345+pat@users.noreply.github.com>"}},
		Repos:    []audit.Repo{fixture("alpha", "tools")},
	}
	if _, err := rules.Evaluate(onlyAccepted); err == nil || !strings.Contains(err.Error(), "identity.canonical") {
		t.Errorf("Evaluate(only accepted) error = %v, want one naming identity.canonical", err)
	}
}

func TestEvaluate_gitIdentityReachesTheWorklist(t *testing.T) {
	t.Parallel()

	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{
		withIdentities(fixture("fine", "tools"), idCanonical),
		withIdentities(fixture("wrong", "tools"), idOtherEmail),
		withIdentities(fixture("Mixed", "tools"), idCanonical, idLongName),
	}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	var gap *rules.Gap
	for i := range rep.Gaps {
		if rep.Gaps[i].Check == rules.CheckGitIdentity {
			gap = &rep.Gaps[i]
		}
	}
	if gap == nil {
		t.Fatalf("Gaps = %+v, want a git_identity gap", rep.Gaps)
	}
	if gap.Label != "Commits under a non-canonical git identity" || !slices.Equal(gap.Repos, []string{"Mixed", "wrong"}) {
		t.Errorf("git_identity gap = %+v, want label %q and repos [Mixed wrong]", *gap, "Commits under a non-canonical git identity")
	}
}

func TestEvaluate_identityLines(t *testing.T) {
	t.Parallel()

	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{
		withIdentities(fixture("delta", "tools"), idLongName, idCanonical),
		withIdentities(fixture("bravo", "tools"), idStranger),
		withIdentities(fixture("alpha", "tools"), idCanonical, idUpperEmail, idBot),
		withIdentities(fixture("charlie", "tools"), idOtherEmail),
	}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	want := []rules.IdentityLine{
		{
			// The case variant collapses into the canonical, and is shown in the
			// canonical's own spelling even though "Pat@" sorts before "pat@".
			Repo:       "alpha",
			Identities: []rules.IdentityEntry{{Name: "Pat Example", Email: "pat@example.com", Canonical: true}},
			Clean:      true,
		},
		{
			Repo:       "charlie",
			Identities: []rules.IdentityEntry{{Name: "Pat Example", Email: "pat@example.org"}},
		},
		{
			Repo: "delta",
			Identities: []rules.IdentityEntry{
				{Name: "Pat Example", Email: "pat@example.com", Canonical: true},
				{Name: "Patrick Example", Email: "patrick@example.org"},
			},
			Mixed: true,
		},
	}
	if diff := cmp.Diff(want, rep.Identities); diff != "" {
		t.Errorf("Identities mismatch (-want +got):\n%s", diff)
	}
}

// TestIdentityLines_nonCanonicalOrder: after the canonical, the rest are by
// name then email, and of two spellings of one non-canonical identity the
// first in byte order is the one shown.
func TestIdentityLines_nonCanonicalOrder(t *testing.T) {
	t.Parallel()

	upperOther := audit.Identity{Name: "Pat Example", Email: "Pat@example.org"}
	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{
		withIdentities(fixture("alpha", "tools"), idLongName, idOtherEmail, upperOther, idCanonical),
	}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	want := []rules.IdentityLine{{
		Repo: "alpha",
		Identities: []rules.IdentityEntry{
			{Name: "Pat Example", Email: "pat@example.com", Canonical: true},
			{Name: "Pat Example", Email: "Pat@example.org"},
			{Name: "Patrick Example", Email: "patrick@example.org"},
		},
		Mixed: true,
	}}
	if diff := cmp.Diff(want, rep.Identities); diff != "" {
		t.Errorf("Identities mismatch (-want +got):\n%s", diff)
	}
	if got := rep.Repos[0].Cell(rules.CheckGitIdentity); got.Value != "2 wrong" {
		t.Errorf("cell value = %q, want %q: two spellings of one address are one identity", got.Value, "2 wrong")
	}
}

func TestIdentityEntry_String(t *testing.T) {
	t.Parallel()

	e := rules.IdentityEntry{Name: "Pat Example", Email: "pat@example.com", Canonical: true}
	if got, want := e.String(), "Pat Example <pat@example.com>"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// TestIdentityLines_accepted: accepted identities are listed, tagged, shown in
// the spelling the standard declares, and never make a line mixed or unclean.
func TestIdentityLines_accepted(t *testing.T) {
	t.Parallel()

	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{
		withIdentities(fixture("alpha", "tools"), idCanonical, idUpperNoreply),
		withIdentities(fixture("bravo", "tools"), idNoreply),
		withIdentities(fixture("charlie", "tools"), idNoreply, idOtherEmail),
		withIdentities(fixture("delta", "tools"), idLongName, idNoreply, idCanonical),
	}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	canonical := rules.IdentityEntry{Name: "Pat Example", Email: "pat@example.com", Canonical: true}
	noreply := rules.IdentityEntry{Name: "Pat Example", Email: "12345+pat@users.noreply.github.com", Accepted: true}
	want := []rules.IdentityLine{
		// The commit spelled the address "12345+Pat@"; the line shows the
		// standard's own spelling, as it does for the canonical identity.
		{Repo: "alpha", Identities: []rules.IdentityEntry{canonical, noreply}, Clean: true},
		{Repo: "bravo", Identities: []rules.IdentityEntry{noreply}, Clean: true},
		// One wrong identity beside an accepted one has not switched between
		// two identities of the owner's that matter: not canonical, not mixed.
		{Repo: "charlie", Identities: []rules.IdentityEntry{noreply, {Name: "Pat Example", Email: "pat@example.org"}}},
		{
			Repo:       "delta",
			Identities: []rules.IdentityEntry{canonical, noreply, {Name: "Patrick Example", Email: "patrick@example.org"}},
			Mixed:      true,
		},
	}
	if diff := cmp.Diff(want, rep.Identities); diff != "" {
		t.Errorf("Identities mismatch (-want +got):\n%s", diff)
	}
}

// TestIdentityLines_acceptedBeforeWrong: accepted identities come before wrong
// ones even when a wrong one sorts first in byte order.
func TestIdentityLines_acceptedBeforeWrong(t *testing.T) {
	t.Parallel()

	std := fixtureIdentity()
	std.Accepted = []string{"Patrick Example <patrick@example.org>"}
	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Identity: std, Repos: []audit.Repo{
		withIdentities(fixture("alpha", "tools"), idOtherEmail, idLongName),
	}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	want := []rules.IdentityLine{{
		Repo: "alpha",
		Identities: []rules.IdentityEntry{
			{Name: "Patrick Example", Email: "patrick@example.org", Accepted: true},
			{Name: "Pat Example", Email: "pat@example.org"},
		},
	}}
	if diff := cmp.Diff(want, rep.Identities); diff != "" {
		t.Errorf("Identities mismatch (-want +got):\n%s", diff)
	}
}
