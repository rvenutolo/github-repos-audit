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

var boldName = regexp.MustCompile(`\*\*([a-z_]+)\*\*`)

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
