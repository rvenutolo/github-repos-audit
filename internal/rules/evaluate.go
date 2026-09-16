package rules

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// Override values that are not a check-specific value.
const (
	// OverrideRequired expects a check even where the type says n/a.
	OverrideRequired = "required"
	// OverrideNotRequired renders n/a and never counts as a gap.
	OverrideNotRequired = "not_required"
)

// mergeGateCheck is the always-green placeholder status check the OpenTofu
// baseline attaches to repositories with no CI of their own. A repository
// whose only required check is this one has no real gate, so it fails the
// required-checks row rather than passing with a count of one.
const mergeGateCheck = "merge-gate"

// communityFileCount is how many community health files a published software
// repository is expected to carry: SECURITY.md, CONTRIBUTING.md and
// CODE_OF_CONDUCT.md.
const communityFileCount = 3

// defaultTagRuleset is the name and scope the OpenTofu baseline gives a tag
// ruleset. A ruleset matching both renders as "default"; anything else renders
// its own name, so a repository whose tag ruleset is named differently from
// the baseline still passes and remains visible as different.
const defaultTagRuleset = "protect-tags"

// observation is what the collected facts say about one check, before any
// question of whether the check applies.
type observation struct {
	// ok reports the expectation would be satisfied if it applied.
	ok bool
	// value is the scalar the check carries, if any.
	value string
	// at is the instant the check carries, if any.
	at time.Time
	// code asks for the value to be rendered as inline code.
	code bool
	// scalar marks a value that survives an n/a verdict.
	scalar bool
	// unknown forces n/a: the fact was not collected, rather than collected
	// and found wanting.
	unknown bool
	// pending marks a CI run in flight, which is shown and never a gap.
	pending bool
}

// Evaluate turns a snapshot into a report: one cell per check per repository,
// the gap worklist, the settings exceptions and the declared overrides.
//
// It reads only the snapshot. Everything repos.toml declares — the types
// table onto the snapshot, and type, published and the overrides onto each
// audit.Repo — has already been copied by the caller, which is what keeps
// this package free of a dependency on config and therefore free of an
// import cycle with it.
func Evaluate(snap *audit.Snapshot) (*Report, error) {
	if snap == nil {
		return nil, errors.New("evaluate: nil snapshot")
	}

	specs, err := compileTypes(snap.Types)
	if err != nil {
		return nil, fmt.Errorf("evaluate: %w", err)
	}

	rep := &Report{GeneratedAt: snap.GeneratedAt, Owner: snap.Owner}
	rep.Repos = make([]RepoReport, 0, len(snap.Repos))
	for _, r := range snap.Repos {
		cells, err := evaluateRepo(r, specs)
		if err != nil {
			return nil, err
		}
		rep.Repos = append(rep.Repos, RepoReport{Repo: r, Cells: cells})
	}
	slices.SortFunc(rep.Repos, func(a, b RepoReport) int { return compareNames(a.Repo.Name, b.Repo.Name) })

	rep.Gaps = collectGaps(rep.Repos)
	rep.Overrides = collectOverrides(rep.Repos)
	rep.Exceptions, rep.NoConsensus = modalExceptions(snap.Repos)
	return rep, nil
}

// compareNames orders repository names the way the report does: alphabetically
// and case-insensitively, so a mixed-case name sorts by its lowercase form
// rather than ahead of everything lowercase.
func compareNames(a, b string) int {
	if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// evaluateRepo produces one cell for every check.
func evaluateRepo(r audit.Repo, specs map[string]typeSpec) (map[Check]Cell, error) {
	spec, known := specs[r.Type]
	if !known {
		return nil, fmt.Errorf("evaluate %s: unknown type %q", r.Name, r.Type)
	}

	public := r.Visibility == "public"
	overrides := r.Overrides

	// Direct push is computed first: the required-checks row is n/a wherever
	// direct push is allowed LIVE, not wherever the type says it ought to be.
	// Every other cell reports live state, and making this one report an
	// expectation would be the odd one out.
	pushCell := directPushCell(r, spec.directPush, overrides)

	cells := make(map[Check]Cell, len(checkNames))
	for _, c := range Checks() {
		if c == CheckDirectPush {
			cells[c] = pushCell
			continue
		}

		exp := spec.cells[c].resolve(public, r.Published)
		overridden := false
		if v, has := overrides[c.String()]; has {
			switch v {
			case OverrideRequired:
				exp, overridden = expAlways, true
			case OverrideNotRequired:
				exp, overridden = expNA, true
			default:
				// A value override on a present-or-absent check has no
				// meaning; config's validation rejects the unknown ones, and a
				// value here is simply carried into the Overrides section.
				overridden = true
			}
		}

		// Required checks are n/a wherever direct push is allowed.
		if c == CheckRequiredChecks && pushCell.Value == directPushAllowed {
			exp = expNA
		}

		// A repository with no commits cannot be judged on what is in them,
		// and a fact GitHub declined to answer is not a fact found wanting:
		// both are n/a rather than gaps.
		obs := observe(c, r)
		if obs.unknown {
			exp = expNA
		}

		cells[c] = Cell{
			Check:      c,
			Verdict:    verdictFor(exp, obs),
			Value:      obs.value,
			At:         obs.at,
			Code:       obs.code,
			Scalar:     obs.scalar,
			Overridden: overridden,
		}
	}
	return cells, nil
}

// verdictFor reduces an expectation and an observation to a verdict.
func verdictFor(exp expectation, obs observation) Verdict {
	switch exp {
	case expInfo:
		return Info
	case expNA:
		return NA
	case expAlways:
		if obs.pending {
			// A run in flight is not a failure, and the daily cadence means it
			// will have settled by the next render.
			return Info
		}
		if obs.ok {
			return Pass
		}
		return Fail
	case expPublic, expPublicPublished:
		// resolve() has already reduced these; reaching here is a bug.
		return NA
	default:
		return NA
	}
}

//nolint:exhaustive // the informational and direct-push rows are handled above
func observe(c Check, r audit.Repo) observation {
	switch c {
	case CheckDescription:
		return observation{ok: r.Description != ""}
	case CheckREADME:
		return observation{ok: r.Files.README}
	case CheckGitignore:
		return observation{ok: r.Files.Gitignore}
	case CheckEditorconfig:
		return observation{ok: r.Files.Editorconfig}
	case CheckFlakeNix:
		return observation{ok: r.Files.FlakeNix}
	case CheckJustfile:
		return observation{ok: r.Files.Justfile}
	case CheckChangelog:
		return observation{ok: r.Files.Changelog}
	case CheckHomepage:
		return observation{ok: r.Homepage != ""}

	case CheckSignedCommits:
		if !r.Branch.Known {
			return observation{unknown: true}
		}
		return observation{ok: slices.Contains(r.Branch.Types, "required_signatures")}

	case CheckTagRuleset:
		return observeTagRuleset(r)

	case CheckLicense:
		// NOASSERTION means GitHub found a license file it could not identify,
		// which is not a pass.
		return observation{ok: r.License != "" && r.License != "NOASSERTION", value: r.License, scalar: true}

	case CheckSecretScanning:
		// An absent security_and_analysis block is n/a, not disabled: GitHub
		// omits it entirely for a private repository on a personal plan.
		if r.Settings.SecretScanning == "" {
			return observation{unknown: true}
		}
		return observation{ok: r.Settings.SecretScanning == "enabled"}

	case CheckVulnReporting:
		if r.Settings.PrivateVulnerabilityReporting == nil {
			return observation{unknown: true}
		}
		return observation{ok: *r.Settings.PrivateVulnerabilityReporting}

	case CheckTopics:
		return observation{ok: r.Topics > 0, value: strconv.Itoa(r.Topics), scalar: true}

	case CheckCommunityFiles:
		n := 0
		for _, present := range []bool{r.Files.Security, r.Files.Contributing, r.Files.CodeOfConduct} {
			if present {
				n++
			}
		}
		return observation{ok: n == communityFileCount, value: fmt.Sprintf("%d/%d", n, communityFileCount)}

	case CheckRenovate:
		return observation{ok: r.Files.RenovateConfig != "", value: r.Files.RenovateConfig, code: true, scalar: true}

	case CheckCIWorkflows:
		return observation{ok: hasRealWorkflow(r.Files.Workflows)}

	case CheckRequiredChecks:
		return observeRequiredChecks(r)

	case CheckCIGreen:
		return observeCIGreen(r)

	case CheckReleases:
		n := max(r.Releases.Total-r.Releases.Drafts, 0)
		return observation{ok: n > 0, value: strconv.Itoa(n), scalar: true}

	case CheckLastReleaseAge:
		return observation{at: r.Releases.LastPublishedAt, scalar: true}
	case CheckLastPush:
		return observation{at: r.PushedAt, scalar: true}
	case CheckOpenPRs:
		return observation{value: strconv.Itoa(r.OpenPullRequests), scalar: true}
	case CheckBranches:
		// The default branch is not news; what the report is after is how many
		// branches there are besides it. An empty repository has no branches
		// at all, so max keeps the count off negative numbers.
		return observation{value: strconv.Itoa(max(r.Branches-1, 0)), scalar: true}

	default:
		return observation{unknown: true}
	}
}

func observeTagRuleset(r audit.Repo) observation {
	for _, rs := range r.Rulesets {
		if rs.Target != "TAG" || rs.Enforcement != "ACTIVE" {
			continue
		}
		if rs.Name == defaultTagRuleset && slices.Equal(rs.Include, []string{"refs/tags/**"}) {
			return observation{ok: true, value: "default", scalar: true}
		}
		return observation{ok: true, value: rs.Name, code: true, scalar: true}
	}
	return observation{}
}

func observeRequiredChecks(r audit.Repo) observation {
	if !r.Branch.Known {
		return observation{unknown: true}
	}
	own := 0
	for _, name := range r.Branch.RequiredChecks {
		if name != mergeGateCheck {
			own++
		}
	}
	if own == 0 {
		// Renders ✗ rather than "0": a repository whose only required check is
		// the always-green placeholder has no gate at all, and a zero would
		// read like a count someone chose.
		return observation{scalar: true}
	}
	return observation{ok: true, value: strconv.Itoa(own), scalar: true}
}

func observeCIGreen(r audit.Repo) observation {
	switch r.CIState {
	case "":
		// A null rollup means no check has run on the head commit.
		return observation{scalar: true}
	case "SUCCESS", "EXPECTED":
		return observation{ok: true, value: r.CIState, scalar: true}
	case "PENDING":
		return observation{value: r.CIState, pending: true, scalar: true}
	default:
		return observation{value: r.CIState, scalar: true}
	}
}

// hasRealWorkflow reports whether .github/workflows holds anything but the
// always-green merge-gate placeholder.
func hasRealWorkflow(entries []string) bool {
	for _, name := range entries {
		if name != mergeGateCheck+".yml" {
			return true
		}
	}
	return false
}

// directPushCell decides whether pushing straight to the default branch is
// allowed, and whether that matches what the type declares.
func directPushCell(r audit.Repo, want string, overrides map[string]string) Cell {
	observed := observeDirectPush(r)

	exp := expAlways
	overridden := false
	if v, has := overrides[CheckDirectPush.String()]; has {
		overridden = true
		switch v {
		case OverrideNotRequired:
			exp = expNA
		case OverrideRequired:
			// The row is already unconditional, so "required" says nothing.
		default:
			want = v
		}
	}

	verdict := NA
	if exp == expAlways {
		verdict = Fail
		if observed == want {
			verdict = Pass
		}
	}
	return Cell{
		Check:      CheckDirectPush,
		Verdict:    verdict,
		Value:      observed,
		Mark:       true,
		Overridden: overridden,
	}
}

// observeDirectPush answers "can I push straight to the default branch?".
//
// The live branch rules answer it directly. An empty repository has no default
// branch, so that endpoint is never called and the answer comes from the
// rulesets instead: allowed unless an ACTIVE branch ruleset carries a pull
// request rule.
func observeDirectPush(r audit.Repo) string {
	if r.Branch.Known {
		if slices.Contains(r.Branch.Types, "pull_request") {
			return directPushBlocked
		}
		return directPushAllowed
	}
	for _, rs := range r.Rulesets {
		if rs.Target != "BRANCH" || rs.Enforcement != "ACTIVE" {
			continue
		}
		if slices.Contains(rs.Rules, "PULL_REQUEST") {
			return directPushBlocked
		}
	}
	return directPushAllowed
}
