package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// usageExitCodeStart and readmeExitCodeStart match the first line of one exit
// code's entry: the code, then its description. In usageText that is "  1   runtime failure: ..."; in
// the README it is "- `1` — runtime failure: ...". A line that does not match
// continues the entry above it.
var (
	usageExitCodeStart  = regexp.MustCompile(`^  (\d+)\s+(.*)$`)
	readmeExitCodeStart = regexp.MustCompile("^- `(\\d+)` — (.*)$")
)

// The sentences that introduce each list. The README's is matched across
// whitespace because prettier may rewrap it.
var (
	usageExitCodes  = regexp.MustCompile(`Exit codes:`)
	readmeExitCodes = regexp.MustCompile(`The\s+exit\s+codes\s+are\s+part\s+of\s+the\s+interface:`)
)

// exitCodes collects the list that follows the first match of heading, which
// must end a line, up to the first blank line after it, as code → description.
// Wrapped lines are joined, and the description is normalized so that the
// README's Markdown (backticks, a closing full stop) and the two texts'
// different wrapping do not count as a difference: what must agree is the
// words.
func exitCodes(t *testing.T, name, text string, heading, start *regexp.Regexp) map[string]string {
	t.Helper()

	loc := heading.FindStringIndex(text)
	if loc == nil {
		t.Fatalf("%s has no match for %q", name, heading)
	}
	lines := strings.Split(text[loc[1]:], "\n")[1:]
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:] // the README leaves a blank line before its list
	}
	codes := map[string]string{}
	code := ""
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			break
		}
		if m := start.FindStringSubmatch(line); m != nil {
			code = m[1]
			if _, dup := codes[code]; dup {
				t.Errorf("%s documents exit code %s twice", name, code)
			}
			codes[code] = m[2]
			continue
		}
		if code == "" {
			t.Fatalf("%s: %q does not start an exit code entry", name, line)
		}
		codes[code] += " " + line
	}
	if len(codes) == 0 {
		t.Fatalf("%s lists no exit codes after %q", name, heading)
	}
	for c, desc := range codes {
		desc = strings.Join(strings.Fields(strings.ReplaceAll(desc, "`", "")), " ")
		codes[c] = strings.TrimSuffix(desc, ".")
	}
	return codes
}

// TestUsage_exitCodesMatchTheREADME: the README calls the exit codes part of
// the interface and lists them for readers who never run --help. Both lists
// are hand-written, so nothing but this keeps them saying the same thing.
func TestUsage_exitCodesMatchTheREADME(t *testing.T) {
	t.Parallel()

	readmePath := filepath.Join("..", "..", "README.md")
	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read %s: %v", readmePath, err)
	}

	want := exitCodes(t, "usageText", usageText, usageExitCodes, usageExitCodeStart)
	got := exitCodes(t, readmePath, string(readme), readmeExitCodes, readmeExitCodeStart)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("README exit codes differ from usageText (-usageText +README):\n%s", diff)
	}
}
