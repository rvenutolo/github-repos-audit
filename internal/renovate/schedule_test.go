package renovate

import (
	"testing"
	"time"
)

func TestParseSchedule_judge(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		entries []string
		want    string
	}{
		{"early monday text", []string{"before 4am on monday"}, "ok"},
		{"weekend text", []string{"every weekend"}, "ok"},
		{"friday night plus weekend runs 50h", []string{"after 10pm on friday", "every weekend"}, "too_often"},
		{"first day of month", []string{"on the first day of the month"}, "ok"},
		{"every 3 months", []string{"every 3 months on the first day of the month"}, "ok"},
		{"cron early monday", []string{"* 0-3 * * 1"}, "ok"},
		{"weekday text", []string{"every weekday"}, "too_often"},
		{"daily text", []string{"before 4am"}, "too_often"},
		{"daily joins weekly", []string{"before 4am on monday", "before 4am"}, "too_often"},
		{"two days a week", []string{"on monday and thursday"}, "too_often"},
		{"empty list", nil, "no_schedule"},
		{"empty slice", []string{}, "no_schedule"},
		{"at any time", []string{"at any time"}, "no_schedule"},
		{"at any time first wins", []string{"At Any Time", "before 4am"}, "no_schedule"},
		{"every hour cron", []string{"* * * * *"}, "no_schedule"},
		{"feb 30 never matches", []string{"* * 30 2 *"}, "unreadable_schedule"},
		{"after and before bounds that cross never overlap", []string{"after 3am and before 3am"}, "unreadable_schedule"},

		// The presets Renovate ships, as cron.
		{"schedule:daily", []string{"* 0-3 * * *"}, "too_often"},
		{"schedule:earlyMondays", []string{"* 0-3 * * 1"}, "ok"},
		{"schedule:monthly", []string{"* 0-3 1 * *"}, "ok"},
		{"schedule:quarterly", []string{"* * 1 */3 *"}, "ok"},
		{"schedule:yearly", []string{"* * 1 */12 *"}, "ok"},
		{"schedule:weekdays", []string{"* * * * 1-5"}, "too_often"},
		{"schedule:weekends is exactly 48h", []string{"* * * * 0,6"}, "ok"},
		{"schedule:nonOfficeHours", []string{"* 0-4,22-23 * * 1-5", "* * * * 0,6"}, "too_often"},
		{"schedule:officeHours", []string{"* 8-17 * * 1-5"}, "too_often"},

		// Cron details.
		{"sunday as 7", []string{"* 0-3 * * 7"}, "ok"},
		{"named day", []string{"* 0-3 * * MON"}, "ok"},
		{"named day range", []string{"* * * * mon-wed"}, "too_often"},
		{"month names", []string{"* 0-3 1 jan,apr,jul,oct *"}, "ok"},
		{"minute step of 1 covers every minute", []string{"*/1 0-3 * * 1"}, "ok"},
		{"six fields lead with seconds", []string{"* * 0-3 * * 1"}, "ok"},
		{"seven fields end with year", []string{"* * 0-3 * * 1 *"}, "ok"},
		{"bare dom and dow both must match", []string{"* * 1-7 * 4"}, "ok"},
		{"day-of-month step every 10 days", []string{"* * */10 * *"}, "ok"},
		{"day-of-month step every 5 days", []string{"* * */5 * *"}, "too_often"},
		{"dow start with step", []string{"* 0-3 * * 1/3"}, "too_often"},
		{"huge step selects only the start", []string{"* 0-3 * * 1/99999999999999999"}, "ok"},
		{"shorthand 12am and 12pm", []string{"before 12pm on monday"}, "ok"},

		// Text forms.
		{"mixed case", []string{"Before 4AM on Monday"}, "ok"},
		{"short hours with minutes", []string{"before 4:00am on monday"}, "ok"},
		{"three-letter day", []string{"before 4am on mon"}, "ok"},
		{"time after day", []string{"on monday before 4am"}, "ok"},
		{"monthly mapping", []string{"monthly"}, "ok"},
		{"every month mapping", []string{"every month"}, "ok"},
		{"every monday is on monday", []string{"before 4am every monday"}, "ok"},
		{"saturday and sunday", []string{"on saturday and sunday"}, "ok"},
		{"friday and saturday", []string{"on friday and saturday"}, "ok"},
		{"after-before split every weekday", []string{"after 10pm and before 5am every weekday"}, "too_often"},
		{"after-before in order is one window", []string{"after 1am and before 3am on monday"}, "ok"},
		{"last day of month is first day", []string{"on the last day of the month"}, "ok"},
		{"trailing every day is dropped", []string{"before 4am on monday every day"}, "ok"},
		{"on every weekday", []string{"on every weekday"}, "too_often"},
		{"trailing days", []string{"on mondays"}, "ok"},
		{"every 4 months text", []string{"every 4 months"}, "too_often"},
		{"on the first day and a time", []string{"before 5am on the first day of the month"}, "ok"},
		{"and between clauses", []string{"before 4am and on monday"}, "ok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w, ok := parseSchedule(tt.entries)
			if !ok {
				t.Fatalf("parseSchedule(%q) ok = false, want true", tt.entries)
			}
			if got := w.judge(); got != tt.want {
				t.Errorf("parseSchedule(%q).judge() = %q, want %q", tt.entries, got, tt.want)
			}
		})
	}
}

func TestParseSchedule_unreadable(t *testing.T) {
	t.Parallel()
	for _, entries := range [][]string{
		{"whenever"},
		{"0 0 * *"},
		{""},
		{"   "},
		{"before 4am", ""},
		{"before 4am", "whenever"},
		{"before 4am", "at any time"},

		// Cron that Renovate rejects or this package does not model.
		{"0 0-3 * * 1"},
		{"*/5 0-3 * * 1"},
		{"0-59 0-3 * * 1"},
		{" * 0-3 * * 1"},
		{"* 0-3 * * 1 1 1 1"},
		{"* 0-3 * *"},
		{"* * * * * * 2030"},
		{"* x * * *"},
		{"* 24 * * *"},
		{"* 5-3 * * *"},
		{"* * * * sat-sun"},
		{"* 0-3 * * L"},
		{"* 0-3 * * 1#2"},
		{"* 0-3 * * 1/0"},
		{"* 0-3 * * 1/x"},
		{"* 0-3 * * 1/+1"},
		{"* 0-3 * * 1,"},
		{"* 0-3 * * -1"},
		{"* 0-3 * * 8"},
		{"* 0-3 * * +1"},
		{"* 0-3 * * 1-x"},
		{"* 0-3 * * x-1"},
		{"* * 0 * *"},
		{"* * * 13 *"},
		{"*x * 0-3 * * 1"},
		{"* x 0-3 * * 1 *"},

		// Later text that Renovate rejects or this package does not model.
		{"before 4am on"},
		{"before"},
		{"before 4"},
		{"before 13am"},
		{"before 0am"},
		{"before 4:30am"},
		{"before 4:0am"},
		{"before +4am"},
		{"after 10pm and"},
		{"and"},
		{"on"},
		{"on the second day of the month"},
		{"on funday"},
		{"on monday and"},
		{"every"},
		{"every day"},
		{"every 5 minutes"},
		{"every 0 months"},
		{"every 13 months"},
		{"every +3 months"},
		{"every x months"},
		{"in January"},
		{"before 3am on the first monday of the month"},
		{"after 10pm and before 5:30am"},
		{"after 10pm and before 5am bogus"},
	} {
		if _, ok := parseSchedule(entries); ok {
			t.Errorf("parseSchedule(%q) ok = true, want false", entries)
		}
	}
}

// Every spelling of one schedule lands on the same hours.
func TestParseSchedule_equivalentSpellings(t *testing.T) {
	t.Parallel()
	groups := [][][]string{
		{{"before 4am on monday"}, {"* 0-3 * * 1"}, {"* 0-3 * * mon"}, {"every monday before 4am"}, {"Before 4:00AM on Mon"}},
		{{"every weekend"}, {"* * * * 0,6"}, {"* * * * 6,7"}, {"on saturday and sunday"}, {"on saturday", "on sunday"}},
		{{"every weekday"}, {"* * * * 1-5"}, {"on every weekday"}},
		{{"after 10pm and before 5am every weekday"}, {"after 10pm every weekday", "before 5am every weekday"}},
		{{"monthly"}, {"every month"}, {"before 5am on the first day of the month"}, {"* 0-4 1 * *"}},
		{{"on the first day of the month"}, {"on the last day of the month"}, {"* * 1 * *"}},
		{{"every 3 months on the first day of the month"}, {"* * 1 1,4,7,10 *"}, {"* * 1 */3 *"}},
		{{"* * 1 */12 *"}, {"* * 1 1 *"}},
	}
	for _, group := range groups {
		want, ok := parseSchedule(group[0])
		if !ok {
			t.Fatalf("parseSchedule(%q) ok = false", group[0])
		}
		for _, entries := range group[1:] {
			got, ok := parseSchedule(entries)
			if !ok || got != want {
				t.Errorf("parseSchedule(%q) = %v (ok %v), want the same hours as %q", entries, got == want, ok, group[0])
			}
		}
	}
}

func TestParseSchedule_notEquivalent(t *testing.T) {
	t.Parallel()
	a, _ := parseSchedule([]string{"before 4am on monday"})
	b, _ := parseSchedule([]string{"before 5am on monday"})
	if a == b {
		t.Error("before 4am and before 5am describe the same hours")
	}
}

func TestWindow_within(t *testing.T) {
	t.Parallel()
	early, _ := parseSchedule([]string{"before 4am on monday"})
	weekend, _ := parseSchedule([]string{"every weekend"})
	monday, _ := parseSchedule([]string{"on monday"})
	any, _ := parseSchedule(nil)

	tests := []struct {
		name         string
		inner, outer window
		want         bool
	}{
		{"early monday not within weekend", early, weekend, false},
		{"early monday within monday", early, monday, true},
		{"monday not within early monday", monday, early, false},
		{"within itself", early, early, true},
		{"anything within any time", weekend, any, true},
		{"any time not within a narrower window", any, weekend, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.inner.within(tt.outer); got != tt.want {
				t.Errorf("within() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The period's length constant and its start must agree, so that the last hour
// is the one the judge treats as the period's end.
func TestReferencePeriod(t *testing.T) {
	t.Parallel()
	if periodStart.Weekday() != time.Monday {
		t.Errorf("period starts on %v, want Monday", periodStart.Weekday())
	}
	table := hourTable()
	if len(table) != periodHours {
		t.Fatalf("hour table has %d entries, want %d", len(table), periodHours)
	}
	end := periodStart.Add(time.Duration(periodHours) * time.Hour)
	if want := time.Date(2030, time.January, 7, 0, 0, 0, 0, time.UTC); !end.Equal(want) {
		t.Errorf("period ends at %v, want %v", end, want)
	}
	leap := false
	for _, h := range table {
		if h.month == 2 && h.dom == 29 {
			leap = true
		}
	}
	if !leap {
		t.Error("the period contains no 29 February")
	}
}

func FuzzSchedule(f *testing.F) {
	for _, seed := range []string{
		"before 4am on monday", "every weekend", "after 10pm on friday", "on the first day of the month",
		"every 3 months on the first day of the month", "* 0-3 * * 1", "every weekday", "before 4am",
		"on monday and thursday", "at any time", "* * 30 2 *", "whenever", "0 0 * *", "",
		"* 0-4,22-23 * * 1-5", "after 10pm and before 5am every weekday", "monthly", "* * * * * * *",
	} {
		f.Add(seed, "")
		f.Add(seed, "every weekend")
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		for _, entries := range [][]string{{a}, {a, b}, {}} {
			w, ok := parseSchedule(entries)
			if !ok {
				continue
			}
			switch got := w.judge(); got {
			case "ok", "no_schedule", "too_often", "unreadable_schedule":
			default:
				t.Fatalf("judge() = %q for %q", got, entries)
			}
			if !w.within(w) {
				t.Fatalf("window of %q is not within itself", entries)
			}
		}
	})
}
