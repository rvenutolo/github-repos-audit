package rules

import "testing"

// expectation is unexported and nothing exported carries one — a cell holds
// the Verdict it resolved to — so its String method can only be reached from
// inside the package.

func TestExpectation_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		e    expectation
		want string
	}{
		{name: "not applicable", e: expNA, want: "not_required"},
		{name: "always", e: expAlways, want: "required"},
		{name: "public", e: expPublic, want: "public"},
		{name: "public and published", e: expPublicPublished, want: "public_published"},
		{name: "informational", e: expInfo, want: "info"},
		{name: "out of range", e: expectation(99), want: "expectation(99)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.e.String(); got != tc.want {
				t.Errorf("expectation(%d).String() = %q, want %q", int(tc.e), got, tc.want)
			}
		})
	}
}

func TestParseExpectation(t *testing.T) {
	t.Parallel()

	for _, e := range []expectation{expNA, expAlways, expPublic, expPublicPublished, expInfo} {
		got, ok := parseExpectation(e.String())
		if !ok || got != e {
			t.Errorf("parseExpectation(%q) = %v, %t; want %v, true", e.String(), got, ok, e)
		}
	}
	for _, word := range []string{"", "yes", "Required", "blocked", "expectation(99)"} {
		if _, ok := parseExpectation(word); ok {
			t.Errorf("parseExpectation(%q) = _, true; want false", word)
		}
	}
}

// TestGapLabels_coverEveryJudgedCheck: a check absent from gapLabels never
// reaches the worklist, which is right for a value-only check and a silent
// omission for anything else. Community files and direct push report through
// their own paths in collectGaps.
func TestGapLabels_coverEveryJudgedCheck(t *testing.T) {
	t.Parallel()

	for _, c := range Checks() {
		_, labelled := gapLabels[c]
		switch {
		case c == CheckCommunityFiles, c == CheckDirectPush:
			if labelled {
				t.Errorf("%s has a gapLabels entry, want it reported through its own path", c)
			}
		case c.ValueOnly():
			if labelled {
				t.Errorf("value-only check %s has a gapLabels entry, want none", c)
			}
		default:
			if !labelled {
				t.Errorf("judged check %s has no gapLabels entry, so it can never reach the worklist", c)
			}
		}
	}
}
