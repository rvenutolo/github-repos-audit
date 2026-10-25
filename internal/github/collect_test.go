package github_test

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
		// Byte order, so the null name's "" sorts first and the bot last; the
		// null author is skipped (see walkHistory).
		Identities: []audit.Identity{
			{Name: "", Email: "robin@example.org"},
			{Name: "GitHub", Email: "noreply@github.com"},
			{Name: "Pat Example", Email: "pat@example.com"},
			{Name: "renovate[bot]", Email: "29139614+renovate[bot]@users.noreply.github.com"},
		},
		Files: audit.Files{
			README:         true,
			Gitignore:      true,
			Editorconfig:   true,
			FlakeNix:       true,
			Justfile:       true,
			JustfilePath:   ".justfile",
			Security:       true,
			SecurityPolicy: ".github/SECURITY.md",
			RenovateConfig: ".github/renovate.json",
			Workflows:      []string{"ci.yml"},
		},
		// The file listing and the file read name the same workflow.
		Workflows: []audit.Workflow{{
			Path:         ".github/workflows/ci.yml",
			Events:       []string{"pull_request", "push"},
			PushBranches: []string{"main"},
			Permissions:  audit.Permissions{Set: true, Scopes: map[string]string{"contents": "read"}},
			Jobs:         []audit.Job{{ID: "check", TimeoutMinutes: "15"}},
		}},
		CIState: "SUCCESS",
		// Every shape the CI fixtures carry, recorded as it is: the first pull
		// request's two workflows ran side by side, so its wait spans both runs
		// from the earlier creation; the bot's pull request is marked, not
		// dropped; and a run still going, a commit no workflow ran on and a
		// truncated jobs list are each recorded as incomplete, with whatever
		// finished jobs they have. A run on a commit outside the sample is
		// never asked for its jobs.
		CI: audit.CI{
			PullRequests: []audit.CICommit{
				{
					OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0c01", PRNumber: 12, MergedAt: at(t, "2025-09-04T18:00:00Z"),
					Complete: true, WaitSeconds: 385,
					Jobs: []audit.CIJob{
						{Label: "docs / docs", Seconds: 60},
						{Label: "ci / check", Seconds: 240},
						{Label: "ci / test", Seconds: 360},
					},
				},
				{
					OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0c02", PRNumber: 11, MergedAt: at(t, "2025-09-03T12:00:00Z"),
					AuthorBot: true, Complete: true, WaitSeconds: 190,
					Jobs: []audit.CIJob{{Label: "ci / check", Seconds: 180}},
				},
				{
					OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0c03", PRNumber: 10, MergedAt: at(t, "2025-09-02T12:00:00Z"),
					Jobs: []audit.CIJob{{Label: "ci / check", Seconds: 120}},
				},
			},
			DefaultBranch: []audit.CICommit{
				{
					OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa000c", Complete: true, WaitSeconds: 255,
					Jobs: []audit.CIJob{{Label: "ci / check", Seconds: 240}},
				},
				{OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0c04"},
				{
					OID:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0c05",
					Jobs: []audit.CIJob{{Label: "ci / check", Seconds: 60}},
				},
			},
			// From the default-branch runs read, not the timing page: its
			// newest run is a scheduled one.
			LastDefaultBranchRun: at(t, "2025-09-06T01:39:30Z"),
		},
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
			Strict: true,
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
			// Left empty by the 422 the access endpoint answers on a public
			// repository (see fetchActionsAccess).
			ActionsAccessLevel: "",
		},
		// The value is set in the repository's own JSONC file, so the source is
		// that file's path rather than a preset.
		Renovate: audit.Renovate{MinReleaseAge: "3 days", MinReleaseAgeSource: ".github/renovate.json"},
		// Sorted by key then ref, whatever order GitHub listed them in; the
		// expired artifact is listed but neither counted nor summed.
		Storage: audit.ActionsStorage{
			Caches: []audit.Cache{
				{Key: "example-action-01", Ref: "refs/heads/main", SizeBytes: 263, CreatedAt: at(t, "2025-09-04T02:20:00Z")},
				{Key: "go-Linux-key-01", Ref: "refs/heads/main", SizeBytes: 206000000, CreatedAt: at(t, "2025-09-04T01:00:00Z")},
				{Key: "go-Linux-key-02", Ref: "refs/pull/3/merge", SizeBytes: 2000, CreatedAt: at(t, "2025-09-04T08:59:00Z")},
				{Key: "nix-Linux-key-01", Ref: "refs/heads/main", SizeBytes: 812250354, CreatedAt: at(t, "2025-09-04T02:20:33Z")},
			},
			CacheUsageBytes: 1018252617,
			CacheLimitBytes: 10_000_000_000,
			ArtifactCount:   2,
			ArtifactBytes:   25000,
		},
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
		// The union of all three history pages; the two spellings of one
		// address stay two identities (see walkHistory), and byte order puts
		// "Pat@" before "pat@".
		Identities: []audit.Identity{
			{Name: "Pat Example", Email: "Pat@Example.com"},
			{Name: "Pat Example", Email: "pat@example.com"},
			{Name: "Patrick Example", Email: "patrick@example.org"},
		},
		Files: audit.Files{RenovateConfig: "renovate.json", Workflows: []string{"workflow-33.yml"}},
		// GitHub cut the workflow's text short, so it is unreadable rather
		// than parsed from what arrived.
		Workflows: []audit.Workflow{{
			Path:       ".github/workflows/workflow-33.yml",
			ParseError: ".github/workflows/workflow-33.yml: too large to read in full",
		}},
		CI: audit.CI{
			PullRequests: []audit.CICommit{{
				OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0c11", PRNumber: 3, MergedAt: at(t, "2025-09-01T09:00:00Z"),
				Complete: true, WaitSeconds: 150,
				Jobs: []audit.CIJob{{Label: "ci / check", Seconds: 120}},
			}},
			DefaultBranch: []audit.CICommit{{
				OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0014", Complete: true, WaitSeconds: 140,
				Jobs: []audit.CIJob{{Label: "ci / check", Seconds: 120}},
			}},
			LastDefaultBranchRun: at(t, "2025-09-02T01:45:00Z"),
		},
		// Nothing stored: the limit is still read.
		Storage: audit.ActionsStorage{CacheLimitBytes: 10_000_000_000},
		// The age is inherited from the in-account preset; config:best-practices
		// is a built-in the tool knows turns lock-file maintenance on.
		Renovate: audit.Renovate{
			MinReleaseAge: "7 days", MinReleaseAgeSource: "github>gh-owner/preset-store",
			LockFileMaintenance: true,
		},
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
			Strict:         true,
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

// TestClient_Collect_walksTheWholeHistory follows web-app's history across all
// three pages. Patrick Example appears only on the last one, so a walk that
// stopped early would drop him silently; and every later page must carry the
// head oid the repository query answered, not the branch name (why: see
// identities).
func TestClient_Collect_walksTheWholeHistory(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"web-app"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !slices.Contains(got[0].Identities, audit.Identity{Name: "Patrick Example", Email: "patrick@example.org"}) {
		t.Errorf("Identities = %v, want the identity only the third page carries", got[0].Identities)
	}

	vars := api.graphQLVariables()
	if len(vars) != 4 {
		t.Fatalf("/graphql saw %d documents for web-app, want 4: the repository query, two history pages and the file query", len(vars))
	}
	if _, ok := vars[0]["cursor"]; ok {
		t.Errorf("the repository query carried a cursor: %v", vars[0])
	}
	for i, cursor := range []string{"cursor-02", "cursor-03"} {
		page := vars[i+1]
		want := map[string]string{
			"owner":  testOwner,
			"name":   "web-app",
			"oid":    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0014",
			"cursor": cursor,
		}
		if diff := cmp.Diff(want, page); diff != "" {
			t.Errorf("history page %d variables mismatch (-want +got):\n%s", i+2, diff)
		}
	}
}

// TestClient_Collect_abortsOnAFailedHistoryPage makes web-app's second history
// page answer with a GraphQL error and asserts the walk aborts at that page
// rather than recording page one as the whole (why: identities in history.go).
func TestClient_Collect_abortsOnAFailedHistoryPage(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	first, err := os.ReadFile(filepath.Join(fixtureRoot, "repos", "web-app", "graphql.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	api.failNext("/graphql",
		scriptedFailure{status: http.StatusOK, body: string(first)},
		scriptedFailure{
			status: http.StatusOK,
			body:   `{"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a GitObject"}]}`,
		},
	)

	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"web-app"})
	if err == nil {
		t.Fatal("Collect() error = nil, want the history page's GraphQL error")
	}
	if !strings.Contains(err.Error(), "NOT_FOUND") {
		t.Errorf("Collect() error = %q, want it to carry GitHub's error", err)
	}
	if got != nil {
		t.Errorf("Collect() returned %d repositories alongside an error, want none", len(got))
	}
	if n := api.callsTo("/graphql"); n != 2 {
		t.Errorf("/graphql saw %d calls, want 2: the walk must stop at the failed page", n)
	}
}

// TestClient_Collect_oneHistoryPage covers the ordinary case: a history that
// fits on the first page costs no request beyond the repository query (the
// other document is the file query), and an identity that is both author and
// committer is recorded once.
func TestClient_Collect_oneHistoryPage(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"mixedCase-flake", "pkg-index"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			api := newFakeAPI(t)
			got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{name})
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			want := []audit.Identity{{Name: "Pat Example", Email: "pat@example.com"}}
			if diff := cmp.Diff(want, got[0].Identities); diff != "" {
				t.Errorf("Identities mismatch (-want +got):\n%s", diff)
			}
			if n := len(api.graphQLVariables()); n != 2 {
				t.Errorf("/graphql saw %d documents, want 2: the repository query and the file query", n)
			}
		})
	}
}

// TestClient_Collect_releasesAndAlternates covers the fields no other fixture
// exercises: a real release history, a capitalised `Justfile`, a Renovate
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
		JustfilePath:   "Justfile",
		Changelog:      true,
		Security:       true,
		SecurityPolicy: "SECURITY.md",
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

// TestClient_Collect_fileFacts covers what the file query reads, end to end:
// mixedCase-flake's lock, workflows and composite action. Only .yml and .yaml
// files are workflows, a binary one is unreadable, and only a directory
// holding an action file is an action.
func TestClient_Collect_fileFacts(t *testing.T) {
	t.Parallel()

	const sha = "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name      string
		flake     audit.Flake
		workflows []audit.Workflow
		actions   []audit.CompositeAction
	}{
		{
			name: "mixedCase-flake",
			flake: audit.Flake{
				HasLock:             true,
				NixpkgsLastModified: at(t, "2025-09-01T00:00:00Z"),
			},
			workflows: []audit.Workflow{
				{
					Path:         ".github/workflows/ci.yml",
					Events:       []string{"pull_request", "push"},
					PushBranches: []string{"main"},
					Concurrency:  audit.Concurrency{Set: true, CancelInProgress: "true"},
					Permissions:  audit.Permissions{Set: true, Scopes: map[string]string{"contents": "read"}},
					Jobs: []audit.Job{{
						ID:             "check",
						TimeoutMinutes: "15",
						Uses: []audit.StepUse{
							{Action: "example-org/action-08@" + sha},
							{Action: "./.github/actions/setup"},
						},
					}},
				},
				{Path: ".github/workflows/workflow-01.yml", ParseError: ".github/workflows/workflow-01.yml: not a text file"},
			},
			actions: []audit.CompositeAction{{
				Path: ".github/actions/setup/action.yml",
				Uses: []audit.StepUse{{Action: "example-org/action-12@" + sha, With: map[string]string{"purge": "true"}}},
			}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			api := newFakeAPI(t)
			got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{tc.name})
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if diff := cmp.Diff(tc.flake, got[0].Flake); diff != "" {
				t.Errorf("Flake mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.workflows, got[0].Workflows); diff != "" {
				t.Errorf("Workflows mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.actions, got[0].Actions); diff != "" {
				t.Errorf("Actions mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestClient_Collect_truncatedWorkflowIsUnreadable: GitHub cuts a large
// blob's text short and says so. Parsing what arrived could pass a check on
// the half of the file that did, so the workflow is recorded as unreadable.
func TestClient_Collect_truncatedWorkflowIsUnreadable(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"web-app"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := []audit.Workflow{{
		Path:       ".github/workflows/workflow-33.yml",
		ParseError: ".github/workflows/workflow-33.yml: too large to read in full",
	}}
	if diff := cmp.Diff(want, got[0].Workflows); diff != "" {
		t.Errorf("Workflows mismatch (-want +got):\n%s", diff)
	}
}

// TestClient_Collect_emptyRepoSkipsFilesQuery: an empty repository has no
// files, so asking for their text would only cost a query.
func TestClient_Collect_emptyRepoSkipsFilesQuery(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	if _, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"blank-repo"}); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, doc := range api.graphQLDocuments() {
		if strings.Contains(doc, "query RepoFiles") {
			t.Error("the file query was sent for an empty repository")
		}
	}
}

// TestClient_Collect_claude covers the Claude file reads end to end. plugin-kit
// is a plugin: its manifest is read first, and only then are the skills,
// command and hooks file its layout implies read, in a second query.
// skill-pack has project skills only, one of them too large to read in full.
func TestClient_Collect_claude(t *testing.T) {
	t.Parallel()

	const description = "Placeholder description."
	tests := []struct {
		name string
		want audit.Claude
		// paths is what each Claude query asks for, in order.
		paths [][]string
	}{
		{
			name: "plugin-kit",
			want: audit.Claude{
				Skills: []audit.ClaudeSkill{
					{
						Path:                   "skills/skill-01/SKILL.md",
						Dir:                    "skill-01",
						Name:                   "skill-01",
						Keys:                   map[string]string{"name": "!!str", "description": "!!str", "disable-model-invocation": "!!bool"},
						HasDescription:         true,
						DescriptionChars:       len(description),
						DisableModelInvocation: true,
						Lines:                  7,
					},
					{
						Path:               "skills/skill-02/SKILL.md",
						Dir:                "skill-02",
						Name:               "skill-02",
						Keys:               map[string]string{"name": "!!str", "description": "!!str", "user-invocable": "!!bool", "hooks": "!!map"},
						HasDescription:     true,
						DescriptionChars:   len(description),
						UserInvocableFalse: true,
						Lines:              13,
					},
				},
				Commands: []audit.ClaudeSkill{{
					Path:             "commands/command-01.md",
					Dir:              "command-01",
					Keys:             map[string]string{"description": "!!str"},
					HasDescription:   true,
					DescriptionChars: len(description),
					Lines:            5,
					WrapsSkill:       "skill-01",
				}},
				Hooks: []audit.ClaudeHook{
					{
						Source: "hooks/hooks.json", Plugin: true, Event: "PreToolUse", Type: "command", Matcher: "Bash",
						Timeout: 30, TimeoutSet: true, Script: "hooks/hook-01.sh", PathForm: "plugin_root", ScriptMode: "100755",
					},
					{Source: "skills/skill-02/SKILL.md", Plugin: true, Event: "PostToolUse", Type: "command", Matcher: "Write"},
				},
				Plugin: audit.ClaudePlugin{
					Present: true,
					Name:    "plugin-kit",
					Version: "1.2.0",
					Fields: []string{
						"$schema", "author", "description", "homepage", "license", "name", "repository", "version",
					},
					OwnMarketplace: true,
				},
			},
			paths: [][]string{
				{".claude-plugin/plugin.json"},
				{"commands/command-01.md", "hooks/hooks.json", "skills/skill-01/SKILL.md", "skills/skill-02/SKILL.md"},
			},
		},
		{
			name: "skill-pack",
			want: audit.Claude{
				Skills: []audit.ClaudeSkill{
					{
						Path:             ".claude/skills/skill-01/SKILL.md",
						Dir:              "skill-01",
						Name:             "skill-01",
						Keys:             map[string]string{"name": "!!str", "description": "!!str"},
						HasDescription:   true,
						DescriptionChars: len(description),
						Lines:            6,
					},
					{
						Path:       ".claude/skills/skill-02/SKILL.md",
						Dir:        "skill-02",
						Unreadable: ".claude/skills/skill-02/SKILL.md: too large to read in full",
					},
				},
			},
			paths: [][]string{{".claude/skills/skill-01/SKILL.md", ".claude/skills/skill-02/SKILL.md"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			api := newFakeAPI(t)
			got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{tc.name})
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if diff := cmp.Diff(tc.want, got[0].Claude); diff != "" {
				t.Errorf("Claude mismatch (-want +got):\n%s", diff)
			}

			// Every path travels as a variable; none is part of the
			// document, so a file name can never be read as query text.
			var asked [][]string
			documents, variables := api.graphQLDocuments(), api.graphQLVariables()
			for i, doc := range documents {
				if !strings.HasPrefix(doc, "query ClaudeFiles") {
					continue
				}
				var paths []string
				for j := 0; ; j++ {
					v, ok := variables[i]["e"+strconv.Itoa(j)]
					if !ok {
						break
					}
					path, found := strings.CutPrefix(v, "HEAD:")
					if !found {
						t.Errorf("variable e%d = %q, want a HEAD: expression", j, v)
					}
					if strings.Contains(doc, path) {
						t.Errorf("the Claude query document contains the path %q", path)
					}
					paths = append(paths, path)
				}
				asked = append(asked, paths)
			}
			if diff := cmp.Diff(tc.paths, asked); diff != "" {
				t.Errorf("Claude query paths mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestClient_Collect_claudeQueryFailureAborts: a Claude file query that fails
// fails the run, as the file query does, rather than recording the files as
// unreadable on GitHub's say-so.
func TestClient_Collect_claudeQueryFailureAborts(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	api.failClaude(scriptedFailure{status: http.StatusOK, body: `{"errors": [{"type": "FORBIDDEN", "message": "denied"}]}`})
	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"skill-pack"})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Errorf("Collect() error = %v, want the Claude query's error", err)
	}
	if got != nil {
		t.Errorf("Collect() = %v, want no repositories", got)
	}
}

// TestClient_Collect_filesQueryFailureAborts: the file query fails the way
// the repository query does. Every other request is passed on to the
// fixtures, so the failure is the file query's alone.
func TestClient_Collect_filesQueryFailureAborts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "errors array",
			body: `{"errors":[{"type":"FORBIDDEN","path":["repository","workflows"],"message":"Resource not accessible"}]}`,
			want: "FORBIDDEN at repository.workflows",
		},
		{name: "no repository", body: `{"data":{"repository":null}}`, want: "no repository in the file query's response"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			api := newFakeAPI(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/graphql" {
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("read body: %v", err)
						return
					}
					if strings.Contains(string(raw), "query RepoFiles") {
						w.Header().Set("Content-Type", "application/json")
						if _, err := w.Write([]byte(tc.body)); err != nil {
							t.Errorf("write: %v", err)
						}
						return
					}
					r.Body = io.NopCloser(strings.NewReader(string(raw)))
				}
				http.Redirect(w, r, api.url()+r.URL.RequestURI(), http.StatusTemporaryRedirect)
			}))
			t.Cleanup(server.Close)

			got, err := newTestClient(t, server.URL).Collect(t.Context(), []string{"shell-scripts"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Collect() error = %v, want it to contain %q", err, tc.want)
			}
			if got != nil {
				t.Errorf("Collect returned %d repositories alongside an error, want none", len(got))
			}
		})
	}
}

// TestClient_Collect_normalNotErrors walks every answer the collector documents
// as ordinary rather than an error (see the n/a notes in rest.go and
// collect.go). Each one must reach the model as a fact rather than aborting
// the run.
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

// TestClient_Collect_keepsTheFullOID pins the forty characters;
// TestLive_headShaNeedsTheFullOID in live_integration_test.go is why.
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
		[]string{"shell-scripts", "web-app", "pkg-index", "blank-repo", "mixedCase-flake", "plugin-kit"}); err != nil {
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

	// The CI and storage reads are GETs a fixture set could stop reaching,
	// which would leave them out of this test without failing it.
	if _, recency := api.runsCalls("shell-scripts"); recency == 0 {
		t.Error("no default-branch runs request; the read-only assertion did not cover it")
	}
	for _, path := range []string{
		"/repos/gh-owner/shell-scripts/actions/runs",
		"/repos/gh-owner/shell-scripts/actions/runs/4000001/jobs",
		"/repos/gh-owner/shell-scripts/actions/caches",
		"/repos/gh-owner/shell-scripts/actions/cache/usage",
		"/repos/gh-owner/shell-scripts/actions/cache/storage-limit",
		"/repos/gh-owner/shell-scripts/actions/artifacts",
	} {
		if api.callsTo(path) == 0 {
			t.Errorf("no request to %s; the read-only assertion did not cover it", path)
		}
	}

	documents := api.graphQLDocuments()
	if len(documents) == 0 {
		t.Fatal("no GraphQL documents recorded; the assertion below would be vacuous")
	}
	// The file query is a second document, held to the same rule.
	if !slices.ContainsFunc(documents, func(d string) bool { return strings.HasPrefix(d, "query RepoFiles") }) {
		t.Error("no file query recorded; the assertion below did not cover it")
	}
	if !slices.ContainsFunc(documents, func(d string) bool { return strings.HasPrefix(d, "query ClaudeFiles") }) {
		t.Error("no Claude file query recorded; the assertion below did not cover it")
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
// repository does, and nothing partial reaches the renderer; Collect's doc
// says why.
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

// TestFakeAPI_abandonedGraphQLRequest holds the fake to what an abandoned
// request means: a client that goes away before its body is whole has not sent
// a bad request, so the fake does not fail the test over it. Collect abandons
// every request in flight once one repository has failed, and how much of a
// body is on the wire by then is the scheduler's choice, so a fake that failed
// here would fail the tests of the abort paths now and then.
func TestFakeAPI_abandonedGraphQLRequest(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", strings.TrimPrefix(api.url(), "http://"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// The length promises more than is sent, which is what the server sees of
	// a request cut off between two writes.
	const cutShort = "POST /graphql HTTP/1.1\r\nHost: fake\r\nContent-Type: application/json\r\n" +
		"Content-Length: 4096\r\n\r\n" + `{"query":"query {`
	if _, err := io.WriteString(conn, cutShort); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The client goes away only once the handler has the request: a
	// connection closed before that never reaches the handler at all, and the
	// test would pass without asking it anything.
	deadline := time.After(10 * time.Second)
	for api.callsTo("/graphql") == 0 {
		select {
		case <-deadline:
			t.Fatal("the fake never saw the request")
		case <-time.After(time.Millisecond):
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Closing the server waits for the handler, so whatever it reported is
	// reported before the test ends.
	api.close()
	if got := len(api.graphQLVariables()); got != 0 {
		t.Errorf("the fake recorded %d queries from a request that never arrived whole, want 0", got)
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

// TestClient_Collect_graphQLErrorNamesField covers a refusal of one field: a
// fine-grained token missing one permission is refused field by field, and
// the error must say which field so the missing permission can be named.
// List indices in a path are JSON numbers, not strings.
func TestClient_Collect_graphQLErrorNamesField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "field",
			path: `["repository","open_pull_requests"]`,
			want: "FORBIDDEN at repository.open_pull_requests: Resource not accessible by personal access token",
		},
		{
			name: "list index",
			path: `["repository","rulesets","nodes",0,"rules"]`,
			want: "FORBIDDEN at repository.rulesets.nodes.0.rules: Resource not accessible by personal access token",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := `{"data":{"repository":null},"errors":[{"type":"FORBIDDEN","path":` + tc.path +
				`,"message":"Resource not accessible by personal access token"}]}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte(body)); err != nil {
					t.Errorf("write: %v", err)
				}
			}))
			t.Cleanup(server.Close)

			_, err := newTestClient(t, server.URL).Collect(t.Context(), []string{"ghost"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Collect() error = %v, want it to contain %q", err, tc.want)
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

// TestClient_Collect_recordsStrictStatusChecks: Strict is true when any
// required_status_checks rule in force asks for the branch to be up to date,
// even if another such rule (from a second ruleset) does not, and false when
// no rule does or there is no such rule at all.
func TestClient_Collect_recordsStrictStatusChecks(t *testing.T) {
	t.Parallel()

	const rulesPath = "/repos/" + testOwner + "/web-app/rules/branches/main"
	tests := []struct {
		name string
		repo string
		body string // replaces the rules response when non-empty
		want bool
	}{
		{name: "strict rule", repo: "web-app", want: true},
		{name: "no status check rule", repo: "pkg-index", want: false},
		{
			name: "only a non-strict rule",
			repo: "web-app",
			body: `[{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":false,"required_status_checks":[{"context":"check-36"}]}}]`,
			want: false,
		},
		{
			name: "one strict and one non-strict rule",
			repo: "web-app",
			body: `[{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":false,"required_status_checks":[{"context":"check-36"}]}},` +
				`{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":true,"required_status_checks":[{"context":"check-37"}]}}]`,
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			api := newFakeAPI(t)
			if tt.body != "" {
				api.failNext(rulesPath, scriptedFailure{status: http.StatusOK, body: tt.body})
			}
			got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{tt.repo})
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if got[0].Branch.Strict != tt.want {
				t.Errorf("Strict = %t, want %t", got[0].Branch.Strict, tt.want)
			}
		})
	}
}

// TestClient_Collect_survivesARateLimit pins the retry policy at the level it
// actually happens: a secondary rate limit on one endpoint mid fan-out; the
// github.go package doc says why the policy exists.
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
	if n := api.callsTo("/graphql"); n != 3 {
		t.Errorf("/graphql saw %d calls, want 3: the limited answer, its retry and the file query", n)
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
		base + "/git/trees/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa000c",
		base + "/actions/runs",
		base + "/actions/runs/4000001/jobs",
		base + "/actions/caches",
		base + "/actions/cache/usage",
		base + "/actions/cache/storage-limit",
		base + "/actions/artifacts",
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

// TestCollect_justfileMatchesAnyCase: just finds a justfile whatever its case,
// so a tree holding `Justfile` has one, and the path is recorded as spelled.
func TestCollect_justfileMatchesAnyCase(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"mixedCase-flake"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !got[0].Files.Justfile {
		t.Error("Files.Justfile = false, want true for a tree holding Justfile")
	}
	if got[0].Files.JustfilePath != "Justfile" {
		t.Errorf("Files.JustfilePath = %q, want Justfile", got[0].Files.JustfilePath)
	}
}

// TestCollect_recordsFilePaths: pkg-index's tree holds one accepted name per
// recorded file, so every field must carry exactly that path.
func TestCollect_recordsFilePaths(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"pkg-index"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := audit.Files{
		Justfile:            true,
		JustfilePath:        "justfile",
		Commitlint:          ".commitlintrc.yml",
		GitCliff:            "cliff.toml",
		Treefmt:             "treefmt.toml",
		Compose:             []string{"compose.yaml", "docker-compose.yml"},
		Typos:               "typos.toml",
		Yamllint:            ".yamllint.yml",
		Markdownlint:        ".markdownlint-cli2.jsonc",
		Prettier:            ".prettierrc.json",
		EditorconfigChecker: ".editorconfig-checker.json",
		Lychee:              "lychee.toml",
		Zizmor:              ".github/zizmor.yml",
		Codecov:             ".github/codecov.yml",
		MkDocs:              "mkdocs.yml",
		LicenseFile:         "LICENSE",
		SecurityPolicy:      "SECURITY.md",
		Codeowners:          ".github/CODEOWNERS",
		PRTemplate:          ".github/pull_request_template.md",
		ClaudeMD:            "CLAUDE.md",
	}
	if diff := cmp.Diff(want, got[0].Files); diff != "" {
		t.Errorf("Files mismatch (-want +got):\n%s", diff)
	}
}

// TestCollect_requestsTheTreeRecursivelyAtHead: the tree is read once, at the
// head commit GraphQL answered with. The fake asserts recursive=1.
func TestCollect_requestsTheTreeRecursivelyAtHead(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	if _, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"shell-scripts"}); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	const path = "/repos/gh-owner/shell-scripts/git/trees/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa000c"
	if n := api.callsTo(path); n != 1 {
		t.Errorf("%s saw %d calls, want 1", path, n)
	}
}

// TestCollect_emptyRepoReadsNoStorage: with no commit no workflow can have
// run, so an empty repository costs no storage reads and records none.
func TestCollect_emptyRepoReadsNoStorage(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"blank-repo"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, c := range api.seen() {
		if strings.Contains(c.path, "/actions/cache") || strings.HasSuffix(c.path, "/actions/artifacts") {
			t.Errorf("called %s for an empty repository", c.path)
		}
	}
	if diff := cmp.Diff(audit.ActionsStorage{}, got[0].Storage); diff != "" {
		t.Errorf("Storage mismatch (-want +got):\n%s", diff)
	}
}

// TestClient_Collect_storageRequestBudget pins what the storage reads cost a
// repository whose lists fit one page: one request to each endpoint.
func TestClient_Collect_storageRequestBudget(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	if _, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"shell-scripts"}); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	base := "/repos/" + testOwner + "/shell-scripts/actions/"
	for _, path := range []string{"caches", "cache/usage", "cache/storage-limit", "artifacts"} {
		if n := api.callsTo(base + path); n != 1 {
			t.Errorf("%s saw %d calls, want 1", base+path, n)
		}
	}
}

// TestCollect_emptyRepoFetchesNoTree: an empty repository has no commit to
// list, so no tree is requested and no path is recorded.
func TestCollect_emptyRepoFetchesNoTree(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"blank-repo"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, c := range api.seen() {
		if strings.Contains(c.path, "/git/trees/") {
			t.Errorf("called %s for an empty repository", c.path)
		}
	}
	if diff := cmp.Diff(audit.Files{}, got[0].Files); diff != "" {
		t.Errorf("Files mismatch (-want +got):\n%s", diff)
	}
}

// TestCollect_truncatedTreeAborts: a truncated listing omits entries, so any
// path decided from it could be a fake gap. The run must fail naming the
// repository and return nothing.
func TestCollect_truncatedTreeAborts(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	api.failNext("/repos/gh-owner/shell-scripts/git/trees/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa000c",
		scriptedFailure{status: http.StatusOK, body: `{"sha":"x","tree":[],"truncated":true}`})

	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"shell-scripts", "web-app"})
	if err == nil {
		t.Fatal("Collect() error = nil, want a failure for a truncated tree")
	}
	if !strings.Contains(err.Error(), "shell-scripts") || !strings.Contains(err.Error(), "truncated") {
		t.Errorf("Collect() error = %q, want it to name shell-scripts and say truncated", err)
	}
	if got != nil {
		t.Errorf("Collect returned %d repositories alongside an error, want none", len(got))
	}
}

// TestClient_Collect_ciRequestBudget pins what CI timing costs: one runs page
// and one default-branch runs read per repository that has a commit to time,
// and one jobs read per run on a sampled commit, never one for a run on any
// other commit. A repository with nothing sampled asks for no runs at all.
func TestClient_Collect_ciRequestBudget(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	if _, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"shell-scripts", "pkg-index"}); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for repo, want := range map[string]int{"shell-scripts": 1, "pkg-index": 0} {
		if timing, recency := api.runsCalls(repo); timing != want || recency != want {
			t.Errorf("%s: %d timing and %d default-branch runs reads, want %d of each", repo, timing, recency, want)
		}
	}
	base := "/repos/" + testOwner + "/shell-scripts/actions/runs"
	want := map[string]int{
		// 4000008 ran on a commit outside the sample; 4000011 is a scheduled
		// run, 4000012 a dispatched one and 4000013 a repository_dispatch one,
		// none of which count.
		base + "/4000008/jobs": 0,
		base + "/4000011/jobs": 0,
		base + "/4000012/jobs": 0,
		base + "/4000013/jobs": 0,
	}
	for _, id := range []string{"4000001", "4000002", "4000003", "4000004", "4000005", "4000006"} {
		want[base+"/"+id+"/jobs"] = 1
	}
	for path, n := range want {
		if got := api.callsTo(path); got != n {
			t.Errorf("%s saw %d calls, want %d", path, got, n)
		}
	}
}

// TestClient_Collect_actionsDisabled: a repository with Actions turned off
// answers the runs list with a 200 and no runs, which is an ordinary answer:
// every sampled commit is recorded with no jobs and not complete, and no jobs
// are asked for.
func TestClient_Collect_actionsDisabled(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	runs := "/repos/" + testOwner + "/web-app/actions/runs"
	// Both runs reads, the timing page and the default-branch one, answer
	// empty.
	none := scriptedFailure{status: http.StatusOK, body: `{"total_count":0,"workflow_runs":[]}`}
	api.failNext(runs, none, none)

	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"web-app"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	want := audit.CI{
		PullRequests: []audit.CICommit{{
			OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0c11", PRNumber: 3, MergedAt: at(t, "2025-09-01T09:00:00Z"),
		}},
		DefaultBranch: []audit.CICommit{{OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0014"}},
	}
	if diff := cmp.Diff(want, got[0].CI); diff != "" {
		t.Errorf("CI mismatch (-want +got):\n%s", diff)
	}
	for _, c := range api.seen() {
		if strings.HasSuffix(c.path, "/jobs") {
			t.Errorf("asked for %s with no runs listed", c.path)
		}
	}
}

// webAppRuns is web-app's runs list with extra appended: its own two runs, a
// pull_request run on the pull request's head and a push run on the
// default-branch commit, then whatever the test adds.
func webAppRuns(extra ...string) string {
	runs := make([]string, 0, 2+len(extra))
	runs = append(runs,
		`{"id":4000010,"name":"ci","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0014","event":"push","status":"completed","created_at":"2025-09-02T01:45:00Z"}`,
		`{"id":4000009,"name":"ci","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0c11","event":"pull_request","status":"completed","created_at":"2025-09-01T08:00:00Z"}`,
	)
	return `{"workflow_runs":[` + strings.Join(append(runs, extra...), ",") + `]}`
}

// collectWebAppWithRuns collects web-app with its runs list replaced by body,
// failing the test if any jobs read is made for ignored, a run that must not
// count. ignored has no jobs fixture, so such a read would also fail Collect.
func collectWebAppWithRuns(t *testing.T, body, ignored string) audit.CI {
	t.Helper()

	api := newFakeAPI(t)
	base := "/repos/" + testOwner + "/web-app/actions/runs"
	api.failNext(base, scriptedFailure{status: http.StatusOK, body: body})
	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"web-app"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if n := api.callsTo(base + "/" + ignored + "/jobs"); n != 0 {
		t.Errorf("run %s was asked for its jobs %d times, want never", ignored, n)
	}
	return got[0].CI
}

// TestClient_Collect_ciIgnoresScheduledRunOnMain: a scheduled run that lands on
// the default-branch head a day later is not CI the push waited on, so the
// commit's wait stays the push's.
func TestClient_Collect_ciIgnoresScheduledRunOnMain(t *testing.T) {
	t.Parallel()

	ci := collectWebAppWithRuns(t, webAppRuns(
		`{"id":4000099,"name":"ci","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0014","event":"schedule","status":"completed","created_at":"2025-09-03T01:45:00Z"}`,
	), "4000099")

	if got := ci.DefaultBranch[0]; !got.Complete || got.WaitSeconds != 140 {
		t.Errorf("Complete = %t, WaitSeconds = %d, want true and 140", got.Complete, got.WaitSeconds)
	}
}

// TestClient_Collect_ciIgnoresDispatchOnPullRequestHead: a run started by hand
// on a pull request's head is not what merging it waited on.
func TestClient_Collect_ciIgnoresDispatchOnPullRequestHead(t *testing.T) {
	t.Parallel()

	ci := collectWebAppWithRuns(t, webAppRuns(
		`{"id":4000098,"name":"ci","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0c11","event":"workflow_dispatch","status":"in_progress","created_at":"2025-09-01T07:00:00Z"}`,
	), "4000098")

	if got := ci.PullRequests[0]; !got.Complete || got.WaitSeconds != 150 {
		t.Errorf("Complete = %t, WaitSeconds = %d, want true and 150", got.Complete, got.WaitSeconds)
	}
}

// TestClient_Collect_ciCountsPushOnPullRequestHead: a workflow triggered by
// the push to a pull request's branch is part of what merging it waited on,
// and some repositories run CI on push alone, so it counts toward the pull
// request's wait like a pull_request run does.
func TestClient_Collect_ciCountsPushOnPullRequestHead(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	base := "/repos/" + testOwner + "/web-app/actions/runs"
	api.failNext(base, scriptedFailure{status: http.StatusOK, body: webAppRuns(
		`{"id":4000097,"name":"docs","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0c11","event":"push","status":"completed","created_at":"2025-09-01T08:00:00Z"}`,
	)})
	api.failNext(base+"/4000097/jobs", scriptedFailure{status: http.StatusOK, body: `{"total_count":1,"jobs":[` +
		`{"name":"docs","status":"completed","started_at":"2025-09-01T08:00:20Z","completed_at":"2025-09-01T08:05:00Z"}]}`})

	got, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"web-app"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	want := audit.CICommit{
		OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0c11", PRNumber: 3, MergedAt: at(t, "2025-09-01T09:00:00Z"),
		Complete: true, WaitSeconds: 300,
		Jobs: []audit.CIJob{{Label: "ci / check", Seconds: 120}, {Label: "docs / docs", Seconds: 280}},
	}
	if diff := cmp.Diff(want, got[0].CI.PullRequests[0]); diff != "" {
		t.Errorf("pull request commit mismatch (-want +got):\n%s", diff)
	}
}

// TestClient_Collect_ciOnlyIgnoredRunsIsNoCI: a commit whose only runs are of
// events that do not count reads as one no workflow ran on.
func TestClient_Collect_ciOnlyIgnoredRunsIsNoCI(t *testing.T) {
	t.Parallel()

	ci := collectWebAppWithRuns(t,
		`{"workflow_runs":[{"id":4000096,"name":"ci","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0014","event":"workflow_run","status":"completed","created_at":"2025-09-02T01:45:00Z"}]}`,
		"4000096")

	if diff := cmp.Diff(audit.CICommit{OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0014"}, ci.DefaultBranch[0]); diff != "" {
		t.Errorf("default-branch commit mismatch (-want +got):\n%s", diff)
	}
}
