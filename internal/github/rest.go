package github

import (
	"context"
	"net/http"
	"net/url"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// restRepo is the slice of the repository object this tool reads. Only
// security_and_analysis comes from here: the merge settings are read over
// GraphQL instead, because REST silently drops allow_merge_commit and its
// siblings for a token with only read rights and a token cannot tell you it
// was handed a trimmed answer.
type restRepo struct {
	SecurityAndAnalysis *securityAndAnalysis `json:"security_and_analysis"`
}

// securityAndAnalysis is absent, or present and null, for a private repository
// on a personal plan, because secret scanning there needs the paid add-on.
// That is n/a rather than disabled, which is why every field is a pointer.
type securityAndAnalysis struct {
	SecretScanning               *analysisStatus `json:"secret_scanning"`
	SecretScanningPushProtection *analysisStatus `json:"secret_scanning_push_protection"`
}

type analysisStatus struct {
	Status string `json:"status"`
}

// enabledFlag is the shape of both private-vulnerability-reporting and
// automated-security-fixes.
type enabledFlag struct {
	Enabled bool `json:"enabled"`
}

type actionsPermissions struct {
	AllowedActions     string `json:"allowed_actions"`
	SHAPinningRequired bool   `json:"sha_pinning_required"`
}

type selectedActions struct {
	GitHubOwnedAllowed bool     `json:"github_owned_allowed"`
	VerifiedAllowed    bool     `json:"verified_allowed"`
	PatternsAllowed    []string `json:"patterns_allowed"`
}

type workflowPermissions struct {
	DefaultWorkflowPermissions   string `json:"default_workflow_permissions"`
	CanApprovePullRequestReviews bool   `json:"can_approve_pull_request_reviews"`
}

type actionsAccess struct {
	AccessLevel string `json:"access_level"`
}

// branchRule is one entry of rules/branches/{branch}, which answers with the
// rules as evaluated for that branch — precisely the "can I push straight to
// it?" question.
type branchRule struct {
	Type       string          `json:"type"`
	Parameters *ruleParameters `json:"parameters"`
}

type ruleParameters struct {
	RequiredStatusChecks []requiredStatusCheck `json:"required_status_checks"`
}

type requiredStatusCheck struct {
	Context string `json:"context"`
}

// repoPath builds /repos/{owner}/{name}. Both segments are escaped: they reach
// this package from repos.toml and from GitHub, and a path built by
// concatenation is how an unexpected name becomes an unexpected request.
func (c *Client) repoPath(name string) string {
	return "/repos/" + url.PathEscape(c.owner) + "/" + url.PathEscape(name)
}

// fetchREST makes the per-repository REST calls and fills the parts of repo
// that GraphQL does not answer. There are eight, all GET, and seven for an
// empty repository, whose default branch does not exist.
func (c *Client) fetchREST(ctx context.Context, name string, repo *audit.Repo) error {
	base := c.repoPath(name)

	if err := c.fetchSecurityAnalysis(ctx, base, repo); err != nil {
		return err
	}
	if err := c.fetchVulnerabilityReporting(ctx, base, repo); err != nil {
		return err
	}
	if err := c.fetchSecurityFixes(ctx, base, repo); err != nil {
		return err
	}
	if err := c.fetchActionsPermissions(ctx, base, repo); err != nil {
		return err
	}
	if err := c.fetchSelectedActions(ctx, base, repo); err != nil {
		return err
	}
	if err := c.fetchWorkflowPermissions(ctx, base, repo); err != nil {
		return err
	}
	if err := c.fetchActionsAccess(ctx, base, repo); err != nil {
		return err
	}
	return c.fetchBranchRules(ctx, base, repo)
}

// fetchSecurityAnalysis reads secret scanning and push protection. A missing
// or null security_and_analysis block leaves both empty, which the model reads
// as n/a rather than disabled.
func (c *Client) fetchSecurityAnalysis(ctx context.Context, base string, repo *audit.Repo) error {
	var body restRepo
	if _, err := c.getJSON(ctx, base, &body); err != nil {
		return err
	}
	if body.SecurityAndAnalysis == nil {
		return nil
	}
	if s := body.SecurityAndAnalysis.SecretScanning; s != nil {
		repo.Settings.SecretScanning = s.Status
	}
	if s := body.SecurityAndAnalysis.SecretScanningPushProtection; s != nil {
		repo.Settings.SecretScanningPushProtection = s.Status
	}
	return nil
}

// fetchVulnerabilityReporting reads private vulnerability reporting. A private
// repository answers 404, which is n/a and leaves the field nil.
func (c *Client) fetchVulnerabilityReporting(ctx context.Context, base string, repo *audit.Repo) error {
	var body enabledFlag
	resp, err := c.getJSON(ctx, base+"/private-vulnerability-reporting", &body, http.StatusNotFound)
	if err != nil {
		return err
	}
	if resp.status == http.StatusNotFound {
		return nil
	}
	repo.Settings.PrivateVulnerabilityReporting = &body.Enabled
	return nil
}

// fetchSecurityFixes reads whether Dependabot security updates are on.
func (c *Client) fetchSecurityFixes(ctx context.Context, base string, repo *audit.Repo) error {
	var body enabledFlag
	if _, err := c.getJSON(ctx, base+"/automated-security-fixes", &body); err != nil {
		return err
	}
	repo.Settings.DependabotSecurityUpdates = body.Enabled
	return nil
}

// fetchActionsPermissions reads the Actions policy and the SHA-pinning flag.
func (c *Client) fetchActionsPermissions(ctx context.Context, base string, repo *audit.Repo) error {
	var body actionsPermissions
	if _, err := c.getJSON(ctx, base+"/actions/permissions", &body); err != nil {
		return err
	}
	repo.Settings.ActionsPolicy = body.AllowedActions
	repo.Settings.SHAPinningRequired = body.SHAPinningRequired
	return nil
}

// fetchSelectedActions reads the Actions allowlist. The endpoint exists only
// under the "selected" policy: it answers 404 under any other one, and 409
// under "all" specifically, and both mean there is no allowlist rather than
// that something went wrong.
func (c *Client) fetchSelectedActions(ctx context.Context, base string, repo *audit.Repo) error {
	var body selectedActions
	resp, err := c.getJSON(ctx, base+"/actions/permissions/selected-actions", &body,
		http.StatusNotFound, http.StatusConflict)
	if err != nil {
		return err
	}
	if resp.status != http.StatusOK {
		return nil
	}
	repo.Settings.AllowedActions = &audit.AllowedActions{
		GitHubOwnedAllowed: body.GitHubOwnedAllowed,
		VerifiedAllowed:    body.VerifiedAllowed,
		Patterns:           sortedUnique(body.PatternsAllowed),
	}
	return nil
}

// fetchWorkflowPermissions reads the workflow token's rights.
func (c *Client) fetchWorkflowPermissions(ctx context.Context, base string, repo *audit.Repo) error {
	var body workflowPermissions
	if _, err := c.getJSON(ctx, base+"/actions/permissions/workflow", &body); err != nil {
		return err
	}
	repo.Settings.DefaultWorkflowPermissions = body.DefaultWorkflowPermissions
	repo.Settings.CanApprovePullRequests = body.CanApprovePullRequestReviews
	return nil
}

// fetchActionsAccess reads whether this repository's actions are reusable from
// others. The setting does not exist on a public repository, where the
// endpoint answers 422; that is n/a and leaves the field empty.
func (c *Client) fetchActionsAccess(ctx context.Context, base string, repo *audit.Repo) error {
	var body actionsAccess
	resp, err := c.getJSON(ctx, base+"/actions/permissions/access", &body, http.StatusUnprocessableEntity)
	if err != nil {
		return err
	}
	if resp.status == http.StatusUnprocessableEntity {
		return nil
	}
	repo.Settings.ActionsAccessLevel = body.AccessLevel
	return nil
}

// fetchBranchRules reads the rules in force on the default branch. An empty
// repository has no default branch, so the endpoint is skipped entirely and
// BranchRules.Known stays false: direct push is then derived from the rulesets
// GraphQL already returned, and the signed-commit and required-check cells
// render n/a, because a repository with no commits cannot be judged on what is
// in them.
func (c *Client) fetchBranchRules(ctx context.Context, base string, repo *audit.Repo) error {
	if repo.Empty || repo.DefaultBranch == "" {
		return nil
	}

	var rules []branchRule
	path := base + "/rules/branches/" + url.PathEscape(repo.DefaultBranch)
	if _, err := c.getJSON(ctx, path, &rules); err != nil {
		return err
	}

	types := make([]string, 0, len(rules))
	var checks []string
	for _, r := range rules {
		types = append(types, r.Type)
		if r.Parameters == nil {
			continue
		}
		for _, check := range r.Parameters.RequiredStatusChecks {
			checks = append(checks, check.Context)
		}
	}

	repo.Branch = audit.BranchRules{
		Known:          true,
		Types:          sortedUnique(types),
		RequiredChecks: sortedUnique(checks),
	}
	return nil
}
