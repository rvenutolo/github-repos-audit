package rules

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

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
// what forces that when a type's expectations later change. The cells this can
// judge are required, not_required and info, whose value depends only on the
// type. An override against a public or public_published cell cannot be
// judged here, because visibility is never declared; see Validate.
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
			if !decidesUnconditionally(base) {
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
			base := spec.cells[check]
			if decidesUnconditionally(base) {
				continue // ValidateOffline's job
			}
			resolved := base.resolve(public, r.Published)
			if reason := deadReason(check, Type(r.Type), spec, resolved, r.Overrides[key]); reason != "" {
				problems = append(problems, fmt.Errorf("%s: override %s = %q is dead — %s",
					r.Name, key, r.Overrides[key], reason))
			}
		}
	}
	return join(problems)
}

// CheckCoverage enforces, in both directions, that repos.toml describes
// exactly the repositories that exist. A discovered repository with no entry
// would quietly escape the standard; an entry naming a repository that no
// longer exists is a line nobody will ever notice is stale.
func CheckCoverage(discovered, declared []string) error {
	inDeclared := make(map[string]bool, len(declared))
	for _, name := range declared {
		inDeclared[name] = true
	}
	inDiscovered := make(map[string]bool, len(discovered))
	for _, name := range discovered {
		inDiscovered[name] = true
	}

	var missing, extra []string
	for _, name := range discovered {
		if !inDeclared[name] {
			missing = append(missing, name)
		}
	}
	for _, name := range declared {
		if !inDiscovered[name] {
			extra = append(extra, name)
		}
	}
	slices.SortFunc(missing, compareNames)
	slices.SortFunc(extra, compareNames)

	var problems []error
	if len(missing) > 0 {
		problems = append(problems, fmt.Errorf(
			"repos.toml has no entry for %s", strings.Join(missing, ", ")))
	}
	if len(extra) > 0 {
		problems = append(problems, fmt.Errorf(
			"repos.toml names %s, which no longer exists on the account", strings.Join(extra, ", ")))
	}
	return join(problems)
}

// deadReason explains why an override says nothing, or returns "" when it
// genuinely changes the derived value.
func deadReason(check Check, typ Type, spec typeSpec, exp expectation, value string) string {
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

	switch exp {
	case expAlways:
		if value == OverrideRequired {
			return fmt.Sprintf("%s already expects it", typ)
		}
	case expNA:
		if value == OverrideNotRequired {
			return fmt.Sprintf("%s already treats it as n/a", typ)
		}
	case expInfo:
		return "the row is informational and never counts as a gap"
	case expPublic, expPublicPublished:
		// Undecidable without visibility; Validate handles the resolved form.
		return ""
	}
	return ""
}

func join(problems []error) error {
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrConfig, errors.Join(problems...))
}
