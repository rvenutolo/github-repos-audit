package rules

import (
	"errors"
	"fmt"
	"regexp"
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
