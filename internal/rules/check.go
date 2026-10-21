// Package rules decides what each repository is expected to have, which of
// those expectations it fails, and which of its settings deviate from the
// account's norm. It is the only package that turns collected facts into
// verdicts: internal/github decides nothing and internal/render decides only
// what a verdict looks like.
package rules

import (
	"fmt"
)

// Check identifies one row of the report. Every check a repository can be
// judged on appears here exactly once, and the identifiers are the names
// repos.toml types and overrides use.
type Check int

// The checks, in the fixed order the Gaps worklist reports them: most
// consequential first, then the informational rows, which never appear as
// gaps.
const (
	CheckREADME Check = iota
	CheckDescription
	CheckLicense
	CheckGitignore
	CheckCIWorkflows
	CheckRequiredChecks
	CheckCIGreen
	CheckRenovate
	CheckRenovateMinReleaseAge
	CheckEditorconfig
	CheckFlakeNix
	CheckJustfile
	CheckHomepage
	CheckTopics
	CheckCommunityFiles
	CheckReleases
	CheckChangelog
	CheckLicenseFile
	CheckSecurityPolicy
	CheckCodeowners
	CheckPRTemplate
	CheckClaudeMD
	CheckCommitlint
	CheckGitCliff
	CheckTreefmt
	CheckCompose
	CheckTypos
	CheckYamllint
	CheckMarkdownlint
	CheckPrettier
	CheckEditorconfigChecker
	CheckLychee
	CheckZizmor
	CheckCodecov
	CheckMkDocs
	CheckSignedCommits
	CheckGitIdentity
	CheckTagRuleset
	CheckDirectPush
	CheckSecretScanning
	CheckVulnReporting
	CheckHermeticNix
	CheckNixLockAge
	CheckNixLockRefresh
	CheckNixCache
	CheckGoCache
	CheckCacheUsage
	CheckLastReleaseAge
	CheckLastPush
	CheckOpenPRs
	CheckBranches
)

// checkNames maps each check to the identifier used in repos.toml overrides
// and in audit.json. Index order matches the constants above.
//
//nolint:gochecknoglobals // immutable lookup table
var checkNames = [...]string{
	CheckREADME:                "readme",
	CheckDescription:           "description",
	CheckLicense:               "license",
	CheckGitignore:             "gitignore",
	CheckCIWorkflows:           "ci_workflows",
	CheckRequiredChecks:        "required_checks",
	CheckCIGreen:               "ci_green",
	CheckRenovate:              "renovate",
	CheckRenovateMinReleaseAge: "renovate_min_release_age",
	CheckEditorconfig:          "editorconfig",
	CheckFlakeNix:              "flake_nix",
	CheckJustfile:              "justfile",
	CheckHomepage:              "homepage",
	CheckTopics:                "topics",
	CheckCommunityFiles:        "community_files",
	CheckReleases:              "releases",
	CheckChangelog:             "changelog",
	CheckLicenseFile:           "license_file",
	CheckSecurityPolicy:        "security_policy",
	CheckCodeowners:            "codeowners",
	CheckPRTemplate:            "pr_template",
	CheckClaudeMD:              "claude_md",
	CheckCommitlint:            "commitlint",
	CheckGitCliff:              "git_cliff",
	CheckTreefmt:               "treefmt",
	CheckCompose:               "compose",
	CheckTypos:                 "typos",
	CheckYamllint:              "yamllint",
	CheckMarkdownlint:          "markdownlint",
	CheckPrettier:              "prettier",
	CheckEditorconfigChecker:   "editorconfig_checker",
	CheckLychee:                "lychee",
	CheckZizmor:                "zizmor",
	CheckCodecov:               "codecov",
	CheckMkDocs:                "mkdocs",
	CheckSignedCommits:         "signed_commits",
	CheckGitIdentity:           "git_identity",
	CheckTagRuleset:            "tag_ruleset",
	CheckDirectPush:            "direct_push",
	CheckSecretScanning:        "secret_scanning",
	CheckVulnReporting:         "vuln_reporting",
	CheckHermeticNix:           "hermetic_nix",
	CheckNixLockAge:            "nix_lock_age",
	CheckNixLockRefresh:        "nix_lock_refresh",
	CheckNixCache:              "nix_cache",
	CheckGoCache:               "go_cache",
	CheckCacheUsage:            "cache_usage",
	CheckLastReleaseAge:        "last_release_age",
	CheckLastPush:              "last_push",
	CheckOpenPRs:               "open_prs",
	CheckBranches:              "branches",
}

// String returns the check's identifier as it is written in repos.toml.
func (c Check) String() string {
	if c < 0 || int(c) >= len(checkNames) {
		return fmt.Sprintf("Check(%d)", int(c))
	}
	return checkNames[c]
}

// Checks returns every check, in worklist order. The slice is freshly built on
// each call, so a caller may sort or filter it.
func Checks() []Check {
	out := make([]Check, 0, len(checkNames))
	for i := range checkNames {
		out = append(out, Check(i))
	}
	return out
}

// ParseCheck resolves a repos.toml override key to its check. It reports
// whether the name is known; an unknown name is a configuration error rather
// than a check that is simply never expected.
func ParseCheck(name string) (Check, bool) {
	for i, n := range checkNames {
		if n == name {
			return Check(i), true
		}
	}
	return 0, false
}

// ValueOnly reports whether a check carries a value and never a pass or a
// fail: the last release, the last push, the open pull requests and the
// branch count. Such a check can be shown or left out, but never required,
// because nothing about it can be missing.
func (c Check) ValueOnly() bool {
	//nolint:exhaustive // every other check carries a pass or a fail
	switch c {
	case CheckLastReleaseAge, CheckLastPush, CheckOpenPRs, CheckBranches:
		return true
	default:
		return false
	}
}

// PathCheck reports whether a check's value is the repository path of the file
// it looks for: the justfile, the Renovate configuration and the tooling and
// community files. The File paths section compares these across the account
// and marks a path that differs from the usual one; see markDeviations.
func (c Check) PathCheck() bool {
	//nolint:exhaustive // every other check carries no file path
	switch c {
	case CheckJustfile, CheckRenovate, CheckLicenseFile, CheckSecurityPolicy,
		CheckCodeowners, CheckPRTemplate, CheckClaudeMD, CheckCommitlint,
		CheckGitCliff, CheckTreefmt, CheckCompose, CheckTypos, CheckYamllint,
		CheckMarkdownlint, CheckPrettier, CheckEditorconfigChecker, CheckLychee,
		CheckZizmor, CheckCodecov, CheckMkDocs:
		return true
	default:
		return false
	}
}

// Threshold reports whether a check is judged against a duration its type
// declares rather than against a present-or-absent expectation: the Renovate
// minimum release age. A type gives it a duration such as "7 days",
// not_required or info; an override may replace the duration with another.
func (c Check) Threshold() bool {
	return c == CheckRenovateMinReleaseAge
}

// Type names the [types.*] table in repos.toml that a repository is judged
// against. A type never implies a visibility.
type Type string
