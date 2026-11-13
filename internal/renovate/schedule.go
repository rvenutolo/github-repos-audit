package renovate

import (
	"math/bits"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A schedule is the list of strings Renovate's "schedule" option holds. This
// file reads those strings the way Renovate does and answers one question:
// does the schedule confine Renovate to a few short windows a week or less
// often? The contract, which judge enforces, is
//
//   - every contiguous run of allowed hours is at most maxBlockHours long, and
//   - consecutive runs begin at least minBlockSpacing hours apart.
//
// Renovate's own docs describe the syntax:
// https://docs.renovatebot.com/configuration-options/#schedule
// and the source that validates it is
// https://github.com/renovatebot/renovate/blob/main/lib/workers/repository/update/branch/schedule.ts
//
// The judge works on hours over a fixed reference period instead of on the
// text, so a cron expression and its later.js spelling reach the same verdict,
// and so combinations of entries (any entry matching allows the hour) need no
// special cases. Timezones are ignored: a shift moves every window by the same
// amount and changes neither run lengths nor spacing.

const (
	// maxBlockHours is the longest run of allowed hours still counted as one
	// batch window: two whole days, which is what a "weekends" schedule spans.
	maxBlockHours = 48
	// minBlockSpacing is the shortest distance between the starts of two
	// windows: one week.
	minBlockSpacing = 168

	// periodHours is the length of the reference period, in hours. The period
	// begins on a Monday (so weekday arithmetic starts aligned) and runs 735
	// days: it holds a leap February (so "day 29" is exercised) and every
	// month-based pattern, including "every 3 months" and "first day of the
	// year", recurs at least twice inside it.
	periodHours = 735 * 24
)

// periodStart is the first hour of the reference period, a Monday.
//
//nolint:gochecknoglobals // immutable constant instant
var periodStart = time.Date(2028, time.January, 3, 0, 0, 0, 0, time.UTC)

// window is the set of hours of the reference period a schedule allows.
type window struct {
	hours [(periodHours + 63) / 64]uint64
}

func (w *window) set(i int)     { w.hours[i/64] |= 1 << (i % 64) }
func (w window) has(i int) bool { return w.hours[i/64]&(1<<(i%64)) != 0 }

func (w window) count() int {
	n := 0
	for _, word := range w.hours {
		n += bits.OnesCount64(word)
	}
	return n
}

// within reports whether every hour w allows is also allowed by outer.
func (w window) within(outer window) bool {
	for i, word := range w.hours {
		if word&^outer.hours[i] != 0 {
			return false
		}
	}
	return true
}

// judge classifies the window: "no_schedule" when every hour is allowed (the
// schedule restricts nothing), "unreadable_schedule" when no hour is (it
// parsed, but can never run), "too_often" when it breaks the contract above,
// and "ok" otherwise.
func (w window) judge() string {
	switch w.count() {
	case periodHours:
		return "no_schedule"
	case 0:
		return "unreadable_schedule"
	}
	// Blocks that touch either end of the period are cut off by the period
	// rather than by the schedule, so their length and start say nothing.
	// Every supported pattern recurs inside the period, so dropping them loses
	// no information.
	prevStart := -1
	for i := 0; i < periodHours; {
		if !w.has(i) {
			i++
			continue
		}
		start := i
		for i < periodHours && w.has(i) {
			i++
		}
		if start == 0 || i == periodHours {
			continue
		}
		if i-start > maxBlockHours || (prevStart >= 0 && start-prevStart < minBlockSpacing) {
			return "too_often"
		}
		prevStart = start
	}
	return "ok"
}

// parseSchedule reads a schedule's entries into the window they allow. ok is
// false when any entry is not a form this package models, so a caller treats
// the schedule as unreadable. An empty list, or a first entry of "at any
// time", allows every hour: Renovate treats both as no schedule, and its
// validator returns early on that first entry
// (https://github.com/renovatebot/renovate/blob/main/lib/workers/repository/update/branch/schedule.ts).
func parseSchedule(entries []string) (w window, ok bool) {
	if len(entries) == 0 || normalize(entries[0]) == "at any time" {
		return fullWindow(), true
	}
	table := hourTable()
	for _, entry := range entries {
		clauses, ok := parseEntry(entry)
		if !ok {
			return window{}, false
		}
		for _, c := range clauses {
			for i, h := range table {
				if c.matches(h) {
					w.set(i)
				}
			}
		}
	}
	return w, true
}

func fullWindow() window {
	var w window
	for i := range periodHours {
		w.set(i)
	}
	return w
}

func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// hourFields are the calendar fields of one hour of the reference period.
type hourFields struct {
	hour, dom, month, dow uint8
}

// hourTable is built once: parsing a schedule is then a bitmask test per hour.
//
//nolint:gochecknoglobals // computed once; read-only
var hourTable = sync.OnceValue(func() []hourFields {
	t := make([]hourFields, periodHours)
	for i := range t {
		at := periodStart.Add(time.Duration(i) * time.Hour)
		t[i] = hourFields{
			hour:  uint8(at.Hour()),
			dom:   uint8(at.Day()),
			month: uint8(at.Month()),
			dow:   uint8(at.Weekday()), // Sunday is 0, as in cron
		}
	}
	return t
})

// clause is one conjunction of constraints: an hour is allowed when every
// field's bit is set. A bit position is the field's value (hour 0-23, day of
// month 1-31, month 1-12, day of week 0-6 with Sunday 0).
type clause struct {
	hour, dom, month, dow uint64
}

func anyClause() clause {
	return clause{hour: 1<<24 - 1, dom: 1<<32 - 2, month: 1<<13 - 2, dow: 1<<7 - 1}
}

func (c clause) matches(h hourFields) bool {
	return c.hour&(1<<h.hour) != 0 && c.dom&(1<<h.dom) != 0 &&
		c.month&(1<<h.month) != 0 && c.dow&(1<<h.dow) != 0
}

// parseEntry reads one schedule string. Cron is tried first and a string that
// is valid cron is never read as text, matching Renovate; text is read after
// the rewrites Renovate applies to configuration.
func parseEntry(entry string) ([]clause, bool) {
	if c, ok := parseCron(entry); ok {
		return []clause{c}, true
	}
	var out []clause
	for _, text := range rewriteText(entry) {
		c, ok := parseText(text)
		if !ok {
			return nil, false
		}
		out = append(out, c)
	}
	return out, true
}

// parseCron reads a cron expression the way Renovate accepts one. Renovate
// hands the text to croner, which takes five, six (leading seconds) or seven
// (leading seconds, trailing year) fields, and then requires the text to start
// with "*" and the minute field to cover every minute, because a schedule is
// judged by the hour and cannot be narrower:
// https://github.com/renovatebot/renovate/blob/main/lib/workers/repository/update/branch/schedule.ts
// Day of month and day of week must both match when both are restricted
// (Renovate sets croner's domAndDow), which is the conjunction clause encodes.
// Year, when present, must be "*": a pinned year would make the schedule stop
// recurring, which the reference period cannot show.
func parseCron(text string) (clause, bool) {
	if !strings.HasPrefix(text, "*") {
		return clause{}, false
	}
	f := strings.Fields(text)
	if len(f) < 5 || len(f) > 7 {
		return clause{}, false
	}
	if len(f) == 7 {
		if f[6] != "*" {
			return clause{}, false
		}
		f = f[:6]
	}
	if len(f) == 6 {
		if _, ok := parseField(f[0], 0, 59, nil); !ok {
			return clause{}, false
		}
		f = f[1:]
	}
	minutes, ok := parseField(f[0], 0, 59, nil)
	if !ok || minutes != 1<<60-1 {
		return clause{}, false
	}
	var c clause
	if c.hour, ok = parseField(f[1], 0, 23, nil); !ok {
		return clause{}, false
	}
	if c.dom, ok = parseField(f[2], 1, 31, nil); !ok {
		return clause{}, false
	}
	if c.month, ok = parseField(f[3], 1, 12, monthNames); !ok {
		return clause{}, false
	}
	if c.dow, ok = parseField(f[4], 0, 7, dayNames); !ok {
		return clause{}, false
	}
	// 7 is a second spelling of Sunday.
	if c.dow&(1<<7) != 0 {
		c.dow = c.dow&^(1<<7) | 1
	}
	return c, true
}

//nolint:gochecknoglobals // immutable lookup table
var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

//nolint:gochecknoglobals // immutable lookup table
var dayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// parseField reads one cron field (a comma list of "*", "a", "a-b", each with
// an optional "/step") into a bitmask indexed by value. Wrapping ranges,
// "L", "#", "?" and "W" are not modelled and fail closed.
func parseField(field string, lo, hi int, names map[string]int) (uint64, bool) {
	value := func(s string) (int, bool) {
		if v, ok := names[strings.ToLower(s)]; ok {
			return v, true
		}
		v, err := strconv.Atoi(s)
		return v, err == nil && s[0] != '+' && s[0] != '-' && v >= lo && v <= hi
	}
	var mask uint64
	for part := range strings.SplitSeq(field, ",") {
		if part == "" {
			return 0, false
		}
		base, stepText, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 || stepText[0] == '+' {
				return 0, false
			}
			// A step past the field's span selects only the start, so capping
			// it keeps the loop below from overflowing on a huge number.
			step = min(n, hi+1)
		}
		from, to := lo, hi
		switch {
		case base == "*":
		case strings.Contains(base, "-"):
			a, b, _ := strings.Cut(base, "-")
			var okA, okB bool
			if from, okA = value(a); !okA {
				return 0, false
			}
			if to, okB = value(b); !okB || to < from {
				return 0, false
			}
		default:
			v, ok := value(base)
			if !ok {
				return 0, false
			}
			from = v
			// A bare value with a step runs to the end of the field; without
			// one it is just that value.
			if !hasStep {
				to = v
			}
		}
		for v := from; v <= to; v += step {
			mask |= 1 << v
		}
	}
	return mask, true
}

// rewriteText applies the rewrites Renovate performs on schedule text before
// using it, so the text is read as Renovate reads it, and returns the entries
// the string becomes (one, or two for the "after X and before Y" split):
//   - the "every month" and "monthly" mappings, and the migration of
//     "on the last day of the month", "on every weekday", a trailing
//     "every day", "every <weekday name>" and a trailing "days"
//     https://github.com/renovatebot/renovate/blob/main/lib/config/migrations/custom/schedule-migration.ts
//     https://github.com/renovatebot/renovate/blob/main/lib/workers/repository/update/branch/schedule.ts
//   - the split of "after X and before Y ..." into two entries when X is
//     later than Y, because later.js would otherwise intersect the two bounds
//     into an empty window.
//
// Names are matched case-insensitively here although Renovate's own regexes
// are lowercase: later.js is case-insensitive, so the rewrite is harmless on
// text Renovate would only parse, and reading it lowercase keeps one spelling
// of each form.
func rewriteText(entry string) []string {
	s := normalize(entry)
	if s == "every month" || s == "monthly" {
		s = "before 5am on the first day of the month"
	}
	s = strings.Replace(s, "on the last day of the month", "on the first day of the month", 1)
	s = strings.Replace(s, "on every weekday", "every weekday", 1)
	s = strings.TrimSuffix(s, " every day")
	tokens := strings.Fields(s)
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i] == "every" && isFullDayName(tokens[i+1]) {
			tokens[i] = "on"
		}
	}
	s = strings.Join(tokens, " ")
	if base, ok := strings.CutSuffix(s, "days"); ok {
		s = base + "day"
	}

	tokens = strings.Fields(s)
	if len(tokens) >= 5 && tokens[0] == "after" && tokens[2] == "and" && tokens[3] == "before" {
		a, okA := parseClock(tokens[1])
		b, okB := parseClock(tokens[4])
		if okA && okB && a > b {
			rest := strings.Join(tokens[5:], " ")
			return []string{
				strings.TrimSpace("after " + tokens[1] + " " + rest),
				strings.TrimSpace("before " + tokens[4] + " " + rest),
			}
		}
	}
	return []string{s}
}

func isFullDayName(s string) bool {
	switch s {
	case "sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday":
		return true
	}
	return false
}

// parseClock reads "4am", "4:00am" or "12pm" into minutes after midnight.
// Minutes other than :00 are not modelled: a window is whole hours.
func parseClock(s string) (int, bool) {
	var pm bool
	switch {
	case strings.HasSuffix(s, "am"):
	case strings.HasSuffix(s, "pm"):
		pm = true
	default:
		return 0, false
	}
	digits := s[:len(s)-2]
	if h, m, has := strings.Cut(digits, ":"); has {
		if m != "00" {
			return 0, false
		}
		digits = h
	}
	h, err := strconv.Atoi(digits)
	if err != nil || h < 1 || h > 12 || digits[0] == '+' {
		return 0, false
	}
	h %= 12
	if pm {
		h += 12
	}
	return h * 60, true
}

// parseText reads one rewritten later.js entry: a sequence of clauses ("and"
// between them is only punctuation). Clauses on different fields all must
// hold, but repeated clauses on the same field add up, as in later.js: "on
// monday on friday" allows both days, and "every weekday on monday" allows
// all five weekdays. "before" and "after" are their own fields and, once each
// is unioned, intersect ("after 1am and before 3am" is the two hours between).
// Reading repeated clauses as an intersection would narrow the window below
// what Renovate runs and could pass a schedule it should fail. Anything
// outside the forms below fails closed.
//
//	before <time> | after <time>
//	on <day> [and <day>]...        full or three-letter names, "and" required
//	on the first day of the month
//	every weekday | every weekend
//	every <n> months               months 1, 1+n, 1+2n, ...
func parseText(text string) (clause, bool) {
	tokens := strings.Fields(text)
	if len(tokens) == 0 {
		return clause{}, false
	}
	// A field starts empty and, once any clause names it, holds the union of
	// the clauses; a field no clause names allows everything.
	var before, after, dow, dom, month uint64
	for i := 0; i < len(tokens); {
		switch tokens[i] {
		case "and":
			// A dangling "and" has nothing to join.
			if i == len(tokens)-1 {
				return clause{}, false
			}
			i++
		case "before", "after":
			if i+1 >= len(tokens) {
				return clause{}, false
			}
			minutes, ok := parseClock(tokens[i+1])
			if !ok {
				return clause{}, false
			}
			below := uint64(1)<<(minutes/60) - 1
			if tokens[i] == "before" {
				// An empty set ("before 12am") still marks the field as named.
				before |= below | 1<<24
			} else {
				after |= (1<<24-1)&^below | 1<<24
			}
			i += 2
		case "on":
			if hasPrefix(tokens[i:], "on", "the", "first", "day", "of", "the", "month") {
				dom |= 1 << 1
				i += 7
				continue
			}
			j := i + 1
			d, ok := dayNames[shortDay(tokens[min(j, len(tokens)-1)])]
			if j >= len(tokens) || !ok {
				return clause{}, false
			}
			dow |= 1 << d
			j++
			// Only "and" chains day names: later.js rejects "monday thursday".
			for j+1 < len(tokens) && tokens[j] == "and" {
				d, ok := dayNames[shortDay(tokens[j+1])]
				if !ok {
					break
				}
				dow |= 1 << d
				j += 2
			}
			i = j
		case "every":
			switch {
			case hasPrefix(tokens[i:], "every", "weekday"):
				dow |= 1<<6 - 1<<1 // monday..friday
				i += 2
			case hasPrefix(tokens[i:], "every", "weekend"):
				dow |= 1<<0 | 1<<6
				i += 2
			case i+2 < len(tokens) && (tokens[i+2] == "months" || tokens[i+2] == "month"):
				n, err := strconv.Atoi(tokens[i+1])
				if err != nil || n < 1 || n > 12 || tokens[i+1][0] == '+' {
					return clause{}, false
				}
				for m := 1; m <= 12; m += n {
					month |= 1 << m
				}
				i += 3
			default:
				return clause{}, false
			}
		default:
			return clause{}, false
		}
	}
	c := anyClause()
	if before != 0 {
		c.hour &= before
	}
	if after != 0 {
		c.hour &= after
	}
	if dow != 0 {
		c.dow = dow
	}
	if dom != 0 {
		c.dom = dom
	}
	if month != 0 {
		c.month = month
	}
	return c, true
}

// shortDay maps a full day name to its three-letter key and leaves everything
// else unchanged, so a token that is not a day misses dayNames.
func shortDay(s string) string {
	if isFullDayName(s) {
		return s[:3]
	}
	return s
}

func hasPrefix(tokens []string, prefix ...string) bool {
	if len(tokens) < len(prefix) {
		return false
	}
	for i, p := range prefix {
		if tokens[i] != p {
			return false
		}
	}
	return true
}
