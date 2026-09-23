package rules

import (
	"cmp"
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
// not match — a contradictory pair that would fail every repository. Of the
// accepted identities, it reports one that is not "Name <email>", one the
// expression does not match (it would never be judged, so accepting it could
// never matter), one that is the canonical identity, and one that repeats an
// earlier entry. An entry no repository carries is not a problem: the next
// merge made in the web UI may need it.
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
	problems = append(problems, acceptedProblems(std.Accepted, canonical, ok, match)...)
	return problems
}

// acceptedProblems checks each accepted entry, by its 0-based index as
// written. A comparison is skipped when the thing to compare with is itself
// unusable, which IdentityProblems has already reported, and a malformed entry
// takes part in no comparison: it is not an identity at all.
func acceptedProblems(accepted []string, canonical audit.Identity, canonicalOK bool, match *regexp.Regexp) []error {
	type entry struct {
		index int
		id    audit.Identity
	}
	var problems []error
	var parsed []entry
	for i, s := range accepted {
		id, ok := ParseIdentity(s)
		if !ok {
			problems = append(problems, fmt.Errorf("identity.accepted[%d] = %q (want \"Name <email>\")", i, s))
			continue
		}
		if match != nil && !match.MatchString(FormatIdentity(id)) {
			problems = append(problems, fmt.Errorf("identity.accepted[%d] does not match identity.match", i))
		}
		if canonicalOK && sameIdentity(id, canonical) {
			problems = append(problems, fmt.Errorf("identity.accepted[%d] is identity.canonical", i))
		}
		if j := slices.IndexFunc(parsed, func(e entry) bool { return sameIdentity(e.id, id) }); j >= 0 {
			problems = append(problems, fmt.Errorf("identity.accepted[%d] repeats identity.accepted[%d]", i, parsed[j].index))
		}
		parsed = append(parsed, entry{index: i, id: id})
	}
	return problems
}

// identityStandard is an [identity] table compiled for judging.
type identityStandard struct {
	canonical audit.Identity
	accepted  []audit.Identity
	match     *regexp.Regexp
}

// compileIdentity compiles a snapshot's standard. A zero standard compiles to
// nil: no repository may then judge git_identity, which evaluateRepo enforces.
// A standard config has accepted always compiles; the error exists for a
// snapshot assembled some other way.
//
//nolint:nilnil // nil, nil is the documented "no standard" answer
func compileIdentity(std audit.IdentityStandard) (*identityStandard, error) {
	if std.Canonical == "" && std.Match == "" && len(std.Accepted) == 0 {
		return nil, nil
	}
	if problems := IdentityProblems(std); len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	canonical, _ := ParseIdentity(std.Canonical)
	accepted := make([]audit.Identity, 0, len(std.Accepted))
	for _, s := range std.Accepted {
		id, _ := ParseIdentity(s) // IdentityProblems has refused any that do not parse
		accepted = append(accepted, id)
	}
	return &identityStandard{canonical: canonical, accepted: accepted, match: regexp.MustCompile(std.Match)}, nil
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

// identityKind is how the standard classes one of the account holder's
// identities. The order is the order the listing shows them in.
type identityKind int

const (
	kindCanonical identityKind = iota
	kindAccepted
	kindWrong
)

// kind classes one of the owner's identities.
func (s *identityStandard) kind(id audit.Identity) identityKind {
	switch {
	case sameIdentity(id, s.canonical):
		return kindCanonical
	case slices.ContainsFunc(s.accepted, func(a audit.Identity) bool { return sameIdentity(a, id) }):
		return kindAccepted
	default:
		return kindWrong
	}
}

// declared is the spelling the standard gives id: the canonical identity's or
// the accepted entry's own, or id itself when the standard does not name it.
func (s *identityStandard) declared(id audit.Identity) audit.Identity {
	if sameIdentity(id, s.canonical) {
		return s.canonical
	}
	for _, a := range s.accepted {
		if sameIdentity(a, id) {
			return a
		}
	}
	return id
}

// mine returns the account holder's identities in ids: those match accepts,
// collapsed under sameIdentity. The canonical identity comes first, then the
// accepted ones, then the wrong ones, each group by name, then email.
//
// Of several spellings of one identity, the kept one is the first in byte
// order — except that a spelling of the canonical identity or of an accepted
// one is replaced by the standard's own, so the report shows the spelling the
// owner declared rather than whichever capitalisation happens to sort first.
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
		out = append(out, s.declared(id))
	}
	slices.SortFunc(out, func(a, b audit.Identity) int {
		if c := cmp.Compare(s.kind(a), s.kind(b)); c != 0 {
			return c
		}
		return compareIdentities(a, b)
	})
	return out
}
