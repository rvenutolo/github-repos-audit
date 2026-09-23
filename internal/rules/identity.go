package rules

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// identityPattern is "Name <email>": the name is everything before the last
// run of whitespace and "<", the email has no whitespace or angle brackets.
// Anything looser would accept a canonical identity git itself could never
// have recorded.
var identityPattern = regexp.MustCompile(`^(.*\S)\s+<([^<>\s]+)>$`)

// ParseIdentity reads an identity written "Name <email>", the way git prints
// one. It reports whether s had that shape.
func ParseIdentity(s string) (audit.Identity, bool) {
	m := identityPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return audit.Identity{}, false
	}
	return audit.Identity{Name: strings.TrimSpace(m[1]), Email: m[2]}, true
}

// FormatIdentity writes an identity the way git prints it, which is also the
// string [identity].match is tested against.
func FormatIdentity(id audit.Identity) string {
	return id.Name + " <" + id.Email + ">"
}

// IdentityProblems reports every way an [identity] table is unusable: a
// canonical identity that is not "Name <email>", a match expression that is
// missing or does not compile, and a canonical identity the expression does
// not match — a contradictory pair that would fail every repository.
func IdentityProblems(std audit.IdentityStandard) []error {
	var problems []error
	canonical, ok := ParseIdentity(std.Canonical)
	if !ok {
		problems = append(problems, fmt.Errorf("identity.canonical = %q (want \"Name <email>\")", std.Canonical))
	}
	var match *regexp.Regexp
	if std.Match == "" {
		problems = append(problems, errors.New("identity.match: missing"))
	} else {
		re, err := regexp.Compile(std.Match)
		if err != nil {
			problems = append(problems, fmt.Errorf("identity.match: %w", err))
		}
		match = re
	}
	if ok && match != nil && !match.MatchString(FormatIdentity(canonical)) {
		problems = append(problems, errors.New("identity.canonical does not match identity.match"))
	}
	return problems
}

// identityStandard is an [identity] table compiled for judging.
type identityStandard struct {
	canonical audit.Identity
	match     *regexp.Regexp
}

// compileIdentity compiles a snapshot's standard. A zero standard compiles to
// nil: no repository may then judge git_identity, which evaluateRepo enforces.
// A standard config has accepted always compiles; the error exists for a
// snapshot assembled some other way.
//
//nolint:nilnil // nil, nil is the documented "no standard" answer
func compileIdentity(std audit.IdentityStandard) (*identityStandard, error) {
	if std == (audit.IdentityStandard{}) {
		return nil, nil
	}
	if problems := IdentityProblems(std); len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	canonical, _ := ParseIdentity(std.Canonical)
	return &identityStandard{canonical: canonical, match: regexp.MustCompile(std.Match)}, nil
}

// sameIdentity is the one equality the check uses: names exactly, emails
// without regard to case, because mail systems treat addresses that way and a
// capital letter is not a different person.
func sameIdentity(a, b audit.Identity) bool {
	return a.Name == b.Name && strings.EqualFold(a.Email, b.Email)
}

// compareIdentities orders identities by name, then email, byte order: the
// order the collector records them in.
func compareIdentities(a, b audit.Identity) int {
	if c := strings.Compare(a.Name, b.Name); c != 0 {
		return c
	}
	return strings.Compare(a.Email, b.Email)
}

// mine returns the account holder's identities in ids: those match accepts,
// collapsed under sameIdentity, with the canonical one first and the rest by
// name, then email.
//
// Of several spellings of one identity, the kept one is the first in byte
// order — except that any spelling of the canonical identity is replaced by
// the canonical itself, so the report shows the spelling the owner declared
// rather than whichever capitalisation happens to sort first.
func (s *identityStandard) mine(ids []audit.Identity) []audit.Identity {
	// The collector already sorts, but a snapshot assembled some other way
	// need not; sorting a copy makes "first in byte order" true regardless.
	sorted := slices.Clone(ids)
	slices.SortFunc(sorted, compareIdentities)

	var out []audit.Identity
	for _, id := range sorted {
		if !s.match.MatchString(FormatIdentity(id)) {
			continue
		}
		if slices.ContainsFunc(out, func(kept audit.Identity) bool { return sameIdentity(kept, id) }) {
			continue
		}
		if sameIdentity(id, s.canonical) {
			id = s.canonical
		}
		out = append(out, id)
	}
	// Stable, so everything after the canonical keeps the byte order above.
	slices.SortStableFunc(out, func(a, b audit.Identity) int {
		switch ac, bc := sameIdentity(a, s.canonical), sameIdentity(b, s.canonical); {
		case ac == bc:
			return 0
		case ac:
			return -1
		default:
			return 1
		}
	})
	return out
}
