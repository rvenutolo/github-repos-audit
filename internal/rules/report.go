package rules

import (
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// Verdict is what one check concluded about one repository.
type Verdict int

// The four verdicts. A cell renders a tick, a cross, "n/a" or the value it
// carries; Info exists for a cell that is always shown and never counted as a
// gap.
const (
	// Pass means the expectation is met.
	Pass Verdict = iota
	// Fail means it is not, and the repository appears in the Gaps worklist.
	Fail
	// NA means the check does not apply. A cell that carries a scalar still
	// shows it: the report withholds the judgement, not the fact.
	NA
	// Info means the cell is always shown and never counted as a gap.
	Info
)

// String returns the verdict's name.
func (v Verdict) String() string {
	switch v {
	case Pass:
		return "pass"
	case Fail:
		return "fail"
	case NA:
		return "n/a"
	case Info:
		return "info"
	default:
		return "unknown"
	}
}

// Cell is one check's outcome for one repository.
type Cell struct {
	// Check is which row of the report this is.
	Check Check
	// Verdict is the outcome.
	Verdict Verdict
	// Value is the scalar the check carries, rendered instead of a tick or a
	// cross. Empty for a check that is merely present-or-absent.
	Value string
	// At carries an instant for a cell whose value is a date. The renderer
	// turns it into "12d" or "4mo" against its injected clock; rules never
	// formats a relative date, because that would rot the golden files daily.
	At time.Time
	// Scalar reports that Value survives an n/a verdict. A check that carries
	// a scalar still renders it on a – cell — the report withholds the
	// judgement, not the fact — while a check that is merely present-or-absent
	// renders n/a, because a bare cross on a non-gap cell is
	// indistinguishable from a gap in the same column.
	Scalar bool
	// Code asks the renderer to show Value as inline code — a Renovate config
	// path, a ruleset name.
	Code bool
	// Mark asks the renderer to show the tick or cross alongside Value rather
	// than instead of it, so direct push reads "✓ blocked" and not merely
	// "blocked": the value alone would hide the verdict, and the verdict alone
	// would hide which way round the repository is.
	Mark bool
	// Overridden reports that this cell came from a repos.toml override rather
	// than from the repository's type.
	Overridden bool
}

// RepoReport is every cell for one repository, beside the facts they were
// derived from.
type RepoReport struct {
	// Repo is the collected facts.
	Repo audit.Repo
	// Cells is one entry per check, complete: every check in Checks() is
	// present.
	Cells map[Check]Cell
}

// Cell returns the cell for a check. A check that is somehow absent yields the
// zero Cell, which renders as a pass with no value — so the completeness of
// Cells is asserted by a test rather than trusted here.
func (r *RepoReport) Cell(c Check) Cell { return r.Cells[c] }

// Gap is one line of the worklist: a failing check and the repositories that
// fail it.
type Gap struct {
	// Check is the row this gap belongs to, which fixes its position.
	Check Check
	// Label is the human phrase, such as "No license".
	Label string
	// Repos is the failing repositories, sorted case-insensitively.
	Repos []string
}

// Exception is one repository deviating from the account's most common value
// for one setting.
type Exception struct {
	// Repo is the deviating repository.
	Repo string
	// Setting is the human label, such as "wiki".
	Setting string
	// Value is this repository's value.
	Value string
	// Modal is the value most of the account carries.
	Modal string
}

// Override is one entry from a repos.toml override list, reported so that an
// excused gap is visible rather than silently absent.
type Override struct {
	// Repo is the repository the override belongs to.
	Repo string
	// Check is the check it replaces.
	Check Check
	// Value is what it replaces the derived value with.
	Value string
}

// Report is everything the rules decided about one snapshot.
type Report struct {
	// GeneratedAt is the snapshot's timestamp, carried through unchanged.
	GeneratedAt time.Time
	// Owner is the snapshot's account, carried through unchanged.
	Owner string
	// Repos is sorted by name, case-insensitively.
	Repos []RepoReport
	// Gaps is in the fixed worklist order. A check with no failures is absent.
	Gaps []Gap
	// Exceptions is sorted by repository, then by setting.
	Exceptions []Exception
	// NoConsensus names the settings whose most common value covers half or
	// fewer of the repositories it applies to, sorted.
	NoConsensus []string
	// Overrides is every declared override, sorted by repository then check.
	Overrides []Override
	// Identities is one line per repository with any of the account holder's
	// git identities, in report order, whatever its git_identity verdict — it
	// is the working list for history rewrites, and an excused repository's
	// history is no less what it is. Empty when the snapshot has no identity
	// standard.
	Identities []IdentityLine
}

// IdentityLine is which of the account holder's identities one repository's
// default-branch history carries.
type IdentityLine struct {
	// Repo is the repository.
	Repo string
	// Identities is the distinct identities, canonical first, then by name,
	// then email. Two spellings of an address differing only in case are one
	// identity.
	Identities []IdentityEntry
	// Mixed reports two or more distinct identities: the history switched.
	Mixed bool
	// Canonical reports that every identity is the canonical one.
	Canonical bool
}

// IdentityEntry is one of the account holder's identities in a repository's
// history.
type IdentityEntry struct {
	// Name is the author or committer name.
	Name string
	// Email is the address. For the canonical identity it is the canonical's
	// own spelling, whatever capitalisation the commits used.
	Email string
	// Canonical reports that this is the canonical identity.
	Canonical bool
}

// String writes the entry the way git prints an identity, "Name <email>".
func (e IdentityEntry) String() string {
	return FormatIdentity(audit.Identity{Name: e.Name, Email: e.Email})
}
