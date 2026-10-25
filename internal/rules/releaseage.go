package rules

import (
	"fmt"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// The two values the minimum-release-age cell shows when there is no
// duration to show.
const (
	// releaseAgeNone is a configuration that sets no minimumReleaseAge, or
	// sets it to null: Renovate proposes a release the moment it appears.
	releaseAgeNone = "none"
	// releaseAgeUnresolved is a configuration whose value could not be
	// decided — a preset that is missing or does not parse, or a value
	// Renovate itself would not read. audit.json says which.
	releaseAgeUnresolved = "unresolved"
)

// ValidThresholdOverride reports whether an override value is one the
// minimum-release-age check accepts: a repos.toml duration, not_required or
// info.
func ValidThresholdOverride(v string) bool {
	if _, ok := ParseThreshold(v); ok {
		return true
	}
	return v == OverrideNotRequired || v == expInfo.String()
}

// minReleaseAgeCell judges the Renovate minimum release age. It has its own
// path, as direct push does, because what it is held to is a duration rather
// than a yes or no.
func minReleaseAgeCell(r audit.Repo, spec typeSpec, overrides map[string]string) Cell {
	c := CheckRenovateMinReleaseAge
	exp, threshold := spec.cells[c], spec.minReleaseAge
	cell := Cell{Check: c, Scalar: true, Mark: true}
	if v, has := overrides[c.String()]; has {
		cell.Overridden = true
		switch v {
		case OverrideNotRequired:
			exp = expNA
		case expInfo.String():
			exp = expInfo
		default:
			if d, ok := ParseThreshold(v); ok {
				exp, threshold = expAlways, d
			}
		}
	}

	// Without a Renovate configuration there is nothing to read, and the
	// renovate row already carries that gap: counting it twice would make
	// one missing file look like two problems.
	if r.Files.RenovateConfig == "" {
		cell.Verdict = NA
		return cell
	}

	age, ok := releaseAge(r.Renovate)
	switch {
	case r.Renovate.MinReleaseAgeError != "" || (r.Renovate.MinReleaseAge != "" && !ok):
		cell.Value = releaseAgeUnresolved
	case r.Renovate.MinReleaseAge == "":
		cell.Value = releaseAgeNone
	default:
		cell.Value = r.Renovate.MinReleaseAge
	}

	switch exp {
	case expInfo:
		cell.Verdict = Info
	case expAlways:
		cell.Verdict = Fail
		if cell.Value == r.Renovate.MinReleaseAge && ok && age >= threshold {
			cell.Verdict = Pass
		}
	case expNA, expPublic, expPublicPublished:
		// A threshold check never compiles to the public words; listing
		// them keeps the exhaustive linter honest, as verdictFor does.
		cell.Verdict = NA
	}
	return cell
}

// releaseAge parses the collected value the way Renovate would.
func releaseAge(rn audit.Renovate) (time.Duration, bool) {
	if rn.MinReleaseAgeError != "" || rn.MinReleaseAge == "" {
		return 0, false
	}
	return ParseRenovateDuration(rn.MinReleaseAge)
}

// thresholdDeadReason is deadReason for the minimum-release-age check: an
// override is dead when it restates the type's word or, for a duration, the
// same length of time however it is spelled.
func thresholdDeadReason(typ Type, spec typeSpec, exp expectation, value string) string {
	switch exp {
	case expInfo:
		return "the row is informational and never counts as a gap"
	case expNA:
		if value == OverrideNotRequired {
			return fmt.Sprintf("%s already treats it as n/a", typ)
		}
	case expAlways:
		if d, ok := ParseThreshold(value); ok && d == spec.minReleaseAge {
			return fmt.Sprintf("%s already expects the same minimum release age", typ)
		}
	case expPublic, expPublicPublished:
		// A threshold check never takes these words.
	}
	return ""
}
