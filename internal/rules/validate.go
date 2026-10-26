package rules

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// ErrConfig reports a configuration error the rules caught rather than the
// parser. Every validation failure in this package wraps it.
var ErrConfig = errors.New("configuration error")

// Declaration is one repos.toml entry, reduced to what the rules need.
type Declaration struct {
	// Name is the repository name.
	Name string
	// Type names the [types.*] table the repository is judged against.
	Type Type
	// Published is the declared flag.
	Published bool
	// Overrides is keyed by check name.
	Overrides map[string]string
}

// ValidateOffline reports the configuration errors decidable from the
// declarations and the types table alone, with no network call: an override
// that is dead against a cell the type decides unconditionally.
//
// A dead override must be deleted rather than left to rot, and rejecting it is
// what forces that when a type's expectations later change. The pairs this can
// judge are a cell and an override that both say required, not_required or
// info, whose meaning depends only on the words. An override against a public
// or public_published cell, or one using either word, cannot be judged here,
// because visibility is never declared; see Validate.
func ValidateOffline(types Types, decls []Declaration) error {
	specs, err := compileTypes(types)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrConfig, err)
	}
	var problems []error
	for _, d := range decls {
		spec, ok := specs[string(d.Type)]
		if !ok {
			// An unknown type is the parser's error to report, not this one's.
			continue
		}
		for _, key := range slices.Sorted(maps.Keys(d.Overrides)) {
			check, known := ParseCheck(key)
			if !known {
				continue
			}
			base := spec.cells[check]
			if !decidesUnconditionally(base) || !overrideUnconditional(d.Overrides[key]) {
				continue
			}
			if reason := deadReason(check, d.Type, spec, base, d.Overrides[key]); reason != "" {
				problems = append(problems, fmt.Errorf("%s: override %s = %q is dead — %s",
					d.Name, key, d.Overrides[key], reason))
			}
		}
	}
	return join(problems)
}

// Validate reports the configuration errors that need live data.
func Validate(snap *audit.Snapshot) error {
	specs, err := compileTypes(snap.Types)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrConfig, err)
	}
	var problems []error
	for _, r := range snap.Repos {
		spec, known := specs[r.Type]
		if !known {
			continue
		}
		public := r.Visibility == "public"

		// The homepage and topics rules are defined for public-and-published
		// or for neither, so this combination has no answer and must be
		// rejected rather than rendered arbitrarily.
		if r.Published && !public {
			problems = append(problems, fmt.Errorf(
				"%s: published = true on a %s repository — the homepage and topics rules have no answer for that",
				r.Name, r.Visibility))
		}

		for _, key := range slices.Sorted(maps.Keys(r.Overrides)) {
			check, ok := ParseCheck(key)
			if !ok {
				continue
			}
			base, value := spec.cells[check], r.Overrides[key]
			if decidesUnconditionally(base) && overrideUnconditional(value) {
				continue // ValidateOffline's job
			}
			// Both sides resolve against the same live facts, so an override
			// is dead exactly when it changes nothing for this repository.
			if e, ok := parseExpectation(value); ok {
				value = e.resolve(public, r.Published).String()
			}
			resolved := base.resolve(public, r.Published)
			if reason := deadReason(check, Type(r.Type), spec, resolved, value); reason != "" {
				problems = append(problems, fmt.Errorf("%s: override %s = %q is dead — %s",
					r.Name, key, r.Overrides[key], reason))
			}
		}
	}
	return join(problems)
}

// Coverage compares the account with repos.toml and returns where they
// disagree. A disagreement is a fact for the report, not an error: creating
// or archiving a repository is routine, and it must not cost every other
// repository its report.
//
// Names are compared exactly, so two spellings differing only in case are
// drift in both directions rather than a match the declared-field lookup
// would then miss. Each list is sorted, free of duplicates, and nil when
// empty — nil, because that is what keeps config_drift out of audit.json.
func Coverage(discovered, declared []string) audit.ConfigDrift {
	return audit.ConfigDrift{
		Unconfigured: missingFrom(discovered, declared),
		StaleEntries: missingFrom(declared, discovered),
	}
}

// missingFrom returns the names in have that are absent from other, sorted
// and free of duplicates, or nil when there are none.
func missingFrom(have, other []string) []string {
	known := make(map[string]bool, len(other))
	for _, name := range other {
		known[name] = true
	}
	var missing []string
	for _, name := range have {
		if !known[name] {
			missing = append(missing, name)
		}
	}
	slices.SortFunc(missing, compareNames)
	return slices.Compact(missing)
}

// deadReason explains why an override says nothing, or returns "" when it
// genuinely changes the derived value.
func deadReason(check Check, typ Type, spec typeSpec, exp expectation, value string) string {
	if check.Threshold() {
		return thresholdDeadReason(typ, spec, exp, value)
	}
	if check == CheckDirectPush {
		switch value {
		case OverrideRequired:
			return "direct push is always judged, so \"required\" changes nothing"
		case OverrideNotRequired:
			return ""
		default:
			if value == spec.directPush {
				return fmt.Sprintf("%s already expects direct push %s", typ, value)
			}
			return ""
		}
	}

	// An override means what its word would mean on the type, so it is dead
	// when it is the very word the cell already resolves to.
	if over, ok := parseExpectation(value); !ok || over != exp {
		return ""
	}
	// exp is never public or public_published here: ValidateOffline passes
	// only words that need no visibility, and Validate resolves both sides.
	if exp == expAlways {
		return fmt.Sprintf("%s already expects it", typ)
	}
	if exp == expNA {
		return fmt.Sprintf("%s already treats it as n/a", typ)
	}
	return fmt.Sprintf("%s already shows it as info", typ)
}

// overrideUnconditional reports whether an override's meaning depends on
// nothing but its word: anything but public or public_published, including a
// value such as a duration or a direct-push setting.
func overrideUnconditional(value string) bool {
	e, ok := parseExpectation(value)
	return !ok || decidesUnconditionally(e)
}

func join(problems []error) error {
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrConfig, errors.Join(problems...))
}
