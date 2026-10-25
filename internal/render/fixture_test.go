package render_test

import (
	"fmt"
	"path/filepath"
	"strings"
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
// relative dates in them are fixed rather than tied to the clock.
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
			JustfilePath:   "justfile",
			RenovateConfig: "renovate.json",
			Workflows:      []string{"ci.yml"},

			LicenseFile:    "LICENSE",
			SecurityPolicy: "SECURITY.md",
			PRTemplate:     ".github/pull_request_template.md",
			Commitlint:     ".commitlintrc.yml",
		},
		// nixpkgs locked days before the render.
		Flake: audit.Flake{
			HasLock:             true,
			NixpkgsLastModified: at(2026, time.September, 1),
		},
		// The canonical identity, and a bot's that is never the owner's.
		Identities: []audit.Identity{
			{Name: "Pat Example", Email: "pat@example.com"},
			{Name: "renovate[bot]", Email: "29139614+renovate[bot]@users.noreply.github.com"},
		},
		// Renovate refreshes flake.lock: its nix manager runs, with lock-file
		// maintenance.
		Renovate: audit.Renovate{
			MinReleaseAge: "7 days", MinReleaseAgeSource: "renovate.json", AutomergeMajor: true,
			NixManager: true, LockFileMaintenance: true,
		},
		// ci.yml runs on pull requests into the default branch and on a push
		// to it, cancels superseded runs and bounds its one job.
		Workflows: []audit.Workflow{{
			Path: ".github/workflows/ci.yml", Events: []string{"pull_request", "push"},
			PullRequestBranches: []string{"main"},
			PushBranches:        []string{"main"},
			Concurrency:         audit.Concurrency{Set: true, CancelInProgress: "true"},
			Jobs:                []audit.Job{{ID: "check", TimeoutMinutes: "15"}},
		}},
		CIState: "SUCCESS",
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
			Strict:         true,
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
// absent altogether. Nothing refreshes the published software repository's
// lock, and the tools repository's nixpkgs lock is stale and its Nix cache
// oversized, so the Nix section carries fails beside the passes. Both are Go modules; the software repository's Go cache is a
// useful size, while a side job in the tools repository saved an empty one
// last, so the CI efficiency section carries a fail too, as it does for
// the tools repository's pull-request workflows that run on after a newer
// push supersedes them and for its job with no timeout. Three repositories
// carry Claude Code skills: one passing, one failing every skill check but
// the name, and one whose skill cannot be read; the other two carry none,
// so the Skills table leaves them out.
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
	// Human pull requests and main commits both finished; a skipped run is
	// recorded at zero and left out of the slowest-job mean.
	soft.CI = audit.CI{
		PullRequests: []audit.CICommit{
			ciCommit("0001", 11, at(2026, time.August, 20), false, 580, ciJob("ci / check", 300), ciJob("ci / lint", 40), ciJob("ci / docs", 0)),
			ciCommit("0002", 10, at(2026, time.August, 18), false, 600, ciJob("ci / check", 320), ciJob("ci / lint", 50)),
		},
		DefaultBranch: []audit.CICommit{
			ciCommit("0003", 0, time.Time{}, false, 300, ciJob("ci / check", 280)),
		},
	}
	// Two repositories per commitlint spelling: a tied column names no standard.
	soft.Files.Commitlint = ".commitlintrc.json"
	// Its release age comes from a preset and falls short of the type's, and
	// Renovate runs the nix manager without lock-file maintenance, which on
	// inputs not pinned by rev leaves flake.lock unrefreshed.
	soft.Renovate = audit.Renovate{MinReleaseAge: "3 days", MinReleaseAgeSource: "github>gh-owner/preset-store", NixManager: true}
	soft.Releases = audit.Releases{
		Total: 4, Drafts: 1, LastPublishedAt: at(2026, time.April, 2),
		LatestTag: "v1.2.0", LatestPublishedAt: at(2026, time.April, 2),
	}
	soft.Settings.SecretScanning = "enabled"
	soft.Settings.SecretScanningPushProtection = "enabled"
	soft.Settings.PrivateVulnerabilityReporting = new(true)
	soft.Settings.ActionsAccessLevel = ""
	soft.Settings.HasProjects = true // the one deviation on projects
	// Its default branch ran CI the day before the render, and the newest of
	// its two Linux Nix caches, and its Go cache, are a useful size; a pull
	// request's cache beside them serves no default-branch run.
	soft.Files.GoMod = true
	soft.CI.LastDefaultBranchRun = at(2026, time.September, 4)
	soft.Storage = audit.ActionsStorage{
		Caches: []audit.Cache{
			{Key: "go-Linux-6c7d", Ref: "refs/heads/main", SizeBytes: 95_000_000, CreatedAt: at(2026, time.September, 4)},
			{Key: "nix-Linux-0a1b", Ref: "refs/heads/main", SizeBytes: 180_000_000, CreatedAt: at(2026, time.September, 1)},
			{Key: "nix-Linux-2c3d", Ref: "refs/heads/main", SizeBytes: 206_000_000, CreatedAt: at(2026, time.September, 4)},
			{Key: "nix-Linux-4e5f", Ref: "refs/pull/11/merge", SizeBytes: 900, CreatedAt: at(2026, time.September, 3)},
		},
		CacheUsageBytes: 481_000_900,
		CacheLimitBytes: 10_000_000_000,
		ArtifactCount:   12,
		ArtifactBytes:   206_000_000,
	}
	// Its pull-request workflow cancels superseded runs, and its one job
	// bounds its run time.
	soft.Workflows = []audit.Workflow{{
		Path: ".github/workflows/ci.yml", Events: []string{"pull_request", "push"},
		Concurrency: audit.Concurrency{Set: true, CancelInProgress: "true"},
		Jobs:        []audit.Job{{ID: "check", TimeoutMinutes: "15"}},
	}}
	// Its history switched identity: mixed. The noreply identity GitHub records
	// on web-UI merges beside them is accepted and does not count toward it.
	soft.Identities = []audit.Identity{
		{Name: "Pat Example", Email: "12345+pat@users.noreply.github.com"},
		{Name: "Pat Example", Email: "pat@example.com"},
		{Name: "Patrick Example", Email: "patrick@example.org"},
		{Name: "renovate[bot]", Email: "29139614+renovate[bot]@users.noreply.github.com"},
	}
	// One slash-only skill that passes every skill check, and no command
	// file, so its wrapper cell is n/a.
	// Its one hook passes every hook check, plugin-root path included.
	// It is also a plugin that passes every plugin check: every field, no
	// marketplace of its own, and a version equal to its latest release's tag.
	soft.Claude = audit.Claude{
		Plugin: audit.ClaudePlugin{
			Present: true, Name: "plugin-kit", Version: "1.2.0", Fields: pluginFields(),
		},
		Skills: []audit.ClaudeSkill{projectSkill("skill-01", 45)},
		Hooks: []audit.ClaudeHook{{
			Source: "hooks/hooks.json", Plugin: true, Event: "PreToolUse", Type: "command", Matcher: "Bash",
			Timeout: 10, TimeoutSet: true, Script: "hooks/hook-01.sh", PathForm: "plugin_root", ScriptMode: "100755",
		}},
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
	content.Flake = audit.Flake{} // no flake.nix, so no flake.lock either
	content.Files.Justfile = false
	content.Files.JustfilePath = ""
	content.Files.Workflows = nil
	content.Workflows = nil
	content.CIState = ""
	content.Settings.SecretScanning = "disabled"
	content.Settings.SecretScanningPushProtection = "disabled"
	content.Settings.PrivateVulnerabilityReporting = new(false)
	content.Settings.ActionsAccessLevel = ""
	// Only someone else has committed: nothing of the owner's to list.
	content.Identities = []audit.Identity{{Name: "Robin Other", Email: "robin@example.org"}}
	// It is a plugin the marketplace lists validly, but pinned a release
	// behind one published ten days before the render, so its pin has lagged
	// past the week the pin check allows.
	content.Claude = audit.Claude{Plugin: audit.ClaudePlugin{
		Present: true, Name: "cipher-lib", Version: "1.2.0", Fields: pluginFields(),
	}}
	content.Releases = audit.Releases{
		Total: 2, LastPublishedAt: at(2026, time.August, 26),
		LatestTag: "v1.2.0", LatestPublishedAt: at(2026, time.August, 26),
	}

	// A public plugin that passes every plugin check of its own but is
	// missing from the marketplace.
	kit := baseRepo("plugin-kit", "tools")
	kit.Visibility = "public"
	kit.License = "MIT"
	// No commitlint config, so the two spellings stay tied two to two.
	kit.Files.Commitlint = ""
	kit.Settings.SecretScanning = "enabled"
	kit.Settings.SecretScanningPushProtection = "enabled"
	kit.Settings.PrivateVulnerabilityReporting = new(true)
	kit.Settings.ActionsAccessLevel = ""
	kit.Claude = audit.Claude{Plugin: audit.ClaudePlugin{
		Present: true, Name: "plugin-kit", Version: "1.0.0", Fields: pluginFields(),
	}}
	kit.Releases = audit.Releases{
		Total: 1, LastPublishedAt: at(2026, time.August, 3),
		LatestTag: "v1.0.0", LatestPublishedAt: at(2026, time.August, 3),
	}

	// A private tools repository with a CI run in flight and no real gate.
	tools := baseRepo("github-repos-audit", "tools")
	tools.CIState = "PENDING"
	tools.Branches = 4 // three besides the default, so the cell is not a zero
	tools.Branch.RequiredChecks = []string{"merge-gate"}
	// Its nixpkgs was last locked well over a month ago.
	tools.Flake.NixpkgsLastModified = at(2026, time.July, 20)
	// Its only merged pull requests are a bot's, so PR CI has nothing to show.
	tools.CI = audit.CI{
		PullRequests: []audit.CICommit{ciCommit("0004", 7, at(2026, time.August, 22), true, 90, ciJob("ci / check", 60))},
		DefaultBranch: []audit.CICommit{
			ciCommit("0005", 0, time.Time{}, false, 3900, ciJob("ci / test", 3800)),
		},
	}
	// Its Nix cache has outgrown the ceiling, and the newest of its Go caches
	// is empty, whatever the older one holds. Beside them sit an action's
	// own cache and two caches under a key no check covers, one saved from a
	// pull request, which the cache lists show unjudged.
	tools.Files.GoMod = true
	tools.CI.LastDefaultBranchRun = at(2026, time.September, 5)
	tools.Storage = audit.ActionsStorage{
		Caches: []audit.Cache{
			{Key: "go-Linux-8e9f", Ref: "refs/heads/main", SizeBytes: 120_000_000, CreatedAt: at(2026, time.September, 2)},
			{Key: "go-Linux-a0b1", Ref: "refs/heads/main", SizeBytes: 430, CreatedAt: at(2026, time.September, 5)},
			{Key: "nix-Linux-6a7b", Ref: "refs/heads/main", SizeBytes: 2_200_000_000, CreatedAt: at(2026, time.September, 5)},
			{Key: "trivy-3a4b", Ref: "refs/heads/main", SizeBytes: 61_000_000, CreatedAt: at(2026, time.September, 5)},
			{Key: "custom-5c6d", Ref: "refs/pull/12/merge", SizeBytes: 3_000_000, CreatedAt: at(2026, time.September, 4)},
			{Key: "custom-1e2f", Ref: "refs/heads/main", SizeBytes: 2_000_000, CreatedAt: at(2026, time.September, 5)},
		},
		CacheUsageBytes: 2_386_000_430,
		CacheLimitBytes: 10_000_000_000,
	}
	// Neither of its pull-request workflows cancels superseded runs: one
	// sets concurrency without cancel-in-progress, and the other cancels in
	// one job of two. Of its jobs, one sets a timeout and one does not; the
	// third calls a reusable workflow, which carries its own, so it is not
	// counted.
	tools.Workflows = []audit.Workflow{
		{
			Path: ".github/workflows/ci.yml", Events: []string{"pull_request"},
			Concurrency: audit.Concurrency{Set: true},
			Jobs:        []audit.Job{{ID: "check", TimeoutMinutes: "30"}},
		},
		{
			Path: ".github/workflows/workflow-02.yml", Events: []string{"pull_request_target"},
			Jobs: []audit.Job{
				{ID: "lint", Concurrency: audit.Concurrency{Set: true, CancelInProgress: "true"}},
				{ID: "test", Reusable: true},
			},
		},
	}
	// Two skills and a command file. The second skill quotes its
	// disable-model-invocation, which is a string and so neither sets it nor
	// has the type the key needs, and its description runs past the limit;
	// the command file does nothing but wrap the first skill.
	quoted := projectSkill("skill-02", 612)
	quoted.Keys["disable-model-invocation"] = "!!str"
	quoted.DisableModelInvocation = false
	quoted.DescriptionChars = rules.SkillDescriptionMax + 64
	// It is a plugin too, behind its latest release, missing two manifest
	// fields and carrying a marketplace of its own: it is the account's
	// marketplace, so its Plugin row carries the marketplace-row cells
	// beside its own plugin checks. Being private, it is not judged on
	// being listed.
	tools.Releases = audit.Releases{Total: 2, LatestTag: "v1.2.0", LatestPublishedAt: at(2026, time.March, 5)}
	tools.Claude = audit.Claude{
		Plugin: audit.ClaudePlugin{
			Present: true, Name: "plugin-kit", Version: "1.1.0", OwnMarketplace: true,
			Fields: []string{"$schema", "author", "description", "name", "repository", "version"},
		},
		Skills: []audit.ClaudeSkill{projectSkill("skill-01", 120), quoted},
		Commands: []audit.ClaudeSkill{{
			Path: ".claude/commands/command-01.md", Dir: "command-01",
			Keys:           map[string]string{"description": "!!str", "disable-model-invocation": "!!bool"},
			HasDescription: true, DescriptionChars: 30, DisableModelInvocation: true, Lines: 6, WrapsSkill: "skill-01",
		}},
		// Two project hooks: one on a tool event with no matcher, no timeout
		// and a script that is not executable, and one that passes. Neither is
		// a plugin hook, so the plugin-root cell is n/a.
		Hooks: []audit.ClaudeHook{
			{
				Source: ".claude/settings.json", Event: "PostToolUse", Type: "command",
				Script: "hooks/hook-01.sh", PathForm: "project_dir", ScriptMode: "100644",
			},
			{
				Source: ".claude/settings.json", Event: "SessionStart", Type: "command",
				Timeout: 5, TimeoutSet: true, Script: "hooks/hook-02.sh", PathForm: "project_dir", ScriptMode: "100755",
			},
		},
	}
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
	cfg.Files.Commitlint = ".commitlintrc.json"
	cfg.Overrides = map[string]string{"flake_nix": rules.OverrideNotRequired}
	cfg.Files.FlakeNix = false
	cfg.Flake = audit.Flake{}
	cfg.Settings.HasWiki = true // the one deviation on wiki
	// Its one skill's frontmatter is never closed, so every skill check
	// that judges it fails as unreadable; its hooks file is not JSON, so
	// every hook check does too, except the plugin-root check, which never
	// judges a project settings file.
	// Its plugin manifest is not JSON, so every plugin check fails too.
	cfg.Claude = audit.Claude{
		Plugin: audit.ClaudePlugin{Present: true, Unreadable: ".claude-plugin/plugin.json: not valid JSON: unexpected end of JSON input"},
		Skills: []audit.ClaudeSkill{{
			Path: ".claude/skills/skill-01/SKILL.md", Dir: "skill-01",
			Unreadable: "the frontmatter is not closed by a --- line",
		}},
		Hooks: []audit.ClaudeHook{{Source: ".claude/settings.json", Unreadable: "not valid JSON: unexpected end of JSON input"}},
	}
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
		Repos:       []audit.Repo{soft, content, tools, empty, cfg, kit},
		// One entry pins the software repository to its latest release,
		// one pins the content repository a release behind, and one names
		// a repository outside the audit and carries a version; the public
		// plugin-kit has no entry.
		Marketplace: audit.ClaudeMarketplace{
			Repo: "github-repos-audit",
			Entries: []audit.MarketplaceEntry{
				{
					Name: "plugin-kit", Repo: "gh-owner/mixedCase-flake", Ref: "v1.2.0", SHA: pinSHA(1),
					ReleaseTag: true, TagCommit: pinSHA(1), PluginName: "plugin-kit",
				},
				{
					Name: "cipher-lib", Repo: "gh-owner/cipher-lib", Ref: "v1.1.0", SHA: pinSHA(2),
					ReleaseTag: true, TagCommit: pinSHA(2), PluginName: "cipher-lib",
				},
				{
					Name: "skill-pack", Repo: "gh-owner/skill-pack", Ref: "v1.0.0", SHA: pinSHA(3),
					HasVersion: true, Problem: "not an audited repository",
				},
			},
		},
	}
}

// pluginFields is every manifest field plugin_manifest_fields requires.
func pluginFields() []string {
	return []string{"$schema", "author", "description", "homepage", "license", "name", "repository", "version"}
}

// pinSHA is the nth fake commit a marketplace entry pins.
func pinSHA(n int) string {
	return fmt.Sprintf("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa%04d", n)
}

// projectSkill is a skill under .claude/skills that passes every skill
// check: slash-only, named for its directory, with only known keys and a
// short description.
func projectSkill(dir string, lines int) audit.ClaudeSkill {
	return audit.ClaudeSkill{
		Path: ".claude/skills/" + dir + "/SKILL.md",
		Dir:  dir,
		Name: dir,
		Keys: map[string]string{
			"name": "!!str", "description": "!!str", "disable-model-invocation": "!!bool",
		},
		HasDescription:         true,
		DescriptionChars:       80,
		DisableModelInvocation: true,
		Lines:                  lines,
	}
}

// ciCommit is a finished commit's checks. Its OID is thirty-six a's and the
// four hex digits of seed, the synthetic shape internal/fixturevocab accepts.
func ciCommit(seed string, pr int, merged time.Time, bot bool, wait int64, jobs ...audit.CIJob) audit.CICommit {
	return audit.CICommit{
		OID:         strings.Repeat("a", 36) + seed,
		PRNumber:    pr,
		MergedAt:    merged,
		AuthorBot:   bot,
		Complete:    true,
		WaitSeconds: wait,
		Jobs:        jobs,
	}
}

func ciJob(label string, seconds int64) audit.CIJob {
	return audit.CIJob{Label: label, Seconds: seconds}
}

// driftSnapshot is an otherwise clean account whose repos.toml disagrees with
// it in both directions: hook-guard exists with no entry, and media-server
// has an entry and no repository.
func driftSnapshot() *audit.Snapshot {
	return &audit.Snapshot{
		GeneratedAt: renderedAt,
		Owner:       "gh-owner",
		Types:       standardTypes(),
		Identity:    fixtureIdentity(),
		Repos:       []audit.Repo{baseRepo("alpha", "tools"), baseRepo("bravo", "tools")},
		Drift: audit.ConfigDrift{
			Unconfigured: []string{"hook-guard"},
			StaleEntries: []string{"media-server"},
		},
	}
}
