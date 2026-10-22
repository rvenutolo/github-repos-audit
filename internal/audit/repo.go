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
	// Identity is repos.toml's [identity] table, recorded so audit.json says
	// which identity the history was held to. Absent when nothing judges
	// git_identity.
	Identity IdentityStandard `json:"identity,omitzero"`
	// Hermetic is repos.toml's [hermetic] table, recorded so audit.json says
	// which flake and lint hermetic_nix looked for. Absent when nothing judges
	// hermetic_nix.
	Hermetic HermeticStandard `json:"hermetic,omitzero"`
	// Repos is sorted by name, case-insensitively.
	Repos []Repo `json:"repos"`
	// Drift is where repos.toml and the account disagree about which
	// repositories exist. Absent when they agree, so a clean account's
	// audit.json carries no trace of it.
	Drift ConfigDrift `json:"config_drift,omitzero"`
}

// ConfigDrift is the disagreement between repos.toml and the account. Neither
// list stops a run: the repositories both sides name are audited, and these
// are reported so the config gets fixed.
type ConfigDrift struct {
	// Unconfigured is every repository the account has that repos.toml has no
	// entry for, sorted by name case-insensitively. Such a repository is
	// named and nothing more: with no type there is nothing to judge it
	// against, so it is not read.
	Unconfigured []string `json:"unconfigured,omitzero"`
	// StaleEntries is every repos.toml entry naming a repository the account
	// does not have: deleted, archived, or turned into a fork. Sorted by name
	// case-insensitively.
	StaleEntries []string `json:"stale_entries,omitzero"`
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
	// HeadOID is the default branch's head commit, all forty characters;
	// TestLive_headShaNeedsTheFullOID in internal/github is why it is never
	// abbreviated.
	HeadOID string `json:"head_oid,omitzero"`
	// Empty reports a repository with no commits at all: GraphQL answered
	// defaultBranchRef: null, an ordinary answer rather than a failure. See
	// toAudit in internal/github/collect.go for what still applies.
	Empty bool `json:"empty,omitzero"`
	// Identities is every distinct author and committer identity on the
	// default branch's full history, exactly as the commits spell them,
	// sorted by name then email. It includes other people and bots: which of
	// these are the account holder's, and whether they are right, is
	// internal/rules' decision. No counts or dates, deliberately — they would
	// change on every push and make audit.json churn.
	Identities []Identity `json:"identities,omitzero"`

	// Files records which of the probed paths exist at HEAD.
	Files Files `json:"files"`
	// Renovate is what the Renovate configuration says, when there is one.
	Renovate Renovate `json:"renovate,omitzero"`
	// Flake is what flake.nix and flake.lock say, and which files name the
	// hermetic lint. Zero when nothing in either file was recorded and no
	// gate file names the lint. That includes a readable flake.nix that binds
	// no URL and names no lint, so Files.FlakeNix, not this, says whether
	// flake.nix exists.
	Flake Flake `json:"flake,omitzero"`
	// Workflows is every .yml or .yaml file in .github/workflows, parsed,
	// sorted by path. Files.Workflows is the directory's listing; this is
	// what the files say.
	Workflows []Workflow `json:"workflow_facts,omitzero"`
	// Actions is every composite action in .github/actions, one per
	// directory holding an action.yml or action.yaml, sorted by path.
	Actions []CompositeAction `json:"composite_actions,omitzero"`
	// CIState is the statusCheckRollup state of the head commit: SUCCESS,
	// FAILURE, PENDING, ERROR or EXPECTED. Empty when the rollup was null,
	// which means no check has run on that commit.
	CIState string `json:"ci_state,omitzero"`
	// CI is the timing of the repository's recent CI, commit by commit. Zero
	// for a repository with no merged pull requests and no commits.
	CI CI `json:"ci,omitzero"`
	// Storage is what the repository holds in GitHub Actions storage. Zero
	// for an empty repository, which is not read for it, and for one whose
	// four answers (caches, cache usage, cache limit, artifacts) are all
	// zero, which omitzero drops alike.
	Storage ActionsStorage `json:"actions_storage,omitzero"`
	// Releases summarises the repository's releases.
	Releases Releases `json:"releases"`
	// Rulesets is every ruleset defined on the repository.
	Rulesets []Ruleset `json:"rulesets,omitzero"`
	// Branch is the rule set as evaluated for the default branch.
	Branch BranchRules `json:"branch"`
	// Settings is every live repository setting: what the modal computation
	// tracks, plus secret scanning and private vulnerability reporting, which
	// are judged as checks.
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
	// NixManager is whether Renovate's nix manager runs: nix.enabled true
	// after the same merge, with enabledManagers unset or listing "nix".
	// False when off, unset, of the wrong type, or unresolved (the reason is
	// then in MinReleaseAgeError; a wrong-typed manager key never sets it).
	NixManager bool `json:"nix_manager,omitzero"`
	// LockFileMaintenance is whether lockFileMaintenance.enabled is true
	// after the same merge, which for this key also reads the built-in
	// presets the README lists under nix_lock_refresh. False when off, unset,
	// of the wrong type, or unresolved.
	LockFileMaintenance bool `json:"lock_file_maintenance,omitzero"`
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
	// Justfile is true when JustfilePath is set. just matches the name
	// case-insensitively, so `Justfile` counts.
	Justfile bool `json:"justfile"`
	// Changelog matches `CHANGELOG.md`.
	Changelog bool `json:"changelog"`
	// Security, Contributing and CodeOfConduct are each probed at all three
	// locations GitHub itself recognises: the root, `.github/` and `docs/`.
	Security      bool `json:"security"`
	Contributing  bool `json:"contributing"`
	CodeOfConduct bool `json:"code_of_conduct"`
	// RenovateConfig is where the Renovate configuration lives, the first of
	// the accepted paths to match in Renovate's own lookup order. Empty when
	// there is none.
	RenovateConfig string `json:"renovate_config,omitzero"`
	// Workflows lists the entries of `.github/workflows`, sorted.
	Workflows []string `json:"workflows,omitzero"`

	// The fields below are where a file was found in the repository's tree at
	// HEAD, as the tree spells it. Empty means no accepted name is present. A
	// directory match carries a trailing slash. Which names each tool accepts
	// is internal/filepaths' table.

	// JustfilePath is the justfile, matched case-insensitively.
	JustfilePath string `json:"justfile_path,omitzero"`
	// Commitlint is the commitlint configuration.
	Commitlint string `json:"commitlint,omitzero"`
	// GitCliff is the git-cliff configuration.
	GitCliff string `json:"git_cliff,omitzero"`
	// Treefmt is the treefmt configuration.
	Treefmt string `json:"treefmt,omitzero"`
	// Compose lists the distinct compose file names found at any depth,
	// sorted.
	Compose []string `json:"compose,omitzero"`
	// Typos is the typos configuration.
	Typos string `json:"typos,omitzero"`
	// Yamllint is the yamllint configuration.
	Yamllint string `json:"yamllint,omitzero"`
	// Markdownlint is the markdownlint configuration.
	Markdownlint string `json:"markdownlint,omitzero"`
	// Prettier is the Prettier configuration.
	Prettier string `json:"prettier,omitzero"`
	// EditorconfigChecker is the editorconfig-checker configuration.
	EditorconfigChecker string `json:"editorconfig_checker,omitzero"`
	// Lychee is the lychee configuration.
	Lychee string `json:"lychee,omitzero"`
	// Zizmor is the zizmor configuration.
	Zizmor string `json:"zizmor,omitzero"`
	// Codecov is the Codecov configuration.
	Codecov string `json:"codecov,omitzero"`
	// MkDocs is the MkDocs configuration.
	MkDocs string `json:"mkdocs,omitzero"`
	// LicenseFile is the license text, or the `LICENSES/` directory.
	LicenseFile string `json:"license_file,omitzero"`
	// SecurityPolicy is the security policy, matched case-insensitively.
	SecurityPolicy string `json:"security_policy,omitzero"`
	// Codeowners is the CODEOWNERS file.
	Codeowners string `json:"codeowners,omitzero"`
	// PRTemplate is the pull request template file or directory.
	PRTemplate string `json:"pr_template,omitzero"`
	// ClaudeMD is the Claude Code memory file.
	ClaudeMD string `json:"claude_md,omitzero"`
	// GoMod reports a file named exactly `go.mod` at the root.
	GoMod bool `json:"go_mod,omitzero"`
}

// CI is the timing of a repository's recent CI, as recorded per commit. It
// carries no means and makes no selection: which commits a figure is taken
// over, and what an incomplete or bot-authored one contributes, is
// internal/rules' decision. Both lists are longer than any sample the report
// takes, so a decision that skips some entries still has enough left.
type CI struct {
	// PullRequests is the head commit of each of the latest merged pull requests,
	// in the order GitHub listed them (most recently updated first). That is
	// not merge order, which is why each entry carries MergedAt.
	PullRequests []CICommit `json:"pull_requests,omitzero"`
	// DefaultBranch is the most recent default-branch commits, newest first,
	// whether or not any workflow ran on them. Empty for an empty repository.
	DefaultBranch []CICommit `json:"default_branch,omitzero"`
	// LastDefaultBranchRun is when the newest workflow run on the default
	// branch from this repository, not a fork's pull request from a branch of
	// the same name, was created, of any event, in UTC. Zero when no such run
	// is among the newest hundred runs on that branch name, and for an empty
	// repository.
	LastDefaultBranchRun time.Time `json:"last_default_branch_run,omitzero"`
}

// CICommit is one commit's GitHub Actions workflow runs, reduced to
// timestamp arithmetic. A check another app posts is not a workflow run and
// is not counted.
type CICommit struct {
	// OID is the commit, all forty characters.
	OID string `json:"oid"`
	// PRNumber is the pull request this commit was the head of. Zero for a
	// default-branch entry.
	PRNumber int `json:"pr_number,omitzero"`
	// MergedAt is when that pull request merged, in UTC. Zero for a
	// default-branch entry.
	MergedAt time.Time `json:"merged_at,omitzero"`
	// AuthorBot is whether the pull request's author is a GitHub App's bot
	// account. False for a default-branch entry and for a deleted author.
	AuthorBot bool `json:"author_bot,omitzero"`
	// Complete is whether the commit's runs are a finished whole: at least
	// one workflow run, every run and every job completed with a known
	// completion time, no jobs list cut short by its page size, and at least
	// one job finished. A run that ended without starting a job (a startup
	// failure) leaves no completion to end the wait at. A
	// truncated list might hide the job that finished last, so it is never
	// read as complete.
	Complete bool `json:"complete"`
	// WaitSeconds is the latest job completion over the commit's runs minus
	// the earliest run creation: how long a push waited for every workflow,
	// queue time included and parallel workflows counted once. Zero unless
	// Complete.
	WaitSeconds int64 `json:"wait_seconds,omitzero"`
	// Jobs is every job that completed with both timestamps, in the order
	// GitHub listed them.
	Jobs []CIJob `json:"jobs,omitzero"`
}

// CIJob is one workflow job's duration.
type CIJob struct {
	// Label is "<workflow> / <job>".
	Label string `json:"label"`
	// Seconds is the job's completion minus its start: time on a runner, not
	// time queued for one.
	Seconds int64 `json:"seconds"`
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
	// Strict is true when any required_status_checks rule in force asks for a
	// pull request's branch to be up to date with the base before merging.
	// GitHub applies the setting only with at least one required check, so
	// true without RequiredChecks enforces nothing.
	Strict bool `json:"strict,omitzero"`
}

// Settings is every live repository setting. Most feed the modal computation
// (see internal/rules/modal.go); SecretScanning and
// PrivateVulnerabilityReporting are judged as the secret_scanning and
// vuln_reporting checks instead.
type Settings struct {
	HasIssues      bool `json:"has_issues"`
	HasWiki        bool `json:"has_wiki"`
	HasProjects    bool `json:"has_projects"`
	HasDiscussions bool `json:"has_discussions"`

	// VulnerabilityAlerts is GraphQL's hasVulnerabilityAlertsEnabled.
	VulnerabilityAlerts bool `json:"vulnerability_alerts"`
	// DependabotSecurityUpdates is REST's automated-security-fixes.
	DependabotSecurityUpdates bool `json:"dependabot_security_updates"`

	// The merge settings come from GraphQL, not REST; see restRepo in
	// internal/github/rest.go for why.
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
	// AllowedActions is nil unless ActionsPolicy is "selected": the
	// selected-actions endpoint answers 404 under any other policy, and 409
	// under "all".
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

// Identity is one git identity as a commit records it, for its author or its
// committer. It is recorded exactly as the commit spells it: deciding that two
// spellings are the same person is internal/rules' business.
type Identity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// IdentityStandard is repos.toml's [identity] table: the one identity the
// account's own commits should carry, and the regular expression that says
// which collected identities are the account holder's at all. It is recorded
// on the snapshot so audit.json describes the standard it was judged by.
type IdentityStandard struct {
	// Canonical is written "Name <email>".
	Canonical string `json:"canonical"`
	// Match is an RE2 expression, unanchored, tested against "Name <email>".
	Match string `json:"match"`
	// Accepted is further identities, each written "Name <email>", that are
	// the account holder's and acceptable in a history although they are not
	// canonical: the account's GitHub noreply identity, typically, which every
	// merge made in the web UI adds back, so it can never be rewritten away.
	// Nil when nothing beyond Canonical is accepted.
	Accepted []string `json:"accepted,omitzero"`
}

// HermeticStandard is repos.toml's [hermetic] table: the flake that carries
// the hermetic lint, and the lint's command name. Both are configuration, not
// constants, because the flake names an account and a repository. It is
// recorded on the snapshot so audit.json describes the standard it was judged
// by.
type HermeticStandard struct {
	// Flake is the input URL as flake.nix writes it, such as
	// "github:<owner>/<repo>".
	Flake string `json:"flake"`
	// Lint is the hermetic-lint command name flake.nix and the gate files are
	// expected to mention.
	Lint string `json:"lint"`
}
