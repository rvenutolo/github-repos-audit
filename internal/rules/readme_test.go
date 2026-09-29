package rules_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// readmeSection returns the body of the README's level-two section with the
// given heading, up to the next level-two heading, with every run of
// whitespace collapsed to one space so that a sentence prettier rewraps still
// matches. The README is the user-facing reference for repos.toml and the
// checks; these tests hold the parts of it that restate this package to what
// this package does, as the schema tests do for audit.schema.json.
func readmeSection(t *testing.T, heading string) string {
	t.Helper()

	readmePath := filepath.Join("..", "..", "README.md")
	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read %s: %v", readmePath, err)
	}
	_, body, ok := strings.Cut(string(data), "\n## "+heading+"\n")
	if !ok {
		t.Fatalf("%s has no %q section", readmePath, "## "+heading)
	}
	body, _, _ = strings.Cut(body, "\n## ")
	return strings.Join(strings.Fields(body), " ")
}

// find returns the submatches of re in s, failing the test when there are
// none: a sentence that has been reworded out of the pattern's reach must
// fail loudly, never leave the assertion on it silently checking nothing.
func find(t *testing.T, re *regexp.Regexp, s string) []string {
	t.Helper()

	m := re.FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("README no longer has a sentence matching %q", re)
	}
	return m
}

var (
	boldName   = regexp.MustCompile(`\*\*([a-z_]+)\*\*`)
	backticked = regexp.MustCompile("`([^`]+)`")
)

// backtickedIn returns every `code` span in s, in order.
func backtickedIn(s string) []string {
	matches := backticked.FindAllStringSubmatch(s, -1)
	spans := make([]string, 0, len(matches))
	for _, m := range matches {
		spans = append(spans, m[1])
	}
	return spans
}

// checkNames returns the names of the checks for which keep is true, in
// worklist order.
func checkNames(keep func(rules.Check) bool) []string {
	var names []string
	for _, c := range rules.Checks() {
		if keep(c) {
			names = append(names, c.String())
		}
	}
	return names
}

// TestREADME_theChecksNameEveryCheck: the README's "The checks" section is
// where a reader learns what each check judges. It groups them by theme, so
// only the set is compared, not the order; a name in bold twice is a check
// described twice, which is its own drift.
func TestREADME_theChecksNameEveryCheck(t *testing.T) {
	t.Parallel()

	matches := boldName.FindAllStringSubmatch(readmeSection(t, "The checks"), -1)
	got := make([]string, 0, len(matches))
	for _, m := range matches {
		if slices.Contains(got, m[1]) {
			t.Errorf("README's checks section describes %s twice", m[1])
		}
		got = append(got, m[1])
	}
	want := checkNames(func(rules.Check) bool { return true })
	if diff := cmp.Diff(slices.Sorted(slices.Values(want)), slices.Sorted(slices.Values(got))); diff != "" {
		t.Errorf("README's checks section differs from rules.Checks() (-want +got):\n%s", diff)
	}
}

// numberWords spells the counts the README uses in prose, for the one
// sentence that counts the value-only checks.
var numberWords = map[string]int{
	"two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8,
}

var (
	wordTableRow      = regexp.MustCompile("\\| `([a-z_]+)` +\\|")
	directPushWords   = regexp.MustCompile("`direct_push` takes `([a-z_]+)` or `([a-z_]+)` instead")
	valueOnlySentence = regexp.MustCompile(`the ([a-z]+) value-only checks — ([^—]+) — take only ([^.]+)\.`)
	thresholdSentence = regexp.MustCompile("`([a-z_]+)` takes a duration instead — [^—]+ — or `([a-z_]+)` or `([a-z_]+)`")
)

// TestREADME_reposTomlReferenceMatchesTheRules holds the README's repos.toml
// reference to the words each check takes. It compares against
// audit.schema.json's enums rather than restating them: the schema tests
// already hold those enums to what TypeProblems accepts, so README = schema
// here makes README = rules by transitivity, with one list of words to edit.
func TestREADME_reposTomlReferenceMatchesTheRules(t *testing.T) {
	t.Parallel()

	section := readmeSection(t, "`repos.toml` reference")
	defs := loadSchema(t).Defs

	rows := wordTableRow.FindAllStringSubmatch(section, -1)
	table := make([]string, 0, len(rows))
	for _, m := range rows {
		table = append(table, m[1])
	}
	if diff := cmp.Diff(defs.Expectation.Enum, table); diff != "" {
		t.Errorf("README's expectation-word table differs from $defs.expectation.enum (-schema +README):\n%s", diff)
	}

	m := find(t, directPushWords, section)
	if diff := cmp.Diff(defs.DirectPush.Enum, m[1:]); diff != "" {
		t.Errorf("README's direct_push words differ from $defs.direct_push.enum (-schema +README):\n%s", diff)
	}

	m = find(t, valueOnlySentence, section)
	valueOnly := backtickedIn(m[2])
	if diff := cmp.Diff(checkNames(rules.Check.ValueOnly), valueOnly); diff != "" {
		t.Errorf("README's value-only checks differ from the ValueOnly() checks (-rules +README):\n%s", diff)
	}
	if n, ok := numberWords[m[1]]; !ok || n != len(valueOnly) {
		t.Errorf("README counts %q value-only checks but names %d", m[1], len(valueOnly))
	}
	if diff := cmp.Diff(defs.InfoExpectation.Enum, backtickedIn(m[3])); diff != "" {
		t.Errorf("README's value-only words differ from $defs.info_expectation.enum (-schema +README):\n%s", diff)
	}

	m = find(t, thresholdSentence, section)
	if diff := cmp.Diff(checkNames(rules.Check.Threshold), m[1:2]); diff != "" {
		t.Errorf("README's duration check differs from the Threshold() checks (-rules +README):\n%s", diff)
	}
	if len(defs.ReleaseAgeThreshold.AnyOf) == 0 {
		t.Fatal("$defs.release_age_threshold.anyOf is empty") // TestSchema_releaseAgeThresholdMatchesTheRules says why
	}
	if diff := cmp.Diff(defs.ReleaseAgeThreshold.AnyOf[0].Enum, m[2:]); diff != "" {
		t.Errorf("README's words for the duration check differ from $defs.release_age_threshold's (-schema +README):\n%s", diff)
	}
}
