package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// graphQLPath is the one endpoint this client posts to. GitHub's GraphQL API
// accepts no other verb, which is why the read-only test asserts the operation
// in the document rather than banning POST outright.
const graphQLPath = "/graphql"

// repoQuery is the single query sent per repository. Every field is aliased to
// snake_case so the decoded shape matches the project's JSON convention and no
// struct tag has to carry GitHub's camelCase.
//
// The file probes use object(expression: "HEAD:<path>"), which answers null for
// a path that does not exist: an absent file is an ordinary answer, not an
// error, and an empty repository answers null for all of them at once because
// HEAD does not resolve. The probes are case-sensitive, so a differently-cased
// README reads as missing, which is deliberate.
//
// isArchived and isFork are not requested. Discovery already filters on the
// REST list's fork and archived flags and audit.Repo records neither, so asking
// for them here would collect a field the report can never show.
const repoQuery = `query RepoAudit($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    description
    homepage_url: homepageUrl
    visibility
    pushed_at: pushedAt
    license_info: licenseInfo {
      spdx_id: spdxId
    }
    topics: repositoryTopics(first: 100) {
      total_count: totalCount
    }
    open_pull_requests: pullRequests(states: OPEN) {
      total_count: totalCount
    }
    branches: refs(refPrefix: "refs/heads/", first: 1) {
      total_count: totalCount
    }
    has_issues: hasIssuesEnabled
    has_wiki: hasWikiEnabled
    has_projects: hasProjectsEnabled
    has_discussions: hasDiscussionsEnabled
    vulnerability_alerts: hasVulnerabilityAlertsEnabled
    merge_commit_allowed: mergeCommitAllowed
    squash_merge_allowed: squashMergeAllowed
    rebase_merge_allowed: rebaseMergeAllowed
    auto_merge_allowed: autoMergeAllowed
    delete_branch_on_merge: deleteBranchOnMerge
    merge_commit_title: mergeCommitTitle
    merge_commit_message: mergeCommitMessage
    default_branch_ref: defaultBranchRef {
      name
      target {
        ... on Commit {
          oid
          status_check_rollup: statusCheckRollup {
            state
          }
        }
      }
    }
    rulesets(first: 20) {
      nodes {
        name
        target
        enforcement
        conditions {
          ref_name: refName {
            include
          }
        }
        rules(first: 50) {
          nodes {
            type
          }
        }
      }
    }
    releases(first: 5, orderBy: { field: CREATED_AT, direction: DESC }) {
      total_count: totalCount
      nodes {
        is_draft: isDraft
        published_at: publishedAt
      }
    }
    readme_md: object(expression: "HEAD:README.md") { oid }
    readme_github_md: object(expression: "HEAD:.github/README.md") { oid }
    readme_docs_md: object(expression: "HEAD:docs/README.md") { oid }
    readme_rst: object(expression: "HEAD:README.rst") { oid }
    readme_txt: object(expression: "HEAD:README.txt") { oid }
    readme_plain: object(expression: "HEAD:README") { oid }
    gitignore: object(expression: "HEAD:.gitignore") { oid }
    editorconfig: object(expression: "HEAD:.editorconfig") { oid }
    flake_nix: object(expression: "HEAD:flake.nix") { oid }
    changelog_md: object(expression: "HEAD:CHANGELOG.md") { oid }
    justfile_dot: object(expression: "HEAD:.justfile") { oid }
    justfile_plain: object(expression: "HEAD:justfile") { oid }
    security_root: object(expression: "HEAD:SECURITY.md") { oid }
    security_github: object(expression: "HEAD:.github/SECURITY.md") { oid }
    security_docs: object(expression: "HEAD:docs/SECURITY.md") { oid }
    contributing_root: object(expression: "HEAD:CONTRIBUTING.md") { oid }
    contributing_github: object(expression: "HEAD:.github/CONTRIBUTING.md") { oid }
    contributing_docs: object(expression: "HEAD:docs/CONTRIBUTING.md") { oid }
    coc_root: object(expression: "HEAD:CODE_OF_CONDUCT.md") { oid }
    coc_github: object(expression: "HEAD:.github/CODE_OF_CONDUCT.md") { oid }
    coc_docs: object(expression: "HEAD:docs/CODE_OF_CONDUCT.md") { oid }
    renovate_json: object(expression: "HEAD:renovate.json") { oid }
    renovate_json5: object(expression: "HEAD:renovate.json5") { oid }
    renovaterc: object(expression: "HEAD:.renovaterc") { oid }
    renovaterc_json: object(expression: "HEAD:.renovaterc.json") { oid }
    renovaterc_json5: object(expression: "HEAD:.renovaterc.json5") { oid }
    renovate_github_json: object(expression: "HEAD:.github/renovate.json") { oid }
    renovate_github_json5: object(expression: "HEAD:.github/renovate.json5") { oid }
    workflows: object(expression: "HEAD:.github/workflows") {
      ... on Tree {
        entries {
          name
        }
      }
    }
  }
}`

// graphQLRequest is the POST body. Variables carry the owner and name rather
// than being interpolated into the document, so a repository name can never be
// read as query text.
type graphQLRequest struct {
	Query     string            `json:"query"`
	Variables map[string]string `json:"variables"`
}

// graphQLResponse is GitHub's envelope. A GraphQL failure arrives as a 200 with
// a non-empty errors array, so the status alone never says whether the answer
// is usable.
type graphQLResponse struct {
	Data   graphQLData    `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type graphQLData struct {
	Repository *repository `json:"repository"`
}

type graphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// gitObject is a file probe's answer. Only its presence matters; oid is
// selected because object returns an interface and a selection set is
// required.
type gitObject struct {
	OID string `json:"oid"`
}

// tree is the .github/workflows listing.
type tree struct {
	Entries []treeEntry `json:"entries"`
}

type treeEntry struct {
	Name string `json:"name"`
}

// repository is the decoded shape of the query above.
type repository struct {
	Description         string       `json:"description"`
	HomepageURL         string       `json:"homepage_url"`
	Visibility          string       `json:"visibility"`
	PushedAt            *time.Time   `json:"pushed_at"`
	LicenseInfo         *licenseInfo `json:"license_info"`
	Topics              countOf      `json:"topics"`
	OpenPullRequests    countOf      `json:"open_pull_requests"`
	Branches            countOf      `json:"branches"`
	HasIssues           bool         `json:"has_issues"`
	HasWiki             bool         `json:"has_wiki"`
	HasProjects         bool         `json:"has_projects"`
	HasDiscussions      bool         `json:"has_discussions"`
	VulnerabilityAlerts bool         `json:"vulnerability_alerts"`

	MergeCommitAllowed  bool   `json:"merge_commit_allowed"`
	SquashMergeAllowed  bool   `json:"squash_merge_allowed"`
	RebaseMergeAllowed  bool   `json:"rebase_merge_allowed"`
	AutoMergeAllowed    bool   `json:"auto_merge_allowed"`
	DeleteBranchOnMerge bool   `json:"delete_branch_on_merge"`
	MergeCommitTitle    string `json:"merge_commit_title"`
	MergeCommitMessage  string `json:"merge_commit_message"`

	DefaultBranchRef *branchRef  `json:"default_branch_ref"`
	Rulesets         rulesetList `json:"rulesets"`
	Releases         releaseList `json:"releases"`

	ReadmeMD       *gitObject `json:"readme_md"`
	ReadmeGitHubMD *gitObject `json:"readme_github_md"`
	ReadmeDocsMD   *gitObject `json:"readme_docs_md"`
	ReadmeRST      *gitObject `json:"readme_rst"`
	ReadmeTXT      *gitObject `json:"readme_txt"`
	ReadmePlain    *gitObject `json:"readme_plain"`

	Gitignore    *gitObject `json:"gitignore"`
	Editorconfig *gitObject `json:"editorconfig"`
	FlakeNix     *gitObject `json:"flake_nix"`
	ChangelogMD  *gitObject `json:"changelog_md"`

	JustfileDot   *gitObject `json:"justfile_dot"`
	JustfilePlain *gitObject `json:"justfile_plain"`

	SecurityRoot       *gitObject `json:"security_root"`
	SecurityGitHub     *gitObject `json:"security_github"`
	SecurityDocs       *gitObject `json:"security_docs"`
	ContributingRoot   *gitObject `json:"contributing_root"`
	ContributingGitHub *gitObject `json:"contributing_github"`
	ContributingDocs   *gitObject `json:"contributing_docs"`
	CoCRoot            *gitObject `json:"coc_root"`
	CoCGitHub          *gitObject `json:"coc_github"`
	CoCDocs            *gitObject `json:"coc_docs"`

	RenovateJSON        *gitObject `json:"renovate_json"`
	RenovateJSON5       *gitObject `json:"renovate_json5"`
	RenovateRC          *gitObject `json:"renovaterc"`
	RenovateRCJSON      *gitObject `json:"renovaterc_json"`
	RenovateRCJSON5     *gitObject `json:"renovaterc_json5"`
	RenovateGitHubJSON  *gitObject `json:"renovate_github_json"`
	RenovateGitHubJSON5 *gitObject `json:"renovate_github_json5"`

	Workflows *tree `json:"workflows"`
}

type licenseInfo struct {
	SPDXID string `json:"spdx_id"`
}

type countOf struct {
	TotalCount int `json:"total_count"`
}

type branchRef struct {
	Name   string  `json:"name"`
	Target *commit `json:"target"`
}

type commit struct {
	OID               string       `json:"oid"`
	StatusCheckRollup *rollupState `json:"status_check_rollup"`
}

type rollupState struct {
	State string `json:"state"`
}

type rulesetList struct {
	Nodes []rulesetNode `json:"nodes"`
}

type rulesetNode struct {
	Name        string             `json:"name"`
	Target      string             `json:"target"`
	Enforcement string             `json:"enforcement"`
	Conditions  *rulesetConditions `json:"conditions"`
	Rules       ruleList           `json:"rules"`
}

type rulesetConditions struct {
	RefName *refNameCondition `json:"ref_name"`
}

type refNameCondition struct {
	Include []string `json:"include"`
}

type ruleList struct {
	Nodes []ruleNode `json:"nodes"`
}

type ruleNode struct {
	Type string `json:"type"`
}

type releaseList struct {
	TotalCount int           `json:"total_count"`
	Nodes      []releaseNode `json:"nodes"`
}

type releaseNode struct {
	IsDraft     bool       `json:"is_draft"`
	PublishedAt *time.Time `json:"published_at"`
}

// fetchRepository runs repoQuery for one repository.
func (c *Client) fetchRepository(ctx context.Context, name string) (*repository, error) {
	body, err := json.Marshal(graphQLRequest{
		Query:     repoQuery,
		Variables: map[string]string{"owner": c.owner, "name": name},
	})
	if err != nil {
		return nil, fmt.Errorf("encode graphql request: %w", err)
	}

	resp, err := c.doRetrying(ctx, http.MethodPost, c.baseURL+graphQLPath, body, graphQLRateLimited)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", graphQLPath, err)
	}
	if resp.status != http.StatusOK {
		return nil, fmt.Errorf("%s: %w %d: %s", graphQLPath, ErrUnexpectedStatus, resp.status, snippet(resp.body))
	}

	var decoded graphQLResponse
	if err := json.Unmarshal(resp.body, &decoded); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", graphQLPath, err)
	}
	if len(decoded.Errors) > 0 {
		return nil, fmt.Errorf("%s: %w: %s", graphQLPath, errGraphQL, joinGraphQLErrors(decoded.Errors))
	}
	if decoded.Data.Repository == nil {
		return nil, fmt.Errorf("%s: %w: no repository in response", graphQLPath, errGraphQL)
	}
	return decoded.Data.Repository, nil
}

// rateLimitedType is how GitHub's GraphQL endpoint names a rate limit. It is
// the one error type there that says "come back later" rather than "this
// answer will not change".
const rateLimitedType = "RATE_LIMITED"

// graphQLRateLimited reports whether a 200 body is nothing but a rate limit.
//
// The GraphQL API has no status for a primary rate limit: it answers 200 with
// an errors array whose entries carry RATE_LIMITED, so the retry policy cannot
// see it from the status the way it sees a 429. Every other error type is
// final on the first response, for the same reason a bare 403 is: NOT_FOUND
// and FORBIDDEN answer the same however many times they are asked, and
// resending them is a hang that looks like a stall. An array mixing a rate
// limit with one of those is final too — waiting could clear the limit but
// never the rest, so the run would spend its whole budget to fail anyway.
func graphQLRateLimited(body []byte) bool {
	var decoded struct {
		Errors []graphQLError `json:"errors"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return false
	}
	if len(decoded.Errors) == 0 {
		return false
	}
	for _, e := range decoded.Errors {
		if e.Type != rateLimitedType {
			return false
		}
	}
	return true
}

// joinGraphQLErrors renders every message GitHub returned, because the first
// one is often the least specific.
func joinGraphQLErrors(errs []graphQLError) string {
	messages := make([]string, 0, len(errs))
	for _, e := range errs {
		if e.Type != "" {
			messages = append(messages, e.Type+": "+e.Message)
			continue
		}
		messages = append(messages, e.Message)
	}
	return strings.Join(messages, "; ")
}
