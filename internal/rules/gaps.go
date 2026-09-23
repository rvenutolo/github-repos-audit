package rules

import (
	"maps"
	"slices"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// gapLabels gives each check the phrase the worklist uses. A check absent here
// never produces a gap, which is how the informational rows stay out of the
// worklist; TestGapLabels_coverEveryJudgedCheck holds every judged check to
// having one.
//
//nolint:gochecknoglobals // immutable lookup table
var gapLabels = map[Check]string{
	CheckREADME:                "No README",
	CheckDescription:           "No description",
	CheckLicense:               "No license",
	CheckGitignore:             "No .gitignore",
	CheckCIWorkflows:           "No CI workflows",
	CheckRequiredChecks:        "No required checks",
	CheckCIGreen:               "CI not green on the default branch",
	CheckRenovate:              "No Renovate config",
	CheckRenovateMinReleaseAge: "Renovate minimum release age unset, unresolved or too short",
	CheckEditorconfig:          "No .editorconfig",
	CheckFlakeNix:              "No flake.nix",
	CheckJustfile:              "No .justfile",
	CheckHomepage:              "No homepage",
	CheckTopics:                "No topics",
	CheckReleases:              "No releases",
	CheckChangelog:             "No CHANGELOG.md",
	CheckSignedCommits:         "Signed commits not required",
	CheckTagRuleset:            "No tag ruleset",
	CheckSecretScanning:        "Secret scanning off",
	CheckVulnReporting:         "Private vulnerability reporting off",
}

// communityFiles names the three community health files, in the order the
// worklist reports them. The community-files row shows a fraction in the
// table; the worklist names the individual missing files, because "1/3" does
// not tell you which two to write.
//
//nolint:gochecknoglobals // immutable lookup table
var communityFiles = []struct {
	label   string
	present func(audit.Files) bool
}{
	{label: "No SECURITY.md", present: func(f audit.Files) bool { return f.Security }},
	{label: "No CONTRIBUTING.md", present: func(f audit.Files) bool { return f.Contributing }},
	{label: "No CODE_OF_CONDUCT.md", present: func(f audit.Files) bool { return f.CodeOfConduct }},
}

// directPushLabels gives the direct-push row a phrase for each direction. One
// label would be ambiguous: a content repository fails by blocking, everything
// else fails by allowing.
//
//nolint:gochecknoglobals // immutable lookup table
var directPushLabels = map[string]string{
	directPushAllowed: "Direct push to the default branch allowed",
	directPushBlocked: "Direct push to the default branch blocked",
}

// collectGaps builds the worklist: one entry per failing check, in the fixed
// order the checks are declared in, listing the repositories that fail it. A
// check with no failures is omitted entirely.
func collectGaps(repos []RepoReport) []Gap {
	var gaps []Gap
	for _, c := range Checks() {
		//nolint:exhaustive // every other check goes through gapLabels below
		switch c {
		case CheckCommunityFiles:
			gaps = append(gaps, communityGaps(repos)...)
		case CheckDirectPush:
			gaps = append(gaps, directPushGaps(repos)...)
		default:
			label, ok := gapLabels[c]
			if !ok {
				continue
			}
			if names := failing(repos, c); len(names) > 0 {
				gaps = append(gaps, Gap{Check: c, Label: label, Repos: names})
			}
		}
	}
	return gaps
}

// failing lists the repositories whose cell for a check is a failure.
func failing(repos []RepoReport, c Check) []string {
	var names []string
	for _, r := range repos {
		if r.Cells[c].Verdict == Fail {
			names = append(names, r.Repo.Name)
		}
	}
	slices.SortFunc(names, compareNames)
	return names
}

func communityGaps(repos []RepoReport) []Gap {
	out := make([]Gap, 0, len(communityFiles))
	for _, f := range communityFiles {
		var names []string
		for _, r := range repos {
			if r.Cells[CheckCommunityFiles].Verdict == Fail && !f.present(r.Repo.Files) {
				names = append(names, r.Repo.Name)
			}
		}
		if len(names) == 0 {
			continue
		}
		slices.SortFunc(names, compareNames)
		out = append(out, Gap{Check: CheckCommunityFiles, Label: f.label, Repos: names})
	}
	return out
}

func directPushGaps(repos []RepoReport) []Gap {
	out := make([]Gap, 0, len(directPushLabels))
	// Sorted so the two directions appear in a fixed order rather than map order.
	for _, observed := range []string{directPushAllowed, directPushBlocked} {
		var names []string
		for _, r := range repos {
			cell := r.Cells[CheckDirectPush]
			if cell.Verdict == Fail && cell.Value == observed {
				names = append(names, r.Repo.Name)
			}
		}
		if len(names) == 0 {
			continue
		}
		slices.SortFunc(names, compareNames)
		out = append(out, Gap{Check: CheckDirectPush, Label: directPushLabels[observed], Repos: names})
	}
	return out
}

// collectOverrides lists every declared override, so an excused gap is visible
// rather than silently absent.
func collectOverrides(repos []RepoReport) []Override {
	var out []Override
	for _, r := range repos {
		for _, key := range slices.Sorted(maps.Keys(r.Repo.Overrides)) {
			check, ok := ParseCheck(key)
			if !ok {
				continue
			}
			out = append(out, Override{Repo: r.Repo.Name, Check: check, Value: r.Repo.Overrides[key]})
		}
	}
	return out
}
