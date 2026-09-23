package rules_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

const day = 24 * time.Hour

func TestParseThreshold(t *testing.T) {
	t.Parallel()

	valid := map[string]time.Duration{
		"7 days":     7 * day,
		"1 day":      day,
		"1 days":     day, // plural is accepted regardless of the number
		"2 day":      2 * day,
		"1 week":     7 * day,
		"48 hours":   48 * time.Hour,
		"30 minutes": 30 * time.Minute,
	}
	for in, want := range valid {
		got, ok := rules.ParseThreshold(in)
		if !ok || got != want {
			t.Errorf("ParseThreshold(%q) = %v, %t; want %v, true", in, got, ok, want)
		}
	}
	for _, in := range []string{
		"", "7", "0 days", "07 days", "-1 days", "7days", "7  days", " 7 days", "7 days ",
		"7 Days", "7d", "1.5 days", "1 month", "1 year", "1 week 2 days", "required", "info",
		"100000000 weeks", // overflows time.Duration: must be rejected, not wrapped
	} {
		if got, ok := rules.ParseThreshold(in); ok {
			t.Errorf("ParseThreshold(%q) = %v, true; want false", in, got)
		}
	}
}

// renovateToMs is Renovate's own lib/util/pretty-time.spec.ts toMs table,
// copied 2026-09-22. -1 means Renovate answers null.
var renovateToMs = []struct {
	in string
	ms int64
}{
	{"1h", 3600000},
	{" 1 h ", 3600000},
	{"1 h", 3600000},
	{"1 hour", 3600000},
	{"1hour", 3600000},
	{"1h 1m", 3660000},
	{"1hour 1minute", 3660000},
	{"1 hour 1 minute", 3660000},
	{"1h 1m 1s", 3661000},
	{"1h 1 m 1s", 3661000},
	{"1hour 1 min 1s", 3661000},
	{"1h 1m 1s 1ms", 3661001},
	{"1d2h3m", 93780000},
	{"1 day", 86400000},
	{"3 days", 259200000},
	{"1 week", 604800000},
	{"1 month", 2592000000},
	{"1 M", 2592000000},
	{"2 months", 5184000000},
	{"1month", 2592000000},
	{"1M", 2592000000},
	{"2months", 5184000000},
	{"1 year", 31557600000},
	{strings.Repeat("0", 100), 0},
	{strings.Repeat("0", 101), -1},
	{"1 whatever", -1},
	{"whatever", -1},
	{"", -1},
	{" ", -1},
	{"  \t\n   ", -1},
	{"minute", -1},
	{"m", -1},
	{"hour", -1},
	{"h", -1},
}

func TestParseRenovateDuration_matchesRenovate(t *testing.T) {
	t.Parallel()

	for _, tt := range renovateToMs {
		got, ok := rules.ParseRenovateDuration(tt.in)
		if tt.ms < 0 {
			if ok {
				t.Errorf("ParseRenovateDuration(%q) = %v, true; want false (Renovate: null)", tt.in, got)
			}
			continue
		}
		if want := time.Duration(tt.ms) * time.Millisecond; !ok || got != want {
			t.Errorf("ParseRenovateDuration(%q) = %v, %t; want %v, true", tt.in, got, ok, want)
		}
	}
}

func TestParseRenovateDuration_beyondRenovatesTable(t *testing.T) {
	t.Parallel()

	valid := map[string]time.Duration{
		"7d":            7 * day,
		"1.5 days":      36 * time.Hour,
		"1 week 2 days": 9 * day,
		"7 DAYS":        7 * day, // ms is case-insensitive
		"3 months":      90 * day,
	}
	for in, want := range valid {
		got, ok := rules.ParseRenovateDuration(in)
		if !ok || got != want {
			t.Errorf("ParseRenovateDuration(%q) = %v, %t; want %v, true", in, got, ok, want)
		}
	}
	for _, in := range []string{
		"-7 days",           // a negative age is not a quarantine
		"99999999999 years", // overflows time.Duration
		// Under MaxInt64 ms-to-ns before rounding, but the product rounds
		// up to 2^63, which no time.Duration holds.
		"9223372036854.7758 ms",
		"7 dayz", "{{arg0}}", "null",
		"1.5 months", "2 mo", "1 MONTHS", // ms 2.1.3 (Renovate's pin) has no months/mo unit
	} {
		if got, ok := rules.ParseRenovateDuration(in); ok {
			t.Errorf("ParseRenovateDuration(%q) = %v, true; want false", in, got)
		}
	}
}

// TestParseThreshold_isASubsetOfRenovate: a threshold written in repos.toml
// means exactly what the same words mean in a Renovate config.
func TestParseThreshold_isASubsetOfRenovate(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"1 minute", "5 minutes", "1 hour", "48 hours", "1 day", "7 days", "1 week", "3 weeks"} {
		strict, sok := rules.ParseThreshold(in)
		lenient, lenientOK := rules.ParseRenovateDuration(in)
		if !sok || !lenientOK || strict != lenient {
			t.Errorf("%q: ParseThreshold = %v, %t; ParseRenovateDuration = %v, %t; want equal and both true",
				in, strict, sok, lenient, lenientOK)
		}
	}
}
