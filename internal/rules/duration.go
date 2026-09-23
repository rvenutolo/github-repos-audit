package rules

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// thresholdPattern is the repos.toml duration grammar. It is deliberately
// narrower than Renovate's: a threshold is written once, by hand, and one
// spelling per duration keeps dead-override detection and diffs honest. The
// schema's release_age_threshold pattern must match it.
var thresholdPattern = regexp.MustCompile(`^([1-9][0-9]*) (minute|hour|day|week)s?$`)

//nolint:gochecknoglobals // immutable lookup table
var thresholdUnits = map[string]time.Duration{
	"minute": time.Minute,
	"hour":   time.Hour,
	"day":    24 * time.Hour,
	"week":   7 * 24 * time.Hour,
}

// ParseThreshold parses a repos.toml minimum-release-age threshold such as
// "7 days". It reports false for anything outside the strict grammar and for
// a count too large for a time.Duration, which would otherwise wrap negative.
func ParseThreshold(s string) (time.Duration, bool) {
	m := thresholdPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	unit := thresholdUnits[m[2]]
	if n > int64(math.MaxInt64/unit) {
		return 0, false
	}
	return time.Duration(n) * unit, true
}

// The Renovate grammar below is a port of Renovate's lib/util/pretty-time.ts
// toMs and the ms library it calls, so that any value Renovate honours is read
// here too: a stricter reader would turn a perfectly good "1 year" into a gap.
var (
	// renovatePart is Renovate's split regex. Like the JavaScript original it
	// is case-sensitive, and the text between matches is kept as parts too.
	renovatePart = regexp.MustCompile(`.*?[a-z]+`)
	// renovateMonths is Renovate's own month rule, applied before ms: a
	// month is exactly 30 days.
	renovateMonths = regexp.MustCompile(`^(\d+)\s*(?:months?|M)$`)
	// msPattern is the ms library's grammar, case-insensitive. Its "mo"
	// month is a twelfth of a year (30.4375 days) and is reached only by a
	// form the month rule above does not take, such as "1.5 months".
	msPattern = regexp.MustCompile(`(?i)^(-?\d*\.?\d+) *(milliseconds?|msecs?|ms|seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h|days?|d|weeks?|w|months?|mo|years?|yrs?|y)?$`)
)

// msMaxLength is the longest string ms will parse; a longer one is NaN.
const msMaxLength = 100

// msYear is ms's year, 365.25 days, in milliseconds.
const msYear = 365.25 * 24 * 60 * 60 * 1000

// ParseRenovateDuration parses a minimumReleaseAge value the way Renovate
// does. It reports false for anything Renovate would reject, and also for a
// negative total or one too large for a time.Duration: neither is a
// quarantine this report should credit.
func ParseRenovateDuration(s string) (time.Duration, bool) {
	parts := splitRenovate(s)
	if len(parts) == 0 {
		return 0, false
	}
	total := 0.0
	for _, p := range parts {
		ms, ok := parseMSPart(p)
		if !ok {
			return 0, false
		}
		total += ms
	}
	if total < 0 || total > float64(math.MaxInt64)/float64(time.Millisecond) {
		return 0, false
	}
	return time.Duration(total * float64(time.Millisecond)), true
}

// splitRenovate mirrors String.prototype.split with a capturing regex: the
// matches and the text between them all become parts, trimmed, empties
// dropped.
func splitRenovate(s string) []string {
	matches := renovatePart.FindAllStringIndex(s, -1)
	parts := make([]string, 0, 2*len(matches)+1)
	last := 0
	for _, loc := range matches {
		parts = append(parts, s[last:loc[0]], s[loc[0]:loc[1]])
		last = loc[1]
	}
	parts = append(parts, s[last:])
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseMSPart parses one part to milliseconds.
func parseMSPart(p string) (float64, bool) {
	if m := renovateMonths.FindStringSubmatch(p); m != nil {
		n, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0, false
		}
		return n * 30 * 24 * 60 * 60 * 1000, true
	}
	if len(p) > msMaxLength {
		return 0, false
	}
	m := msPattern.FindStringSubmatch(p)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return n * msUnit(strings.ToLower(m[2])), true
}

// msUnit is ms's unit table, in milliseconds. No unit means milliseconds.
func msUnit(u string) float64 {
	const (
		second = 1000.0
		minute = 60 * second
		hour   = 60 * minute
		day    = 24 * hour
	)
	switch u {
	case "years", "year", "yrs", "yr", "y":
		return msYear
	case "months", "month", "mo":
		return msYear / 12
	case "weeks", "week", "w":
		return 7 * day
	case "days", "day", "d":
		return day
	case "hours", "hour", "hrs", "hr", "h":
		return hour
	case "minutes", "minute", "mins", "min", "m":
		return minute
	case "seconds", "second", "secs", "sec", "s":
		return second
	default: // milliseconds, msecs, ms, or none
		return 1
	}
}
