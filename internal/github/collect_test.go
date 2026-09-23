package github_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.uber.org/goleak"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/github"
)

// at parses a fixture timestamp. Every instant reaching audit.Repo is UTC.
func at(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return parsed.UTC()
}

// wantShellScripts is the public, fully-equipped repository: a license, topics, a
// green rollup, both rulesets, a required-check list and an Actions allowlist.
func wantShellScripts(t *testing.T) audit.Repo {
	t.Helper()
	return audit.Repo{
		Name:             "shell-scripts",
		Visibility:       "public",
		Description:      "Placeholder description.",
		Homepage:         "https://example.com/shell-scripts",
		Topics:           5,
		License:          "GPL-3.0",
		OpenPullRequests: 1,
		Branches:         2,
		PushedAt:         at(t, "2025-09-05T01:39:25Z"),
		DefaultBranch:    "main",
		HeadOID:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa000c",
		Files: audit.Files{
			README:         true,
			Gitignore:      true,
			Editorconfig:   true,
			FlakeNix:       true,
			Justfile:       true,
			Security:       true,
			RenovateConfig: ".github/renovate.json",
			Workflows: []string{
				"ci.yml", "workflow-06.yml", "workflow-10.yml", "workflow-12.yml", "workflow-13.yml",
				"workflow-15.yml", "workflow-16.yml", "workflow-29.yml", "workflow-30.yml",
				"workflow-31.yml", "workflow-32.yml",
			},
		},
		CIState: "SUCCESS",
		Rulesets: []audit.Ruleset{
			{
				Name:        "ruleset-01",
				Target:      "BRANCH",
				Enforcement: "ACTIVE",
				Include:     []string{"~DEFAULT_BRANCH"},
				Rules: []string{
					"DELETION", "NON_FAST_FORWARD", "PULL_REQUEST",
					"REQUIRED_SIGNATURES", "REQUIRED_STATUS_CHECKS",
				},
			},
			{
				Name:        "ruleset-03",
				Target:      "TAG",
				Enforcement: "ACTIVE",
				Include:     []string{"refs/tags/**"},
				Rules:       []string{"DELETION", "NON_FAST_FORWARD", "UPDATE"},
			},
		},
		Branch: audit.BranchRules{
			Known: true,
			Types: []string{
				"deletion", "non_fast_forward", "pull_request",
				"required_signatures", "required_status_checks",
			},
			RequiredChecks: []string{
				"check-06", "check-11", "check-14", "check-17", "check-30",
				"check-31", "check-32", "check-33", "check-34", "check-35",
			},
		},
		Settings: audit.Settings{
			HasIssues:                    true,
			VulnerabilityAlerts:          true,
			MergeCommitAllowed:           true,
			MergeCommitTitle:             "PR_TITLE",
			MergeCommitMessage:           "PR_BODY",
			AutoMergeAllowed:             true,
			DeleteBranchOnMerge:          true,
			SecretScanning:               "enabled",
			SecretScanningPushProtection: "enabled",
			// The endpoint answered, so this is a real "on" rather than an n/a.
			PrivateVulnerabilityReporting: new(true),
			ActionsPolicy:                 "selected",
			SHAPinningRequired:            true,
			AllowedActions: &audit.AllowedActions{
				GitHubOwnedAllowed: true,
				Patterns: []string{
					"example-org/action-08@*", "example-org/action-12@*", "example-org/action-13@*",
					"example-org/action-14@*", "example-org/action-17@*", "example-org/action-18@*",
					"example-org/action-19@*", "example-org/action-20@*", "example-org/action-21@*",
					"example-org/action-22@*", "example-org/action-23@*",
				},
			},
			DefaultWorkflowPermissions: "read",
			// Left empty by the 422 from the access endpoint: the setting does
			// not exist on a public repository.
			ActionsAccessLevel: "",
		},
		// The value is set in the repository's own JSONC file, so the source is
		// that file's path rather than a preset.
		Renovate: audit.Renovate{MinReleaseAge: "3 days", MinReleaseAgeSource: ".github/renovate.json"},
	}
}

// wantWebApp is the private repository: no security_and_analysis block, a
// 404 from private-vulnerability-reporting, a null rollup, and an access level
// that only a private repository has.
func wantWebApp(t *testing.T) audit.Repo {
	t.Helper()
	return audit.Repo{
		Name:          "web-app",
		Visibility:    "private",
		Description:   "Placeholder description.",
		Branches:      1,
		PushedAt:      at(t, "2025-09-02T01:44:53Z"),
		DefaultBranch: "main",
		HeadOID:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0014",
		Files:         audit.Files{RenovateConfig: "renovate.json", Workflows: []string{"workflow-33.yml"}},
		// Inherited from the in-account preset; config:best-practices is a
		// built-in, which sets nothing this tool can read.
		Renovate: audit.Renovate{MinReleaseAge: "7 days", MinReleaseAgeSource: "github>gh-owner/preset-store"},
		Rulesets: []audit.Ruleset{
			{
				Name:        "ruleset-01",
				Target:      "BRANCH",
				Enforcement: "ACTIVE",
				Include:     []string{"~DEFAULT_BRANCH"},
				Rules: []string{
					"DELETION", "NON_FAST_FORWARD", "PULL_REQUEST",
					"REQUIRED_SIGNATURES", "REQUIRED_STATUS_CHECKS",
				},
			},
			{
				Name:        "ruleset-03",
				Target:      "TAG",
				Enforcement: "ACTIVE",
				Include:     []string{"refs/tags/**"},
				Rules:       []string{"DELETION", "NON_FAST_FORWARD", "UPDATE"},
			},
		},
		Branch: audit.BranchRules{
			Known: true,
			Types: []string{
				"deletion", "non_fast_forward", "pull_request",
				"required_signatures", "required_status_checks",
			},
			RequiredChecks: []string{"check-36"},
		},
		Settings: audit.Settings{
			HasIssues:           true,
			VulnerabilityAlerts: true,
			MergeCommitAllowed:  true,
			MergeCommitTitle:    "PR_TITLE",
			MergeCommitMessage:  "PR_BODY",
			AutoMergeAllowed:    true,
			DeleteBranchOnMerge: true,
			ActionsPolicy:       "selected",
			SHAPinningRequired:  true,
			// The allowlist exists but is empty: GitHub-owned actions only.
			AllowedActions:             &audit.AllowedActions{GitHubOwnedAllowed: true},
			DefaultWorkflowPermissions: "read",
			ActionsAccessLevel:         "none",
		},
	}
}

// wantEmpty is the repository with no commits at all. Everything derived from a
// branch is unknown; everything else still applies.
func wantEmpty() audit.Repo {
	return audit.Repo{
		Name:        "blank-repo",
		Visibility:  "private",
		Description: "Placeholder description.",
		Empty:       true,
		Settings: audit.Settings{
			HasIssues:                  true,
			VulnerabilityAlerts:        true,
			MergeCommitAllowed:         true,
			MergeCommitTitle:           "PR_TITLE",
			MergeCommitMessage:         "PR_BODY",
			AutoMergeAllowed:           true,
			DeleteBranchOnMerge:        true,
			ActionsPolicy:              "all",
			DefaultWorkflowPermissions: "read",
			ActionsAccessLevel:         "none",
		},
	}
}

// TestClient_Collect drives the whole collector against the recorded responses
// and compares the assembled facts field by field.
func TestClient_Collect(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	client := newTestClient(t, api.url())

	tests := []struct {
		name string
		want audit.Repo
	}{
		{name: "shell-scripts", want: wantShellScripts(t)},
		{name: "web-app", want: wantWebApp(t)},
		{name: "blank-repo", want: wantEmpty()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := client.Collect(t.Context(), []string{tc.name})
			if err != nil {
				t.Fatalf("Collect(%q): %v", tc.name, err)
			}
			if len(got) != 1 {
				t.Fatalf("Collect(%q) returned %d repositories, want 1", tc.name, len(got))
			}
			if diff := cmp.Diff(tc.want, got[0]); diff != "" {
				t.Errorf("Collect(%q) mismatch (-want +got):\n%s", tc.name, diff)
			}
		})
	}
}

// TestClient_Collect_releasesAndAlternates covers the fields no other fixture
// exercises: a real release history, the undotted justfile spelling, a Renovate
// config at the root whose minimum release age comes from a preset, and two of
// the three community health files.
func TestClient_Collect_releasesAndAlternates(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"mixedCase-flake"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	repo := got[0]

	wantReleases := audit.Releases{Total: 23, LastPublishedAt: at(t, "2025-09-01T10:14:28Z")}
	if diff := cmp.Diff(wantReleases, repo.Releases); diff != "" {
		t.Errorf("Releases mismatch (-want +got):\n%s", diff)
	}

	wantFiles := audit.Files{
		README:         true,
		Gitignore:      true,
		Editorconfig:   true,
		FlakeNix:       true,
		Justfile:       true,
		Changelog:      true,
		Security:       true,
		Contributing:   true,
		RenovateConfig: "renovate.json",
		Workflows:      repo.Files.Workflows,
	}
	if diff := cmp.Diff(wantFiles, repo.Files); diff != "" {
		t.Errorf("Files mismatch (-want +got):\n%s", diff)
	}
	wantRenovate := audit.Renovate{MinReleaseAge: "7 days", MinReleaseAgeSource: "github>gh-owner/preset-store"}
	if diff := cmp.Diff(wantRenovate, repo.Renovate); diff != "" {
		t.Errorf("Renovate mismatch (-want +got):\n%s", diff)
	}
	if repo.License != "MIT" {
		t.Errorf("License = %q, want MIT", repo.License)
	}
	// mixedCase-flake's tag ruleset is named differently from every other
	// repo's, which is exactly what the ruleset name is collected for.
	if repo.Rulesets[1].Name != "ruleset-02" {
		t.Errorf("tag ruleset = %q, want ruleset-02", repo.Rulesets[1].Name)
	}
}

// TestClient_Collect_normalNotErrors walks every row of the spec's
// normal-not-errors table. Each one is an ordinary answer that must reach the
// model as a fact rather than aborting the run.
func TestClient_Collect_normalNotErrors(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	client := newTestClient(t, api.url())

	tests := []struct {
		name  string
		repo  string
		check func(t *testing.T, repo audit.Repo)
	}{
		{
			name: "security_and_analysis absent on a private repo means n/a",
			repo: "web-app",
			check: func(t *testing.T, repo audit.Repo) {
				t.Helper()
				if repo.Settings.SecretScanning != "" || repo.Settings.SecretScanningPushProtection != "" {
					t.Errorf("secret scanning = %q/%q, want both empty",
						repo.Settings.SecretScanning, repo.Settings.SecretScanningPushProtection)
				}
			},
		},
		{
			name: "404 from private-vulnerability-reporting means n/a",
			repo: "web-app",
			check: func(t *testing.T, repo audit.Repo) {
				t.Helper()
				if repo.Settings.PrivateVulnerabilityReporting != nil {
					t.Errorf("private vulnerability reporting = %v, want nil",
						*repo.Settings.PrivateVulnerabilityReporting)
				}
			},
		},
		{
			name: "422 from the access endpoint means n/a",
			repo: "shell-scripts",
			check: func(t *testing.T, repo audit.Repo) {
				t.Helper()
				if repo.Settings.ActionsAccessLevel != "" {
					t.Errorf("actions access level = %q, want empty", repo.Settings.ActionsAccessLevel)
				}
			},
		},
		{
			name: "404 from selected-actions means no allowlist",
			repo: "blank-repo",
			check: func(t *testing.T, repo audit.Repo) {
				t.Helper()
				if repo.Settings.AllowedActions != nil {
					t.Errorf("allowed actions = %+v, want nil", repo.Settings.AllowedActions)
				}
				if repo.Settings.ActionsPolicy != "all" {
					t.Errorf("actions policy = %q, want all", repo.Settings.ActionsPolicy)
				}
			},
		},
		{
			name: "a null statusCheckRollup means no check has run",
			repo: "web-app",
			check: func(t *testing.T, repo audit.Repo) {
				t.Helper()
				if repo.CIState != "" {
					t.Errorf("ci state = %q, want empty", repo.CIState)
				}
			},
		},
		{
			name: "an empty ruleset list is not a failure",
			repo: "blank-repo",
			check: func(t *testing.T, repo audit.Repo) {
				t.Helper()
				if len(repo.Rulesets) != 0 {
					t.Errorf("rulesets = %+v, want none", repo.Rulesets)
				}
			},
		},
		{
			name: "a null defaultBranchRef is an empty repository",
			repo: "blank-repo",
			check: func(t *testing.T, repo audit.Repo) {
				t.Helper()
				if !repo.Empty {
					t.Error("Empty = false, want true")
				}
				if repo.Branch.Known {
					t.Error("Branch.Known = true, want false: the branch endpoint must be skipped")
				}
				if repo.DefaultBranch != "" || repo.HeadOID != "" {
					t.Errorf("branch = %q, head = %q, want both empty", repo.DefaultBranch, repo.HeadOID)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := client.Collect(t.Context(), []string{tc.repo})
			if err != nil {
				t.Fatalf("Collect(%q): %v", tc.repo, err)
			}
			tc.check(t, got[0])
		})
	}
}

// TestClient_Collect_skipsBranchRulesForAnEmptyRepository asserts the endpoint
// is not merely tolerated but never called: {branch} does not exist, so a
// request for it would be a guess at a name.
func TestClient_Collect_skipsBranchRulesForAnEmptyRepository(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	if _, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"blank-repo"}); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for _, c := range api.seen() {
		if strings.Contains(c.path, "/rules/branches/") {
			t.Errorf("called %s for an empty repository", c.path)
		}
	}
}

// TestClient_Collect_usesTheLiveDefaultBranch guards against a hardcoded main:
// the branch in the REST path must be the one GraphQL answered with.
func TestClient_Collect_usesTheLiveDefaultBranch(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	if _, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"shell-scripts"}); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	var found bool
	for _, c := range api.seen() {
		if c.path == "/repos/gh-owner/shell-scripts/rules/branches/main" {
			found = true
		}
	}
	if !found {
		t.Errorf("no branch-rules request for the live default branch; saw %v", api.seen())
	}
}

// TestClient_Collect_keepsTheFullOID pins the forty characters. Abbreviating
// would silently break the smoke suite's cross-check, which answers
// total_count: 0 for a short SHA with a 200 rather than an error.
func TestClient_Collect_keepsTheFullOID(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"shell-scripts"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got[0].HeadOID) != 40 {
		t.Errorf("HeadOID = %q (%d characters), want 40", got[0].HeadOID, len(got[0].HeadOID))
	}
}

// TestClient_Collect_sortsCaseInsensitively pins the ordering audit.json is
// diffed on: an unstable order would make every run look like a change.
func TestClient_Collect_sortsCaseInsensitively(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	got, err := newTestClient(t, api.url()).Collect(t.Context(),
		[]string{"shell-scripts", "blank-repo", "mixedCase-flake", "pkg-index", "web-app"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	names := make([]string, 0, len(got))
	for _, repo := range got {
		names = append(names, repo.Name)
	}
	want := []string{"blank-repo", "mixedCase-flake", "pkg-index", "shell-scripts", "web-app"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("order mismatch (-want +got):\n%s", diff)
	}
}

// TestClient_Collect_nothingToDo covers the degenerate call.
func TestClient_Collect_nothingToDo(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	got, err := newTestClient(t, api.url()).Collect(t.Context(), nil)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Collect(nil) = %v, want nothing", got)
	}
	if calls := api.seen(); len(calls) != 0 {
		t.Errorf("Collect(nil) made %d requests, want none", len(calls))
	}
}

// TestClient_Collect_isReadOnly is the assertion the whole design turns on. It
// cannot be a blanket ban on POST, because GitHub's GraphQL endpoint accepts no
// other verb — so exactly one POST target is allowed, every other call must be
// a GET, and every GraphQL document must be a query rather than a mutation.
// Checking the operation is the stricter reading: a verb ban alone would pass a
// mutation smuggled through the allowed endpoint.
func TestClient_Collect_isReadOnly(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	client := newTestClient(t, api.url())

	if _, err := client.Discover(t.Context()); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if _, err := client.Collect(t.Context(),
		[]string{"shell-scripts", "web-app", "pkg-index", "blank-repo", "mixedCase-flake"}); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	calls := api.seen()
	if len(calls) == 0 {
		t.Fatal("no requests recorded; the assertion below would be vacuous")
	}
	for _, c := range calls {
		switch c.method {
		case http.MethodGet:
			// Every read is a GET, which is the whole point.
		case http.MethodPost:
			if c.path != "/graphql" {
				t.Errorf("POST %s: the only permitted POST target is /graphql", c.path)
			}
		default:
			t.Errorf("%s %s: only GET, and POST to /graphql, may be sent", c.method, c.path)
		}
	}

	documents := api.graphQLDocuments()
	if len(documents) == 0 {
		t.Fatal("no GraphQL documents recorded; the assertion below would be vacuous")
	}
	for _, doc := range documents {
		fields := strings.Fields(doc)
		if len(fields) == 0 {
			t.Error("empty GraphQL document")
			continue
		}
		if operation := fields[0]; !strings.HasPrefix(operation, "query") {
			t.Errorf("GraphQL operation is %q, want a query", operation)
		}
		if strings.Contains(doc, "mutation") {
			t.Errorf("GraphQL document mentions a mutation:\n%s", doc)
		}
	}
}

// TestClient_Collect_isLeakFree exercises the fan-out with more than one worker
// and asserts it leaves no goroutine behind.
//
// It does not call t.Parallel: goleak inspects every goroutine in the process,
// so a test sharing the run with another one would see that other test's
// goroutines as leaks. Go runs the sequential tests before it resumes the
// parallel ones, which is what makes this deterministic.
//
//nolint:paralleltest // goleak inspects the whole process; see above
func TestClient_Collect_isLeakFree(t *testing.T) {
	api := newFakeAPI(t)
	httpClient := &http.Client{Timeout: 10 * time.Second}
	client, err := github.New(github.Options{
		Owner:      testOwner,
		Token:      theToken,
		BaseURL:    api.url(),
		HTTPClient: httpClient,
		Logger:     discardLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// More names than the fan-out's limit, so several workers run at once.
	names := []string{
		"shell-scripts", "web-app", "pkg-index", "blank-repo", "mixedCase-flake",
		"shell-scripts", "web-app", "pkg-index", "blank-repo", "mixedCase-flake",
	}
	got, err := client.Collect(t.Context(), names)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != len(names) {
		t.Fatalf("Collect returned %d repositories, want %d", len(got), len(names))
	}

	// Both sides of every connection have to be released before the check: the
	// transport's idle connections are ours, the server's are the fixture's.
	httpClient.CloseIdleConnections()
	api.close()
	goleak.VerifyNone(t)
}

// TestClient_Collect_abortsOnFailure asserts the whole run fails when one
// repository does. Nothing partial may reach the renderer: a field GitHub
// declined to answer is not a field that is missing, and rendering it as a gap
// would invent one.
func TestClient_Collect_abortsOnFailure(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/automated-security-fixes") {
			http.Error(w, `{"message":"Server Error"}`, http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, api.url()+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	t.Cleanup(failing.Close)

	got, err := newTestClient(t, failing.URL).Collect(t.Context(),
		[]string{"shell-scripts", "web-app", "pkg-index"})
	if !errors.Is(err, github.ErrUnexpectedStatus) {
		t.Fatalf("Collect error = %v, want ErrUnexpectedStatus", err)
	}
	if got != nil {
		t.Errorf("Collect returned %d repositories alongside an error, want none", len(got))
	}
	if !strings.Contains(err.Error(), "automated-security-fixes") {
		t.Errorf("error does not name the endpoint that failed: %v", err)
	}
}

// TestClient_Collect_graphQLErrorsAbort covers GitHub's other failure mode: a
// 200 carrying an errors array, which a status check alone would let through.
func TestClient_Collect_graphQLErrorsAbort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{
			name: "errors array",
			body: `{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a Repository"}]}`,
		},
		{name: "no repository", body: `{"data":{"repository":null}}`},
		{name: "unparsable body", body: `not json at all`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Errorf("write: %v", err)
				}
			}))
			t.Cleanup(server.Close)

			if _, err := newTestClient(t, server.URL).Collect(t.Context(), []string{"ghost"}); err == nil {
				t.Fatal("Collect accepted a GraphQL failure")
			}
		})
	}
}

// TestClient_Collect_graphQLNon200Aborts covers the endpoint answering with a
// status rather than an errors array.
func TestClient_Collect_graphQLNon200Aborts(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	_, err := newTestClient(t, server.URL).Collect(t.Context(), []string{"shell-scripts"})
	if !errors.Is(err, github.ErrUnexpectedStatus) {
		t.Errorf("Collect error = %v, want ErrUnexpectedStatus", err)
	}
}

// TestClient_Collect_selectedActionsConflict covers the status GitHub actually
// answers when the policy is "all". The spec documents a 404; a live capture
// showed a 409, and both mean the same thing — there is no allowlist — so both
// are ordinary answers rather than failures.
func TestClient_Collect_selectedActionsConflict(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	conflicting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/selected-actions") {
			http.Error(w,
				`{"message":"Conflict","errors":"All actions and workflows are allowed on this repository"}`,
				http.StatusConflict)
			return
		}
		http.Redirect(w, r, api.url()+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	t.Cleanup(conflicting.Close)

	got, err := newTestClient(t, conflicting.URL).Collect(t.Context(), []string{"shell-scripts"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got[0].Settings.AllowedActions != nil {
		t.Errorf("allowed actions = %+v, want nil", got[0].Settings.AllowedActions)
	}
}

// TestClient_Collect_directPushRepository covers a branch whose rules carry no
// pull_request entry and therefore no required checks — the shape internal/rules
// reads as "direct push is allowed".
func TestClient_Collect_directPushRepository(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"pkg-index"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	want := audit.BranchRules{
		Known: true,
		Types: []string{"deletion", "non_fast_forward", "required_signatures"},
	}
	if diff := cmp.Diff(want, got[0].Branch); diff != "" {
		t.Errorf("Branch mismatch (-want +got):\n%s", diff)
	}
	if got[0].Files.Workflows != nil {
		t.Errorf("Workflows = %v, want nil for a repository with no .github/workflows", got[0].Files.Workflows)
	}
}

// TestClient_Collect_survivesARateLimit is what the retry policy exists for,
// at the level it actually happens: a six-wide fan-out trips GitHub's
// secondary rate limiter on one endpoint, and the nightly refresh has to come
// back for the answer rather than fail and leave the report stale.
func TestClient_Collect_survivesARateLimit(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	limited := "/repos/" + testOwner + "/shell-scripts/actions/permissions"
	api.failNext(limited,
		scriptedFailure{
			status: http.StatusForbidden,
			header: map[string]string{"Retry-After": "1"},
			body:   `{"message":"You have exceeded a secondary rate limit"}`,
		},
		scriptedFailure{
			status: http.StatusTooManyRequests,
			header: map[string]string{
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Reset":     strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10),
			},
			body: `{"message":"API rate limit exceeded"}`,
		},
	)

	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"shell-scripts"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Collect returned %d repositories, want 1", len(got))
	}
	if diff := cmp.Diff(wantShellScripts(t), got[0]); diff != "" {
		t.Errorf("Collect mismatch (-want +got):\n%s", diff)
	}
	if n := api.callsTo(limited); n != 3 {
		t.Errorf("the rate-limited endpoint saw %d calls, want 3", n)
	}
}

// TestClient_Collect_survivesAGraphQLRateLimit is the same failure reached
// through the one endpoint a status-keyed retry cannot observe: /graphql
// answers its rate limit with a 200, so nothing but the body says to come
// back, and the repository still has to arrive whole.
func TestClient_Collect_survivesAGraphQLRateLimit(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	api.failNext("/graphql", scriptedFailure{
		status: http.StatusOK,
		header: map[string]string{
			"X-RateLimit-Remaining": "0",
			"X-RateLimit-Reset":     strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10),
		},
		body: `{"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`,
	})

	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"shell-scripts"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Collect returned %d repositories, want 1", len(got))
	}
	if diff := cmp.Diff(wantShellScripts(t), got[0]); diff != "" {
		t.Errorf("Collect mismatch (-want +got):\n%s", diff)
	}
	if n := api.callsTo("/graphql"); n != 2 {
		t.Errorf("/graphql saw %d calls, want 2", n)
	}
}

// TestClient_Collect_failsOnANonRateLimitGraphQLError is the other half of the
// body-level discriminator, seen from outside: a NOT_FOUND is not a limit, so
// it aborts the run at once rather than being asked for three more times.
func TestClient_Collect_failsOnANonRateLimitGraphQLError(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	api.failNext("/graphql", scriptedFailure{
		status: http.StatusOK,
		body:   `{"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a Repository"}]}`,
	})

	if _, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"shell-scripts"}); err == nil {
		t.Fatal("Collect() error = nil, want the GraphQL error")
	}
	if n := api.callsTo("/graphql"); n != 1 {
		t.Errorf("/graphql saw %d calls, want 1", n)
	}
}

// TestClient_Collect_abortsOnAnUnreadableEndpoint walks every per-repository
// REST call and makes each one answer 401 in turn. A revoked or under-scoped
// token is the realistic cause, and there is exactly one right response to it:
// abort, so a partial picture never reaches the report as if it were whole.
// The endpoints that tolerate a 404, a 409 or a 422 tolerate only those.
func TestClient_Collect_abortsOnAnUnreadableEndpoint(t *testing.T) {
	t.Parallel()

	base := "/repos/" + testOwner + "/shell-scripts"
	endpoints := []string{
		base,
		base + "/private-vulnerability-reporting",
		base + "/automated-security-fixes",
		base + "/actions/permissions",
		base + "/actions/permissions/selected-actions",
		base + "/actions/permissions/workflow",
		base + "/actions/permissions/access",
		base + "/rules/branches/main",
	}
	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()

			api := newFakeAPI(t)
			// 401 is not transient, so the client comes straight back rather
			// than spending the retry budget on a token that will not improve.
			api.failNext(endpoint, scriptedFailure{
				status: http.StatusUnauthorized,
				body:   `{"message":"Bad credentials"}`,
			})

			_, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"shell-scripts"})
			if err == nil {
				t.Fatalf("Collect() error = nil, want a failure from %s", endpoint)
			}
			if !errors.Is(err, github.ErrUnexpectedStatus) {
				t.Errorf("Collect() error = %v, want it to wrap ErrUnexpectedStatus", err)
			}
			if !strings.Contains(err.Error(), endpoint) {
				t.Errorf("Collect() error = %q, want it to name %s", err, endpoint)
			}
			if n := api.callsTo(endpoint); n != 1 {
				t.Errorf("the failing endpoint saw %d calls, want 1 — a 401 must not be retried", n)
			}
		})
	}
}
