package rules

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Types is every repository type a configuration defines, keyed by type name.
// Each type maps every check name to the word saying what the type expects of
// it. It is the shape repos.toml's [types.*] tables decode into and the shape
// audit.json records, so a standard has one representation from the file to
// the report.
type Types map[string]map[string]string

// typeNamePattern is what a type name may look like: a TOML bare key that
// reads the same in a table header, an error message and audit.json.
var typeNamePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

// allowedWords is every word a type may use for a check, in the order an error
// message lists them.
func allowedWords(c Check) []string {
	switch {
	case c == CheckDirectPush:
		return []string{directPushBlocked, directPushAllowed}
	case c.ValueOnly():
		return []string{expInfo.String(), expNA.String()}
	case c.Threshold():
		// Durations are not a finite list; wordAccepted checks them.
		return []string{expNA.String(), expInfo.String()}
	default:
		return []string{
			expAlways.String(), expNA.String(), expPublic.String(),
			expPublicPublished.String(), expInfo.String(),
		}
	}
}

// wordAccepted reports whether a type may give check c the word.
func wordAccepted(c Check, word string) bool {
	if c.Threshold() {
		if _, ok := ParseThreshold(word); ok {
			return true
		}
	}
	return slices.Contains(allowedWords(c), word)
}

// wantPhrase lists the accepted words the way the error messages read.
func wantPhrase(words []string) string {
	if len(words) == 2 {
		return words[0] + " or " + words[1]
	}
	return "one of " + strings.Join(words, ", ")
}

// TypeProblems reports every way a types table is unusable: a type name
// outside [a-z0-9_-]+, a check a type does not list, a word the check does not
// accept, and a key that names no check. Every check must be listed so that a
// check added to the tool is decided for every type rather than silently
// expected of none.
//
// It returns every problem rather than the first, ordered by type name, then
// by check order, then unknown keys by name, so one run reports the whole
// table the same way each time. A type no repository uses is not a problem.
func TypeProblems(types Types) []error {
	var problems []error
	for _, name := range slices.Sorted(maps.Keys(types)) {
		entries := types[name]
		if !typeNamePattern.MatchString(name) {
			problems = append(problems, fmt.Errorf("types.%s: type name must match [a-z0-9_-]+", name))
		}
		for _, c := range Checks() {
			word, listed := entries[c.String()]
			if !listed {
				problems = append(problems, fmt.Errorf("types.%s: missing check %q", name, c))
				continue
			}
			if !wordAccepted(c, word) {
				want := wantPhrase(allowedWords(c))
				if c.Threshold() {
					want = `a duration like "7 days", ` + want
				}
				problems = append(problems, fmt.Errorf("types.%s: %s = %q (want %s)", name, c, word, want))
			}
		}
		for _, key := range slices.Sorted(maps.Keys(entries)) {
			if _, known := ParseCheck(key); !known {
				problems = append(problems, fmt.Errorf("types.%s: unknown check %q", name, key))
			}
		}
	}
	return problems
}

// typeSpec is one type's expectations, parsed out of its words.
type typeSpec struct {
	// cells holds an expectation for every check. Direct push is always
	// judged, so its cell is expAlways and what it is judged against lives in
	// directPush.
	cells map[Check]expectation
	// directPush is the value direct push is expected to have.
	directPush string
	// minReleaseAge is the threshold the Renovate minimum-release-age check
	// is held to when its cell is expAlways; zero otherwise.
	minReleaseAge time.Duration
}

// compileTypes parses a types table. A table config has accepted always
// compiles; the error exists for a snapshot assembled some other way.
func compileTypes(types Types) (map[string]typeSpec, error) {
	if problems := TypeProblems(types); len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	specs := make(map[string]typeSpec, len(types))
	for name, entries := range types {
		spec := typeSpec{
			cells:      make(map[Check]expectation, len(checkNames)),
			directPush: entries[CheckDirectPush.String()],
		}
		for _, c := range Checks() {
			if c == CheckDirectPush {
				spec.cells[c] = expAlways
				continue
			}
			if c.Threshold() {
				// A duration means "expected, at least this long"; the words
				// not_required and info fall through to parseExpectation.
				if d, ok := ParseThreshold(entries[c.String()]); ok {
					spec.cells[c], spec.minReleaseAge = expAlways, d
					continue
				}
			}
			e, _ := parseExpectation(entries[c.String()])
			spec.cells[c] = e
		}
		specs[name] = spec
	}
	return specs, nil
}
