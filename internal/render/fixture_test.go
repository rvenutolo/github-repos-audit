package render_test

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// standardTypes is the types table the render tests judge against, read once
// from testdata/standard-types.toml and shared read-only. A broken fixture is a
// broken test tree rather than a failing test, so it panics.
var standardTypes = sync.OnceValue(func() rules.Types {
	var file struct {
		Types rules.Types `toml:"types"`
	}
	if _, err := toml.DecodeFile(filepath.Join("..", "rules", "testdata", "standard-types.toml"), &file); err != nil {
		panic(fmt.Sprintf("decode the standard types fixture: %v", err))
	}
	return file.Types
})

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

// renderedAt is the instant every golden file is rendered against, so the
// relative dates in them are fixed rather than yesterday's.
var renderedAt = time.Date(2026, 9, 5, 7, 30, 0, 0, time.UTC)

func at(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}

// baseRepo is a repository that passes everything its type asks of it.
func baseRepo(name string, typ rules.Type) audit.Repo {
	return audit.Repo{
		Name:             name,
		Type:             string(typ),
		Visibility:       "private",
		Description:      "the " + name + " repository",
		Topics:           0,
		PushedAt:         at(2026, time.August, 24),
		DefaultBranch:    "main",
		HeadOID:          "0123456789abcdef0123456789abcdef01234567",
		OpenPullRequests: 0,
		Branches:         1,
		Files: audit.Files{
			README:         true,
			Gitignore:      true,
			Editorconfig:   true,
			FlakeNix:       true,
			Justfile:       true,
			RenovateConfig: "renovate.json",
			Workflows:      []string{"ci.yml"},
		},
		// The canonical identity, and a bot's that is never the owner's.
		Identities: []audit.Identity{
			{Name: "Pat Example", Email: "pat@example.com"},
			{Name: "renovate[bot]", Email: "29139614+renovate[bot]@users.noreply.github.com"},
		},
		Renovate: audit.Renovate{MinReleaseAge: "7 days", MinReleaseAgeSource: "renovate.json"},
		CIState:  "SUCCESS",
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
			HasIssues:                  true,
			VulnerabilityAlerts:        true,
			DependabotSecurityUpdates:  true,
			MergeCommitAllowed:         true,
			MergeCommitTitle:           "PR_TITLE",
			MergeCommitMessage:         "PR_BODY",
			AutoMergeAllowed:           true,
			DeleteBranchOnMerge:        true,
			ActionsPolicy:              "selected",
			SHAPinningRequired:         true,
			AllowedActions:             &audit.AllowedActions{GitHubOwnedAllowed: true, Patterns: []string{"step-security/*"}},
			DefaultWorkflowPermissions: "read",
			ActionsAccessLevel:         "none",
		},
	}
}

// fullSnapshot is the fixture the golden files are rendered from. It is built
// to exercise the awkward rows rather than to look tidy: a published public
// repository with community files, a content repository whose every tooling
// cell is n/a, a public-but-unpublished repository carrying topics it is not
// judged on, an empty repository, one deviation on each of two settings, a
// minimum release age that falls short and another that is unresolved, and
// git identities that are mixed (beside an accepted one), canonical beside
// an accepted one, consistently non-canonical, only someone else's, and
// absent altogether.
func fullSnapshot() *audit.Snapshot {
	// A published, public software repository, missing two community files.
	soft := baseRepo("mixedCase-flake", "software")
	soft.Visibility = "public"
	soft.Published = true
	soft.Homepage = "https://example.invalid/mixedcase-flake"
	soft.Topics = 4
	soft.License = "MIT"
	soft.Description = "a flake | with a pipe in its description"
	soft.Files.Changelog = true
	soft.Files.Security = true
	soft.Files.RenovateConfig = ".github/renovate.json5"
	// Its release age comes from a preset and falls short of the type's.
	soft.Renovate = audit.Renovate{MinReleaseAge: "3 days", MinReleaseAgeSource: "github>gh-owner/preset-store"}
	soft.Releases = audit.Releases{Total: 4, Drafts: 1, LastPublishedAt: at(2026, time.April, 2)}
	soft.Settings.SecretScanning = "enabled"
	soft.Settings.SecretScanningPushProtection = "enabled"
	soft.Settings.PrivateVulnerabilityReporting = new(true)
	soft.Settings.ActionsAccessLevel = ""
	soft.Settings.HasProjects = true // the one deviation on projects
	// Its history switched identity: mixed. The web-flow noreply identity
	// beside them is accepted and does not count toward it.
	soft.Identities = []audit.Identity{
		{Name: "Pat Example", Email: "12345+pat@users.noreply.github.com"},
		{Name: "Pat Example", Email: "pat@example.com"},
		{Name: "Patrick Example", Email: "patrick@example.org"},
		{Name: "renovate[bot]", Email: "29139614+renovate[bot]@users.noreply.github.com"},
	}
	soft.Rulesets = []audit.Ruleset{
		{Name: "ruleset-02", Target: "TAG", Enforcement: "ACTIVE", Include: []string{"refs/tags/v*"}},
		{
			Name: "protect-main", Target: "BRANCH", Enforcement: "ACTIVE",
			Include: []string{"~DEFAULT_BRANCH"}, Rules: []string{"PULL_REQUEST"},
		},
	}

	// A public content repository: every tooling cell n/a, topics shown but
	// not judged, and a direct-push gap because it is still PR-gated.
	content := baseRepo("cipher-lib", "content")
	content.Visibility = "public"
	content.Topics = 6
	content.License = ""
	content.Files.RenovateConfig = ""
	content.Renovate = audit.Renovate{} // no configuration, so nothing it says
	content.Files.FlakeNix = false
	content.Files.Justfile = false
	content.Files.Workflows = nil
	content.CIState = ""
	content.Settings.SecretScanning = "disabled"
	content.Settings.SecretScanningPushProtection = "disabled"
	content.Settings.PrivateVulnerabilityReporting = new(false)
	content.Settings.ActionsAccessLevel = ""
	// Only someone else has committed: nothing of the owner's to list.
	content.Identities = []audit.Identity{{Name: "Robin Other", Email: "robin@example.org"}}

	// A private tools repository with a CI run in flight and no real gate.
	tools := baseRepo("github-repos-audit", "tools")
	tools.CIState = "PENDING"
	tools.Branches = 4 // three besides the default, so the cell is not a zero
	tools.Branch.RequiredChecks = []string{"merge-gate"}
	// Consistently the wrong identity, beside GitHub's own web-flow committer.
	tools.Identities = []audit.Identity{
		{Name: "GitHub", Email: "noreply@github.com"},
		{Name: "Pat Example", Email: "pat@example.org"},
	}

	// An empty repository: no commits at all, and no rulesets either.
	empty := audit.Repo{
		Name: "go-linter", Type: "config", Visibility: "private",
		Empty: true, Branch: audit.BranchRules{Known: false},
		Settings: tools.Settings,
	}

	// A private config repository with an override that excuses a real gap.
	cfg := baseRepo("web-app", "config")
	cfg.Files.Gitignore = false
	cfg.Overrides = map[string]string{"flake_nix": rules.OverrideNotRequired}
	cfg.Files.FlakeNix = false
	cfg.Settings.HasWiki = true // the one deviation on wiki
	// A preset it extends is missing, so its release age is unresolved.
	cfg.Renovate = audit.Renovate{MinReleaseAgeError: "preset github>gh-owner/preset-store:go: not found"}
	// Its own commits and web-UI merges: canonical and accepted, a pass.
	cfg.Identities = []audit.Identity{
		{Name: "Pat Example", Email: "12345+pat@users.noreply.github.com"},
		{Name: "Pat Example", Email: "pat@example.com"},
		{Name: "renovate[bot]", Email: "29139614+renovate[bot]@users.noreply.github.com"},
	}

	return &audit.Snapshot{
		GeneratedAt: renderedAt,
		Owner:       "gh-owner",
		Types:       standardTypes(),
		Identity:    fixtureIdentity(),
		Repos:       []audit.Repo{soft, content, tools, empty, cfg},
	}
}
