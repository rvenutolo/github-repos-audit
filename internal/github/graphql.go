package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// graphQLPath is the one endpoint this client posts to;
// TestClient_Collect_isReadOnly is why it is the only one.
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
// The Renovate probes also select the blob's text, because the minimum
// release age is resolved from the file's content: selecting it here costs no
// extra request, where a contents API read per repository would.
//
// isArchived and isFork are not requested. Discovery already filters on the
// REST list's fork and archived flags and audit.Repo records neither, so asking
// for them here would collect a field the report can never show.
//
// The head commit's first hundred history entries ride along too, so a
// repository with a short history costs no request beyond this one; the rest
// are paged by historyQuery.
//
// So do the check suites of the latest merged pull requests' head commits and
// of the most recent default-branch commits, which is what the CI timing is
// read from; ciFields explains the page sizes. Merged pull requests can only
// be ordered by when they were last updated, not by when they merged, which
// is why each one's merge time is selected beside it.
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
    merged_pull_requests: pullRequests(states: MERGED, first: ` + ciCommitsPage + `, orderBy: { field: UPDATED_AT, direction: DESC }) {
      nodes {
        number
        merged_at: mergedAt
        author { type: __typename }
        commits(last: 1) { nodes { commit { ...CICommit } } }
      }
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
          history(first: 100) {
            ` + historyFields + `
          }
          recent_commits: history(first: ` + ciCommitsPage + `) {
            nodes { ...CICommit }
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
    security_root: object(expression: "HEAD:SECURITY.md") { oid }
    security_github: object(expression: "HEAD:.github/SECURITY.md") { oid }
    security_docs: object(expression: "HEAD:docs/SECURITY.md") { oid }
    contributing_root: object(expression: "HEAD:CONTRIBUTING.md") { oid }
    contributing_github: object(expression: "HEAD:.github/CONTRIBUTING.md") { oid }
    contributing_docs: object(expression: "HEAD:docs/CONTRIBUTING.md") { oid }
    coc_root: object(expression: "HEAD:CODE_OF_CONDUCT.md") { oid }
    coc_github: object(expression: "HEAD:.github/CODE_OF_CONDUCT.md") { oid }
    coc_docs: object(expression: "HEAD:docs/CODE_OF_CONDUCT.md") { oid }
    renovate_json: object(expression: "HEAD:renovate.json") {
      oid
      ... on Blob { text is_truncated: isTruncated is_binary: isBinary }
    }
    renovate_jsonc: object(expression: "HEAD:renovate.jsonc") {
      oid
      ... on Blob { text is_truncated: isTruncated is_binary: isBinary }
    }
    renovate_json5: object(expression: "HEAD:renovate.json5") {
      oid
      ... on Blob { text is_truncated: isTruncated is_binary: isBinary }
    }
    renovate_github_json: object(expression: "HEAD:.github/renovate.json") {
      oid
      ... on Blob { text is_truncated: isTruncated is_binary: isBinary }
    }
    renovate_github_jsonc: object(expression: "HEAD:.github/renovate.jsonc") {
      oid
      ... on Blob { text is_truncated: isTruncated is_binary: isBinary }
    }
    renovate_github_json5: object(expression: "HEAD:.github/renovate.json5") {
      oid
      ... on Blob { text is_truncated: isTruncated is_binary: isBinary }
    }
    renovate_gitlab_json: object(expression: "HEAD:.gitlab/renovate.json") {
      oid
      ... on Blob { text is_truncated: isTruncated is_binary: isBinary }
    }
    renovate_gitlab_jsonc: object(expression: "HEAD:.gitlab/renovate.jsonc") {
      oid
      ... on Blob { text is_truncated: isTruncated is_binary: isBinary }
    }
    renovate_gitlab_json5: object(expression: "HEAD:.gitlab/renovate.json5") {
      oid
      ... on Blob { text is_truncated: isTruncated is_binary: isBinary }
    }
    renovaterc: object(expression: "HEAD:.renovaterc") {
      oid
      ... on Blob { text is_truncated: isTruncated is_binary: isBinary }
    }
    renovaterc_json: object(expression: "HEAD:.renovaterc.json") {
      oid
      ... on Blob { text is_truncated: isTruncated is_binary: isBinary }
    }
    renovaterc_jsonc: object(expression: "HEAD:.renovaterc.jsonc") {
      oid
      ... on Blob { text is_truncated: isTruncated is_binary: isBinary }
    }
    renovaterc_json5: object(expression: "HEAD:.renovaterc.json5") {
      oid
      ... on Blob { text is_truncated: isTruncated is_binary: isBinary }
    }
    workflows: object(expression: "HEAD:.github/workflows") {
      ... on Tree {
        entries {
          name
        }
      }
    }
  }
}
` + ciFields

// The CI page sizes. ciCommitsPage is how many merged pull requests and how
// many default-branch commits are read: three times the five the report
// averages over, so that leaving out bot-authored pull requests, commits no
// check ran on and commits still running normally leaves five to average.
// ciSuitesPage and ciRunsPage bound one commit's suites and each suite's runs;
// a commit that overflows either is recorded as incomplete rather than
// measured short. A commit carries one suite per workflow its push triggered
// plus one per app that subscribes to checks, which can pass ten, and a page
// too small for that would quietly make every commit incomplete.
//
// GitHub refuses a query whose worst case exceeds 500,000 nodes, and the
// worst case multiplies along each path: two lists of 15 commits × 20 suites
// × 30 runs is 18,000 nodes, far inside the limit and far above anything the
// CI of a personal repository produces.
const (
	ciCommitsPage = "15"
	ciSuitesPage  = "20"
	ciRunsPage    = "30"
)

// ciFields is the fragment both CI lists select per commit: when each check
// suite was created, which app and workflow it belongs to, and each check
// run's status and times. A re-run is timed like any other run, so a flaky
// job that needed one reads as the wait it caused.
const ciFields = `fragment CICommit on Commit {
  oid
  check_suites: checkSuites(first: ` + ciSuitesPage + `) {
    page_info: pageInfo { has_next_page: hasNextPage }
    nodes {
      created_at: createdAt
      app { name }
      workflow_run: workflowRun { workflow { name } }
      check_runs: checkRuns(first: ` + ciRunsPage + `) {
        page_info: pageInfo { has_next_page: hasNextPage }
        nodes { name status started_at: startedAt completed_at: completedAt }
      }
    }
  }
}`

// historyFields is one page of a commit's history: who wrote and who
// committed each commit. It is shared by the first page, which rides along
// with repoQuery, and every later page, which is its own query.
const historyFields = `page_info: pageInfo { has_next_page: hasNextPage end_cursor: endCursor }
            nodes {
              author { name email }
              committer { name email }
            }`

// historyQuery fetches one later page of a commit's history, anchored to an
// oid rather than the branch name; identities explains why.
const historyQuery = `query RepoHistory($owner: String!, $name: String!, $oid: GitObjectID!, $cursor: String!) {
  repository(owner: $owner, name: $name) {
    object(oid: $oid) {
      ... on Commit {
        history(first: 100, after: $cursor) {
            ` + historyFields + `
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

type graphQLData struct {
	Repository *repository `json:"repository"`
}

// graphQLError is one entry of an errors array. Path is the response path of
// the field the error belongs to, absent for an error about the whole
// document. Its elements are response keys, so they are this package's
// snake_case aliases, and list indices, which decode as float64.
type graphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

// gitObject is a file probe's answer. Only its presence matters; oid is
// selected because object returns an interface and a selection set is
// required.
type gitObject struct {
	OID string `json:"oid"`
}

// renovateBlob is a Renovate config probe's answer: its presence, as for every
// other probe, plus the text the minimum-release-age resolution starts from.
// GitHub answers text: null for a binary blob and truncates a large one, and
// both are recorded as unresolved rather than read as an empty config.
type renovateBlob struct {
	OID         string  `json:"oid"`
	Text        *string `json:"text"`
	IsTruncated bool    `json:"is_truncated"`
	IsBinary    bool    `json:"is_binary"`
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
	MergedPullRequests  mergedList   `json:"merged_pull_requests"`
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

	SecurityRoot       *gitObject `json:"security_root"`
	SecurityGitHub     *gitObject `json:"security_github"`
	SecurityDocs       *gitObject `json:"security_docs"`
	ContributingRoot   *gitObject `json:"contributing_root"`
	ContributingGitHub *gitObject `json:"contributing_github"`
	ContributingDocs   *gitObject `json:"contributing_docs"`
	CoCRoot            *gitObject `json:"coc_root"`
	CoCGitHub          *gitObject `json:"coc_github"`
	CoCDocs            *gitObject `json:"coc_docs"`

	RenovateJSON        *renovateBlob `json:"renovate_json"`
	RenovateJSONC       *renovateBlob `json:"renovate_jsonc"`
	RenovateJSON5       *renovateBlob `json:"renovate_json5"`
	RenovateGitHubJSON  *renovateBlob `json:"renovate_github_json"`
	RenovateGitHubJSONC *renovateBlob `json:"renovate_github_jsonc"`
	RenovateGitHubJSON5 *renovateBlob `json:"renovate_github_json5"`
	RenovateGitLabJSON  *renovateBlob `json:"renovate_gitlab_json"`
	RenovateGitLabJSONC *renovateBlob `json:"renovate_gitlab_jsonc"`
	RenovateGitLabJSON5 *renovateBlob `json:"renovate_gitlab_json5"`
	RenovateRC          *renovateBlob `json:"renovaterc"`
	RenovateRCJSON      *renovateBlob `json:"renovaterc_json"`
	RenovateRCJSONC     *renovateBlob `json:"renovaterc_jsonc"`
	RenovateRCJSON5     *renovateBlob `json:"renovaterc_json5"`

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
	History           *history     `json:"history"`
	RecentCommits     *ciCommits   `json:"recent_commits"`
}

// history is one page of a commit's history, as historyFields selects it.
type history struct {
	PageInfo pageInfo      `json:"page_info"`
	Nodes    []historyNode `json:"nodes"`
}

type pageInfo struct {
	HasNextPage bool    `json:"has_next_page"`
	EndCursor   *string `json:"end_cursor"`
}

type historyNode struct {
	// Author and Committer are null when GitHub cannot resolve the actor.
	Author    *gitActor `json:"author"`
	Committer *gitActor `json:"committer"`
}

// gitActor is a commit's author or committer as the commit spells it. Either
// half is null when the commit does not carry it.
type gitActor struct {
	Name  *string `json:"name"`
	Email *string `json:"email"`
}

// mergedList is the latest merged pull requests, as repoQuery selects them.
type mergedList struct {
	Nodes []mergedPullRequest `json:"nodes"`
}

type mergedPullRequest struct {
	Number   int        `json:"number"`
	MergedAt *time.Time `json:"merged_at"`
	// Author is null when the account that opened the pull request has been
	// deleted.
	Author  *prAuthor `json:"author"`
	Commits ciHeads   `json:"commits"`
}

// prAuthor is only the author's GraphQL type: Bot for a GitHub App's account.
type prAuthor struct {
	Type string `json:"type"`
}

// ciHeads is commits(last: 1): the pull request's head commit.
type ciHeads struct {
	Nodes []struct {
		Commit ciCommit `json:"commit"`
	} `json:"nodes"`
}

// ciCommits is the recent default-branch commits.
type ciCommits struct {
	Nodes []ciCommit `json:"nodes"`
}

// ciCommit is one commit as ciFields selects it.
type ciCommit struct {
	OID         string      `json:"oid"`
	CheckSuites checkSuites `json:"check_suites"`
}

type checkSuites struct {
	PageInfo pageInfo     `json:"page_info"`
	Nodes    []checkSuite `json:"nodes"`
}

type checkSuite struct {
	CreatedAt time.Time `json:"created_at"`
	// App is null when GitHub does not name the app that owns the suite.
	App *namedNode `json:"app"`
	// WorkflowRun is null for every suite that is not a GitHub Actions run.
	WorkflowRun *workflowRun `json:"workflow_run"`
	CheckRuns   checkRuns    `json:"check_runs"`
}

type workflowRun struct {
	Workflow *namedNode `json:"workflow"`
}

type namedNode struct {
	Name string `json:"name"`
}

type checkRuns struct {
	PageInfo pageInfo   `json:"page_info"`
	Nodes    []checkRun `json:"nodes"`
}

// checkRun is one run. StartedAt and CompletedAt are null until the run gets
// that far.
type checkRun struct {
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
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
	var data graphQLData
	vars := map[string]string{"owner": c.owner, "name": name}
	if err := c.postGraphQL(ctx, repoQuery, vars, &data); err != nil {
		return nil, err
	}
	if data.Repository == nil {
		return nil, fmt.Errorf("%s: %w: no repository in response", graphQLPath, errGraphQL)
	}
	return data.Repository, nil
}

// postGraphQL sends one query and decodes its data into into. Everything a
// GraphQL answer can fail by — a non-200, an errors array, an undecodable
// body — is an error here, so neither caller can mistake a failure for data.
// A GraphQL failure arrives as a 200 with a non-empty errors array, which is
// why the status alone never says whether the answer is usable.
func (c *Client) postGraphQL(ctx context.Context, query string, vars map[string]string, into any) error {
	body, err := json.Marshal(graphQLRequest{Query: query, Variables: vars})
	if err != nil { // coverage-exempt: json.Marshal cannot fail for a string and a string map
		return fmt.Errorf("encode graphql request: %w", err)
	}

	resp, err := c.doRetrying(ctx, http.MethodPost, c.baseURL+graphQLPath, body, graphQLTransient)
	if err != nil {
		return fmt.Errorf("%s: %w", graphQLPath, err)
	}
	if resp.status != http.StatusOK {
		return fmt.Errorf("%s: %w %d: %s", graphQLPath, ErrUnexpectedStatus, resp.status, snippet(resp.body))
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []graphQLError  `json:"errors"`
	}
	if err := json.Unmarshal(resp.body, &envelope); err != nil {
		return fmt.Errorf("%s: decode response: %w", graphQLPath, err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("%s: %w: %s", graphQLPath, errGraphQL, joinGraphQLErrors(envelope.Errors))
	}
	if err := json.Unmarshal(envelope.Data, into); err != nil {
		return fmt.Errorf("%s: decode data: %w", graphQLPath, err)
	}
	return nil
}

// rateLimitedType is how GitHub's GraphQL endpoint names a rate limit. It is
// the one error type there that says "come back later" rather than "this
// answer will not change".
const rateLimitedType = "RATE_LIMITED"

// timeoutMessage is the part of GitHub's answer to a query that ran out of
// execution time that names the timeout. The error carries no type, and the
// rest of its message ("Something went wrong while executing your query.",
// then a request id) is the same for a genuine GitHub bug, so this phrase is
// the only thing that tells a slow query apart.
const timeoutMessage = "This may be the result of a timeout"

// errGraphQLTimeout is a query GitHub gave up executing. It is a GraphQL
// error, so a run that fails on it reads as one, not as a spent quota.
var errGraphQLTimeout = fmt.Errorf("%w: the query timed out", errGraphQL)

// graphQLTransient returns nil when a 200 body is an answer or a final
// failure, and the kind of transient failure it is otherwise: ErrRateLimited
// when any entry is a rate limit, errGraphQLTimeout when every entry is an
// execution timeout.
//
// The GraphQL API has no status for either. A primary rate limit is a 200
// with RATE_LIMITED entries, so the retry policy cannot see it from the
// status the way it sees a 429. A query that ran too long is a 200 with a
// type-less timeout entry, where the same slowness one layer out is a 502
// that is already retried; failing the whole nightly run over one slow
// repository would treat the two differently for no reason. Every other
// error is final on the first response, for the same reason a bare 403 is
// (see retryWait): NOT_FOUND and FORBIDDEN answer the same however many
// times they are asked. An array mixing a transient entry with one of those
// is final too — waiting could clear the limit or let the query finish but
// never fix the rest, so the run would spend its whole budget to fail anyway.
func graphQLTransient(body []byte) error {
	var decoded struct {
		Errors []graphQLError `json:"errors"`
	}
	// A body that does not decode is not transient: it goes back to
	// postGraphQL as it stands, which reports it as the failure it is.
	if json.Unmarshal(body, &decoded) != nil || len(decoded.Errors) == 0 {
		return nil //nolint:nilerr // nil means "not transient", not "no error"; postGraphQL reports the bad body
	}
	limited := false
	for _, e := range decoded.Errors {
		switch {
		case e.Type == rateLimitedType:
			limited = true
		case e.Type == "" && strings.Contains(e.Message, timeoutMessage):
		default:
			return nil
		}
	}
	if limited {
		return ErrRateLimited
	}
	return errGraphQLTimeout
}

// joinGraphQLErrors renders every message GitHub returned, because the first
// one is often the least specific. Each carries its path when it has one: a
// fine-grained token missing one permission is refused field by field with
// the same FORBIDDEN message, so the path is the only thing that says which
// permission is missing.
func joinGraphQLErrors(errs []graphQLError) string {
	messages := make([]string, 0, len(errs))
	for _, e := range errs {
		var prefix string
		switch path := joinPath(e.Path); {
		case e.Type != "" && path != "":
			prefix = e.Type + " at " + path
		case e.Type != "":
			prefix = e.Type
		default:
			prefix = path
		}
		if prefix == "" {
			messages = append(messages, e.Message)
			continue
		}
		messages = append(messages, prefix+": "+e.Message)
	}
	return strings.Join(messages, "; ")
}

// joinPath renders a response path as dotted keys, e.g.
// repository.rulesets.nodes.0.rules. fmt prints a whole float64 without a
// fraction, so a list index reads as an integer.
func joinPath(path []any) string {
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = fmt.Sprint(p)
	}
	return strings.Join(parts, ".")
}
