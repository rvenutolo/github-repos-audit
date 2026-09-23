package rules_test

import (
	"fmt"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

func TestChecks_areAllDistinctAndRoundTrip(t *testing.T) {
	t.Parallel()

	seen := make(map[string]rules.Check)
	for _, c := range rules.Checks() {
		name := c.String()
		if name == "" {
			t.Errorf("check %d has an empty name", int(c))
			continue
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("checks %d and %d share the name %q", int(prev), int(c), name)
			continue
		}
		seen[name] = c

		got, ok := rules.ParseCheck(name)
		if !ok {
			t.Errorf("ParseCheck(%q) = _, false; want the check back", name)
			continue
		}
		if got != c {
			t.Errorf("ParseCheck(%q) = %d, want %d", name, int(got), int(c))
		}
	}
}

func TestChecks_count(t *testing.T) {
	t.Parallel()

	// 26 report rows, the git identity the latest, plus direct push, the one
	// declared setting a type decides. A change here is a change to the
	// report, to the override vocabulary, and to every [types.*] table at
	// once.
	const want = 27
	if got := len(rules.Checks()); got != want {
		t.Errorf("len(Checks()) = %d, want %d", got, want)
	}
}

func TestParseCheck_rejectsAnUnknownName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "near miss", in: "readmes"},
		{name: "wrong separator", in: "secret-scanning"},
		{name: "out of range rendering", in: "Check(3)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, ok := rules.ParseCheck(tc.in); ok {
				t.Errorf("ParseCheck(%q) = _, true; want false", tc.in)
			}
		})
	}
}

// TestCheck_ValueOnly pins the four checks that carry a value and no pass or
// fail. Requiring one of them could only ever render a cross that no worklist
// entry explains, so config refuses it and a type may not ask for it.
func TestCheck_ValueOnly(t *testing.T) {
	t.Parallel()

	want := map[rules.Check]bool{
		rules.CheckLastReleaseAge: true,
		rules.CheckLastPush:       true,
		rules.CheckOpenPRs:        true,
		rules.CheckBranches:       true,
	}
	for _, c := range rules.Checks() {
		if got := c.ValueOnly(); got != want[c] {
			t.Errorf("%s.ValueOnly() = %t, want %t", c, got, want[c])
		}
	}
}

// TestCheck_Threshold pins the one check judged against a duration its type
// declares. It is not value-only: it has a pass and a fail, so a type may
// expect it.
func TestCheck_Threshold(t *testing.T) {
	t.Parallel()

	for _, c := range rules.Checks() {
		if got, want := c.Threshold(), c == rules.CheckRenovateMinReleaseAge; got != want {
			t.Errorf("%s.Threshold() = %t, want %t", c, got, want)
		}
	}
	if rules.CheckRenovateMinReleaseAge.ValueOnly() {
		t.Errorf("%s.ValueOnly() = true, want false", rules.CheckRenovateMinReleaseAge)
	}
}

func TestCheck_StringOnAnOutOfRangeValue(t *testing.T) {
	t.Parallel()

	if got, want := rules.Check(-1).String(), "Check(-1)"; got != want {
		t.Errorf("Check(-1).String() = %q, want %q", got, want)
	}
	if got, want := rules.Check(9999).String(), "Check(9999)"; got != want {
		t.Errorf("Check(9999).String() = %q, want %q", got, want)
	}

	// The FIRST out-of-range value, which -1 and 9999 both step over. String's
	// upper guard is `int(c) >= len(checkNames)`, and relaxing it to `>` leaves
	// exactly this one value indexing off the end of the table: a panic, not a
	// wrong string. Surfaced as a surviving CONDITIONALS_BOUNDARY mutant at
	// check.go:82 by `just mutate`.
	n := len(rules.Checks())
	if got, want := rules.Check(n).String(), fmt.Sprintf("Check(%d)", n); got != want {
		t.Errorf("Check(%d).String() = %q, want %q", n, got, want)
	}
}
