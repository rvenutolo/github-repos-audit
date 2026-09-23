// Package audit holds the facts one run collected about each repository. It
// derives nothing: internal/github fills these types in, internal/rules turns
// them into verdicts, and internal/render decides what a verdict looks like.
//
// The JSON tags are an interface, not an implementation detail. audit.json is
// committed and diffed, so `git log -p audit.json` is a history of the account
// over time, and a renamed key rewrites that history.
package audit

import "time"

// Snapshot is one complete reading of the account.
type Snapshot struct {
	// GeneratedAt is when the run started, in UTC.
	GeneratedAt time.Time `json:"generated_at"`
	// Owner is the account every repository belongs to: the login of the token
	// the run read with.
	Owner string `json:"owner"`
	// Types is every repository type repos.toml defines: type name, then check
	// name, then the word saying what that type expects of that check. It is
	// recorded so audit.json describes the standard each repository was held
	// to, not only the name of it.
	Types map[string]map[string]string `json:"types"`
	// Repos is sorted by name, case-insensitively.
	Repos []Repo `json:"repos"`
}

// Repo is everything known about one repository: what repos.toml declares and
// what GitHub answered.
type Repo struct {
	// Name is the repository name on GitHub.
	Name string `json:"name"`
	// Type is the declared repos.toml type.
	Type string `json:"type"`
	// Published is the declared repos.toml flag.
	Published bool `json:"published"`
	// Overrides is the declared repos.toml override list, keyed by check name.
	// It is recorded here so audit.json describes the standard each repository
	// was actually held to, and so the report can show an excused gap rather
	// than silently omit it.
	Overrides map[string]string `json:"overrides,omitzero"`

	// Visibility is "public" or "private", read live and never declared.
	Visibility string `json:"visibility"`
	// Description is the repository description, empty when unset.
	Description string `json:"description,omitzero"`
	// Homepage is the repository homepage URL, empty when unset.
	Homepage string `json:"homepage,omitzero"`
	// Topics is how many topics the repository carries.
	Topics int `json:"topics"`
	// License is the SPDX identifier GitHub detected. "NOASSERTION" means it
	// found a license file it could not identify, which is not a pass.
	License string `json:"license,omitzero"`
	// OpenPullRequests is the count of open pull requests.
	OpenPullRequests int `json:"open_pull_requests"`
	// Branches is how many branches the repository has, the default branch
	// included. It is the raw count GitHub reports; subtracting the default
	// branch is internal/rules' business, not this package's. Zero for an
	// empty repository, which has no branches at all.
	Branches int `json:"branches"`
	// PushedAt is the last push to any branch, in UTC.
	PushedAt time.Time `json:"pushed_at,omitzero"`

	// DefaultBranch is the live default branch name, empty for an empty
	// repository.
	DefaultBranch string `json:"default_branch,omitzero"`
	// HeadOID is the default branch's head commit, all forty characters.
	// Abbreviating it would silently break the smoke suite's cross-check,
	// which queries actions/runs?head_sha= and answers total_count: 0 for a
	// short SHA, with a 200.
	HeadOID string `json:"head_oid,omitzero"`
	// Empty reports a repository with no commits at all — GraphQL answered
	// defaultBranchRef: null. That is an ordinary answer, not a failure: the
	// branch-derived cells are unknown and everything else still applies.
	Empty bool `json:"empty,omitzero"`

	// Files records which of the probed paths exist at HEAD.
	Files Files `json:"files"`
	// Renovate is what the Renovate configuration says, when there is one.
	Renovate Renovate `json:"renovate,omitzero"`
	// CIState is the statusCheckRollup state of the head commit: SUCCESS,
	// FAILURE, PENDING, ERROR or EXPECTED. Empty when the rollup was null,
	// which means no check has run on that commit.
	CIState string `json:"ci_state,omitzero"`
	// Releases summarises the repository's releases.
	Releases Releases `json:"releases"`
	// Rulesets is every ruleset defined on the repository.
	Rulesets []Ruleset `json:"rulesets,omitzero"`
	// Branch is the rule set as evaluated for the default branch.
	Branch BranchRules `json:"branch"`
	// Settings is every repository setting the modal computation tracks.
	Settings Settings `json:"settings"`
}

// Renovate is what the Renovate configuration says beyond where it lives
// (Files.RenovateConfig). Zero when there is no configuration.
type Renovate struct {
	// MinReleaseAge is the effective top-level minimumReleaseAge as written,
	// such as "7 days". Empty when unset, explicitly null, or unresolved.
	MinReleaseAge string `json:"min_release_age,omitzero"`
	// MinReleaseAgeSource is where that value was set: the config path, or
	// the preset reference as written. Empty when unset.
	MinReleaseAgeSource string `json:"min_release_age_source,omitzero"`
	// MinReleaseAgeError is why the value could not be decided. Empty
	// otherwise.
	MinReleaseAgeError string `json:"min_release_age_error,omitzero"`
}

// Files records the outcome of every file-existence probe. A probe answers
// null for a path that does not exist, so an absent file is an ordinary
// answer rather than an error.
type Files struct {
	// README is true when any of the six accepted README paths exists. Probes
	// are case-sensitive, so a differently-cased file reads as missing.
	README bool `json:"readme"`
	// Gitignore matches `.gitignore` exactly. A looser match would report
	// web-app's typo'd `.gitigore` as a pass, which is the very thing
	// this report exists to catch.
	Gitignore bool `json:"gitignore"`
	// Editorconfig matches `.editorconfig`.
	Editorconfig bool `json:"editorconfig"`
	// FlakeNix matches `flake.nix`.
	FlakeNix bool `json:"flake_nix"`
	// Justfile is satisfied by either `.justfile` or `justfile`.
	Justfile bool `json:"justfile"`
	// Changelog matches `CHANGELOG.md`.
	Changelog bool `json:"changelog"`
	// Security, Contributing and CodeOfConduct are each probed at all three
	// locations GitHub itself recognises: the root, `.github/` and `docs/`.
	Security      bool `json:"security"`
	Contributing  bool `json:"contributing"`
	CodeOfConduct bool `json:"code_of_conduct"`
	// RenovateConfig is where the Renovate configuration lives, the first of
	// the seven accepted paths to match. Empty when there is none.
	RenovateConfig string `json:"renovate_config,omitzero"`
	// Workflows lists the entries of `.github/workflows`, sorted.
	Workflows []string `json:"workflows,omitzero"`
}

// Releases summarises a repository's releases.
type Releases struct {
	// Total is GraphQL's totalCount, which counts drafts.
	Total int `json:"total"`
	// Drafts is how many of the five most recent releases are drafts. The
	// published count is Total minus Drafts.
	Drafts int `json:"drafts"`
	// LastPublishedAt is when the most recent non-draft release was published,
	// in UTC. Zero when there is none.
	LastPublishedAt time.Time `json:"last_published_at,omitzero"`
}

// Ruleset is one repository ruleset, with the conditions and rules needed to
// tell a tag ruleset from a branch one and an active ruleset from an evaluated
// one.
type Ruleset struct {
	// Name is the ruleset's name, such as "protect-main".
	Name string `json:"name"`
	// Target is BRANCH, TAG or PUSH.
	Target string `json:"target"`
	// Enforcement is ACTIVE, EVALUATE or DISABLED. Only ACTIVE counts.
	Enforcement string `json:"enforcement"`
	// Include is conditions.refName.include, such as ["refs/tags/**"] or
	// ["~DEFAULT_BRANCH"].
	Include []string `json:"include,omitzero"`
	// Rules is every rule type the ruleset carries, such as PULL_REQUEST.
	Rules []string `json:"rules,omitzero"`
}

// BranchRules is the rule set as evaluated for the default branch, which is
// precisely the "can I push straight to it?" question.
type BranchRules struct {
	// Known is false for an empty repository, whose default branch does not
	// exist, so the endpoint is never called. Direct push is then derived from
	// the rulesets instead, and the signed-commits and required-checks cells
	// render n/a: a repository with no commits cannot be judged on what is in
	// them.
	Known bool `json:"known"`
	// Types is every rule type in force on the branch, lowercase as REST
	// spells them: "pull_request", "required_signatures", "required_status_checks".
	Types []string `json:"types,omitzero"`
	// RequiredChecks is the context name of each required status check.
	RequiredChecks []string `json:"required_checks,omitzero"`
}

// Settings is every repository setting the modal computation tracks. Nothing
// here has a declared expected value: the tool computes the most common value
// across the repositories where the setting applies and reports the ones that
// differ.
type Settings struct {
	HasIssues      bool `json:"has_issues"`
	HasWiki        bool `json:"has_wiki"`
	HasProjects    bool `json:"has_projects"`
	HasDiscussions bool `json:"has_discussions"`

	// VulnerabilityAlerts is GraphQL's hasVulnerabilityAlertsEnabled.
	VulnerabilityAlerts bool `json:"vulnerability_alerts"`
	// DependabotSecurityUpdates is REST's automated-security-fixes.
	DependabotSecurityUpdates bool `json:"dependabot_security_updates"`

	// The merge settings come from GraphQL, not REST: REST silently drops them
	// for a token with only read rights, and a token cannot tell you it was
	// handed a trimmed answer.
	MergeCommitAllowed  bool   `json:"merge_commit_allowed"`
	SquashMergeAllowed  bool   `json:"squash_merge_allowed"`
	RebaseMergeAllowed  bool   `json:"rebase_merge_allowed"`
	MergeCommitTitle    string `json:"merge_commit_title,omitzero"`
	MergeCommitMessage  string `json:"merge_commit_message,omitzero"`
	AutoMergeAllowed    bool   `json:"auto_merge_allowed"`
	DeleteBranchOnMerge bool   `json:"delete_branch_on_merge"`

	// SecretScanning is "enabled" or "disabled", and empty when GitHub omitted
	// the security_and_analysis block entirely, which it does for a private
	// repository on a personal plan. Empty means n/a, not disabled.
	SecretScanning string `json:"secret_scanning,omitzero"`
	// SecretScanningPushProtection follows the same convention.
	SecretScanningPushProtection string `json:"secret_scanning_push_protection,omitzero"`
	// PrivateVulnerabilityReporting is nil when the endpoint answered 404,
	// which it does for a private repository. That is n/a, not disabled.
	PrivateVulnerabilityReporting *bool `json:"private_vulnerability_reporting,omitzero"`

	// ActionsPolicy is "all", "local_only" or "selected".
	ActionsPolicy string `json:"actions_policy,omitzero"`
	// SHAPinningRequired is the actions/permissions sha_pinning_required flag.
	SHAPinningRequired bool `json:"sha_pinning_required"`
	// AllowedActions is nil unless ActionsPolicy is "selected", where the
	// selected-actions endpoint answers 404 for any other policy.
	AllowedActions *AllowedActions `json:"allowed_actions,omitzero"`
	// DefaultWorkflowPermissions is "read" or "write".
	DefaultWorkflowPermissions string `json:"default_workflow_permissions,omitzero"`
	// CanApprovePullRequests is whether the workflow token may approve pull
	// requests.
	CanApprovePullRequests bool `json:"can_approve_pull_requests"`
	// ActionsAccessLevel is whether this repository's actions are reusable from
	// other repositories. Empty for a public repository, where the endpoint
	// answers 422 — the setting does not exist there.
	ActionsAccessLevel string `json:"actions_access_level,omitzero"`
}

// AllowedActions is the Actions allowlist, present only under the "selected"
// policy.
type AllowedActions struct {
	GitHubOwnedAllowed bool `json:"github_owned_allowed"`
	VerifiedAllowed    bool `json:"verified_allowed"`
	// Patterns is the allowlist itself, sorted.
	Patterns []string `json:"patterns,omitzero"`
}
