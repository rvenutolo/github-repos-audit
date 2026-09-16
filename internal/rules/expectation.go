package rules

import "fmt"

// expectation is what a type asks of one check, before visibility,
// publication and overrides are applied. A [types.*] table spells it as a
// word; see String.
type expectation int

const (
	// expNA never applies and never counts as a gap: "not_required".
	expNA expectation = iota
	// expAlways is expected: "required".
	expAlways
	// expPublic is expected only when the repository is public: "public".
	expPublic
	// expPublicPublished is expected only when the repository is public and
	// declared published: "public_published".
	expPublicPublished
	// expInfo is always shown and never a gap: "info".
	expInfo
)

// The two values direct push can have, which are also the two words a
// [types.*] table may give it.
const (
	directPushAllowed = "allowed"
	directPushBlocked = "blocked"
)

// String returns the word a [types.*] table uses for the expectation.
func (e expectation) String() string {
	switch e {
	case expNA:
		return "not_required"
	case expAlways:
		return "required"
	case expPublic:
		return "public"
	case expPublicPublished:
		return "public_published"
	case expInfo:
		return "info"
	default:
		return fmt.Sprintf("expectation(%d)", int(e))
	}
}

// parseExpectation resolves a [types.*] word. It reports whether the word is
// an expectation at all; whether a check accepts it is allowedWords' question.
func parseExpectation(word string) (expectation, bool) {
	for _, e := range []expectation{expNA, expAlways, expPublic, expPublicPublished, expInfo} {
		if e.String() == word {
			return e, true
		}
	}
	return expNA, false
}

// resolve applies visibility and publication to an expectation, reducing it
// to expected, not expected, or informational.
func (e expectation) resolve(public, published bool) expectation {
	switch e {
	case expPublic:
		if public {
			return expAlways
		}
		return expNA
	case expPublicPublished:
		if public && published {
			return expAlways
		}
		return expNA
	case expNA, expAlways, expInfo:
		return e
	default:
		return expNA
	}
}

// decidesUnconditionally reports whether an expectation depends on nothing
// but the type — required, not_required or info rather than public or
// public_published. An override against such a cell can be checked for
// deadness offline; an override against a visibility-dependent cell cannot,
// because visibility is never declared.
func decidesUnconditionally(e expectation) bool {
	return e == expAlways || e == expNA || e == expInfo
}
