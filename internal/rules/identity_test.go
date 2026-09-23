package rules_test

import (
	"strings"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

func TestParseIdentity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want audit.Identity
		ok   bool
	}{
		{"Pat Example <pat@example.com>", audit.Identity{Name: "Pat Example", Email: "pat@example.com"}, true},
		{"  Pat Example   <pat@example.com>  ", audit.Identity{Name: "Pat Example", Email: "pat@example.com"}, true},
		{
			"renovate[bot] <29139614+renovate[bot]@users.noreply.github.com>",
			audit.Identity{Name: "renovate[bot]", Email: "29139614+renovate[bot]@users.noreply.github.com"},
			true,
		},
		{"Pat Example", audit.Identity{}, false},
		{"<pat@example.com>", audit.Identity{}, false},
		{"Pat Example <>", audit.Identity{}, false},
		{"Pat Example <pat @example.com>", audit.Identity{}, false},
		{"Pat Example <pat@example.com> trailing", audit.Identity{}, false},
		{"", audit.Identity{}, false},
	}
	for _, tt := range tests {
		got, ok := rules.ParseIdentity(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("ParseIdentity(%q) = %+v, %t; want %+v, %t", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestFormatIdentity_roundTrips(t *testing.T) {
	t.Parallel()
	id := audit.Identity{Name: "Pat Example", Email: "pat@example.com"}
	if got := rules.FormatIdentity(id); got != "Pat Example <pat@example.com>" {
		t.Fatalf("FormatIdentity = %q", got)
	}
	if back, ok := rules.ParseIdentity(rules.FormatIdentity(id)); !ok || back != id {
		t.Fatalf("round trip = %+v, %t", back, ok)
	}
}

func TestIdentityProblems(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		std  audit.IdentityStandard
		want []string // substrings, one per expected problem, in order
	}{
		{"valid", audit.IdentityStandard{Canonical: "Pat Example <pat@example.com>", Match: " Example <"}, nil},
		{"contains-style word", audit.IdentityStandard{Canonical: "Pat Example <pat@example.com>", Match: "(?i)example"}, nil},
		{
			"malformed canonical",
			audit.IdentityStandard{Canonical: "Pat Example", Match: "x"},
			[]string{`identity.canonical = "Pat Example" (want "Name <email>")`},
		},
		{
			"missing canonical",
			audit.IdentityStandard{Match: "x"},
			[]string{`identity.canonical = "" (want "Name <email>")`},
		},
		{
			"missing match",
			audit.IdentityStandard{Canonical: "Pat Example <pat@example.com>"},
			[]string{"identity.match: missing"},
		},
		{
			"bad regex",
			audit.IdentityStandard{Canonical: "Pat Example <pat@example.com>", Match: "(unclosed"},
			[]string{"identity.match: error parsing regexp"},
		},
		{
			"canonical not matched",
			audit.IdentityStandard{Canonical: "Pat Example <pat@example.com>", Match: "Robin"},
			[]string{"identity.canonical does not match identity.match"},
		},
		{
			"both empty",
			audit.IdentityStandard{},
			[]string{`identity.canonical = ""`, "identity.match: missing"},
		},
		{
			"accepted identities",
			audit.IdentityStandard{
				Canonical: "Pat Example <pat@example.com>", Match: " Example <",
				Accepted: []string{"Pat Example <12345+pat@users.noreply.github.com>"},
			},
			nil,
		},
		{
			"an empty accepted list",
			audit.IdentityStandard{Canonical: "Pat Example <pat@example.com>", Match: " Example <", Accepted: []string{}},
			nil,
		},
		{
			"a malformed accepted identity",
			audit.IdentityStandard{Canonical: "Pat Example <pat@example.com>", Match: " Example <", Accepted: []string{"Pat Example"}},
			[]string{`identity.accepted[0] = "Pat Example" (want "Name <email>")`},
		},
		{
			"an accepted identity match does not catch",
			audit.IdentityStandard{Canonical: "Pat Example <pat@example.com>", Match: " Example <", Accepted: []string{"Robin Other <robin@example.org>"}},
			[]string{"identity.accepted[0] does not match identity.match"},
		},
		{
			// Case in the address is not a different identity, so this is the
			// canonical one restated.
			"an accepted identity that is the canonical one",
			audit.IdentityStandard{Canonical: "Pat Example <pat@example.com>", Match: " Example <", Accepted: []string{"Pat Example <Pat@Example.com>"}},
			[]string{"identity.accepted[0] is identity.canonical"},
		},
		{
			"an accepted identity repeated in another case",
			audit.IdentityStandard{
				Canonical: "Pat Example <pat@example.com>", Match: " Example <",
				Accepted: []string{"Pat Example <12345+pat@users.noreply.github.com>", "Pat Example <patrick@example.org>", "Pat Example <12345+PAT@users.noreply.github.com>"},
			},
			[]string{"identity.accepted[2] repeats identity.accepted[0]"},
		},
		{
			"every accepted problem at once",
			audit.IdentityStandard{
				Canonical: "Pat Example <pat@example.com>", Match: " Example <",
				Accepted: []string{"x", "Robin Other <robin@example.org>", "Pat Example <pat@example.com>"},
			},
			[]string{
				`identity.accepted[0] = "x" (want "Name <email>")`,
				"identity.accepted[1] does not match identity.match",
				"identity.accepted[2] is identity.canonical",
			},
		},
		{
			// Nothing to compare an entry with: only the canonical problem.
			"a malformed canonical skips the canonical comparison",
			audit.IdentityStandard{Canonical: "Pat Example", Match: " Example <", Accepted: []string{"Pat Example <pat@example.com>"}},
			[]string{`identity.canonical = "Pat Example" (want "Name <email>")`},
		},
		{
			"a bad expression skips the match comparison",
			audit.IdentityStandard{Canonical: "Pat Example <pat@example.com>", Match: "(unclosed", Accepted: []string{"Robin Other <robin@example.org>"}},
			[]string{"identity.match: error parsing regexp"},
		},
		{
			// A malformed entry is not an identity, so two of them repeat
			// nothing.
			"malformed entries are never repeats",
			audit.IdentityStandard{Canonical: "Pat Example <pat@example.com>", Match: " Example <", Accepted: []string{"bad", "bad"}},
			[]string{`identity.accepted[0] = "bad"`, `identity.accepted[1] = "bad"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := rules.IdentityProblems(tt.std)
			if len(got) != len(tt.want) {
				t.Fatalf("IdentityProblems = %v, want %d problems", got, len(tt.want))
			}
			for i, sub := range tt.want {
				if !strings.Contains(got[i].Error(), sub) {
					t.Errorf("problem %d = %q, want it to contain %q", i, got[i], sub)
				}
			}
		})
	}
}
