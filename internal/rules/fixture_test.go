package rules_test

import (
	"fmt"
	"maps"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// standardTypes is the types table most rules tests judge against, read once
// from testdata/standard-types.toml and shared read-only. A broken fixture is a
// broken test tree rather than a failing test, so it panics.
var standardTypes = sync.OnceValue(func() rules.Types {
	var file struct {
		Types rules.Types `toml:"types"`
	}
	if _, err := toml.DecodeFile(filepath.Join("testdata", "standard-types.toml"), &file); err != nil {
		panic(fmt.Sprintf("decode the standard types fixture: %v", err))
	}
	return file.Types
})

// typesWith is standard-types.toml with check c at word for type typ.
func typesWith(typ string, c rules.Check, word string) rules.Types {
	types := make(rules.Types, len(standardTypes()))
	for name, entries := range standardTypes() {
		// standardTypes is shared read-only, so each table is copied.
		types[name] = maps.Clone(entries)
	}
	types[typ][c.String()] = word
	return types
}

// fixture builds a repository that passes every check its type asks of it, so
// a test can break exactly one thing and see exactly one gap. Anything a test
// wants different, it sets afterwards.
func fixture(name string, typ rules.Type) audit.Repo {
	yes := true
	return audit.Repo{
		Name:             name,
		Type:             string(typ),
		Visibility:       "private",
		Description:      "a repository",
		Topics:           3,
		License:          "MIT",
		OpenPullRequests: 0,
		Branches:         1,
		PushedAt:         time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		DefaultBranch:    "main",
		HeadOID:          "0123456789abcdef0123456789abcdef01234567",
		Files: audit.Files{
			README:         true,
			Gitignore:      true,
			Editorconfig:   true,
			FlakeNix:       true,
			Justfile:       true,
			JustfilePath:   "justfile",
			Changelog:      true,
			Security:       true,
			Contributing:   true,
			CodeOfConduct:  true,
			RenovateConfig: "renovate.json",
			Workflows:      []string{"ci.yml"},

			LicenseFile:    "LICENSE",
			SecurityPolicy: "SECURITY.md",
			PRTemplate:     ".github/pull_request_template.md",
			Commitlint:     ".commitlintrc.yml",
		},
		// The fixture [hermetic] standard's flake, declared, locked, named in
		// flake.nix and wired into the gate, with nixpkgs locked well within
		// the lock-age limit.
		Flake: audit.Flake{
			Inputs:              []string{"github:NixOS/nixpkgs/nixos-unstable", "github:gh-owner/lint-flake"},
			HasLock:             true,
			LockedInputs:        []string{"github:gh-owner/lint-flake", "github:nixos/nixpkgs"},
			NixpkgsLastModified: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
			NamesLint:           true,
			GateFiles:           []string{"run-all-checks"},
		},
		// Renovate refreshes flake.lock: its nix manager runs, with lock-file
		// maintenance.
		Renovate: audit.Renovate{
			MinReleaseAge: "7 days", MinReleaseAgeSource: "renovate.json",
			NixManager: true, LockFileMaintenance: true,
		},
		// ci.yml runs on pull requests and on a push to the default branch,
		// cancels superseded runs and bounds its one job, so every check
		// that reads workflows passes.
		Workflows: []audit.Workflow{{
			Path:         ".github/workflows/ci.yml",
			Events:       []string{"pull_request", "push"},
			PushBranches: []string{"main"},
			Concurrency:  audit.Concurrency{Set: true, CancelInProgress: "true"},
			Jobs:         []audit.Job{{ID: "check", TimeoutMinutes: "15"}},
		}},
		CIState: "SUCCESS",
		Releases: audit.Releases{
			Total:           2,
			LastPublishedAt: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		},
		Rulesets: []audit.Ruleset{
			{Name: "protect-tags", Target: "TAG", Enforcement: "ACTIVE", Include: []string{"refs/tags/**"}},
			{
				Name: "protect-main", Target: "BRANCH", Enforcement: "ACTIVE",
				Include: []string{"~DEFAULT_BRANCH"}, Rules: []string{"PULL_REQUEST"},
			},
		},
		Branch: audit.BranchRules{
			Known:          true,
			Types:          []string{"pull_request", "required_signatures", "required_status_checks"},
			RequiredChecks: []string{"gate (ubuntu-latest)", "commitlint"},
		},
		Settings: audit.Settings{
			HasIssues:                     true,
			VulnerabilityAlerts:           true,
			DependabotSecurityUpdates:     true,
			MergeCommitAllowed:            true,
			MergeCommitTitle:              "PR_TITLE",
			MergeCommitMessage:            "PR_BODY",
			AutoMergeAllowed:              true,
			DeleteBranchOnMerge:           true,
			PrivateVulnerabilityReporting: &yes,
			ActionsPolicy:                 "selected",
			SHAPinningRequired:            true,
			AllowedActions: &audit.AllowedActions{
				GitHubOwnedAllowed: true,
				Patterns:           []string{"step-security/*"},
			},
			DefaultWorkflowPermissions: "read",
			ActionsAccessLevel:         "none",
		},
	}
}

// public turns a fixture public, which is what unlocks the pub rows.
func public(r audit.Repo) audit.Repo {
	r.Visibility = "public"
	r.Settings.SecretScanning = "enabled"
	r.Settings.SecretScanningPushProtection = "enabled"
	r.Settings.ActionsAccessLevel = "" // /access answers 422 on a public repo
	return r
}

// published marks a fixture as meant for a stranger to find and use, which is
// what unlocks the pub+pub'd rows.
func published(r audit.Repo) audit.Repo {
	r.Published = true
	r.Homepage = "https://example.invalid"
	return r
}

// evaluateOne runs one repository through the rules and returns its cells.
func evaluateOne(r audit.Repo) (map[rules.Check]rules.Cell, error) {
	rep, err := evaluateOneReport(r)
	if err != nil {
		return nil, err
	}
	return rep.Repos[0].Cells, nil
}

// evaluateOneReport runs one repository through the rules as of cacheNow, the
// instant the cache and CI-efficiency fixtures are dated against, and returns
// the whole report for tests that read more than the cells.
func evaluateOneReport(r audit.Repo) (*rules.Report, error) {
	return rules.Evaluate(&audit.Snapshot{GeneratedAt: cacheNow, Types: standardTypes(), Identity: fixtureIdentity(), Hermetic: fixtureHermetic(), Repos: []audit.Repo{r}})
}

// mustEvaluateOne is evaluateOneReport for a test that has no use for an
// Evaluate error.
func mustEvaluateOne(t *testing.T, r audit.Repo) *rules.Report {
	t.Helper()
	rep, err := evaluateOneReport(r)
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	return rep
}

// fixtureIdentity is the [identity] table standard-types.toml declares, which
// every snapshot judged against those types needs once any of them judges
// git_identity: a judged check with no standard is an Evaluate error.
func fixtureIdentity() audit.IdentityStandard {
	return audit.IdentityStandard{
		Canonical: "Pat Example <pat@example.com>",
		Match:     " Example <",
		Accepted:  []string{"Pat Example <12345+pat@users.noreply.github.com>"},
	}
}

// fixtureHermetic is the [hermetic] table standard-types.toml declares, which
// every snapshot judged against those types needs once any of them judges
// hermetic_nix, as fixtureIdentity is for git_identity.
func fixtureHermetic() audit.HermeticStandard {
	return audit.HermeticStandard{Flake: "github:gh-owner/lint-flake", Lint: "check-workflow-hermetic"}
}

// toolsRepos builds several private `tools` fixtures at once, which is the
// shape most modal tests want: a uniform account with one deviation.
func toolsRepos(names ...string) []audit.Repo {
	out := make([]audit.Repo, 0, len(names))
	for _, name := range names {
		out = append(out, fixture(name, "tools"))
	}
	return out
}

// publicToolsRepos is toolsRepos, made public.
func publicToolsRepos(names ...string) []audit.Repo {
	out := make([]audit.Repo, 0, len(names))
	for _, name := range names {
		out = append(out, public(fixture(name, "tools")))
	}
	return out
}

// table builds one complete type table: every check listed at a word it
// accepts — required, blocked for direct push, info for the value-only checks,
// "7 days" for the release-age threshold, not_required for git_identity,
// hermetic_nix and the file-path checks — with set overriding individual
// words. git_identity and hermetic_nix default to not_required because most
// tests are about other checks, and a judged one would make each of them
// declare a standard it has no interest in. A key in set that is not a check
// is added as well, so a test can build an unknown-check table the same way.
func table(set map[string]string) map[string]string {
	out := make(map[string]string, len(rules.Checks())+len(set))
	for _, c := range rules.Checks() {
		switch {
		case c == rules.CheckDirectPush:
			out[c.String()] = "blocked"
		case c.ValueOnly():
			out[c.String()] = "info"
		case c.Threshold():
			out[c.String()] = "7 days"
		case c == rules.CheckGitIdentity, c == rules.CheckHermeticNix, c.PathCheck() && c != rules.CheckJustfile && c != rules.CheckRenovate:
			out[c.String()] = rules.OverrideNotRequired
		default:
			out[c.String()] = rules.OverrideRequired
		}
	}
	maps.Copy(out, set)
	return out
}
