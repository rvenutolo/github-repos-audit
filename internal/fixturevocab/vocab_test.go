package fixturevocab_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// fakeOwner is the only account a fixture may name.
const fakeOwner = "gh-owner"

// fakeRepos is every repository name a fixture may carry. Add a name only
// after confirming it is not a repository on a real account.
//
// "github-repos-audit" is this tool's own name, which the render snapshot
// golden legitimately carries alongside the fake names (it audits itself in
// that fixture). "alpha" and "bravo" are synthetic literals from
// internal/render/block_test.go: they were never fetched from a real
// account, so internal/render/testdata/golden/clean.md — which predates the
// account scrub and was untouched by it — carries them safely.
// "preset-store" is the repository the API fixtures' Renovate configs extend
// presets from; no fixture directory of its own exists. "lint-flake" is the
// flake the API fixtures' flake.nix and flake.lock name as an input; it has
// no fixture directory either.
var fakeRepos = []string{
	"blank-repo", "cipher-lib", "compiler-config", "config-files", "go-linter",
	"hook-guard", "java-demo", "media-server", "mixedCase-flake", "pkg-index",
	"private-notes", "recipe-site", "shell-scripts", "updater", "web-app",
	"github-repos-audit", "alpha", "bravo", "preset-store", "lint-flake",
}

// fakeIdentityNames and fakeIdentityEmails are the only git identities a
// fixture or golden may carry. A captured history would carry the account
// holder's real name and address, which is exactly what this test exists to
// keep out; the bot and GitHub entries are GitHub's own public identities,
// and 12345+pat@users.noreply.github.com stands for the fake owner's GitHub
// noreply address.
var (
	fakeIdentityNames  = []string{"Pat Example", "Patrick Example", "Robin Other", "renovate[bot]", "GitHub"}
	fakeIdentityEmails = []string{
		"pat@example.com", "Pat@Example.com", "pat@example.org", "patrick@example.org", "robin@example.org",
		"29139614+renovate[bot]@users.noreply.github.com", "noreply@github.com", "12345+pat@users.noreply.github.com",
	}
)

// rule says what the scalar at a key path may hold. want describes the
// allowed form for the failure message; the offending value is never printed.
type rule struct {
	path *regexp.Regexp
	ok   func(v any) bool
	want string
}

func str(v any) (string, bool) { s, ok := v.(string); return s, ok }

func matches(re string) func(any) bool {
	r := regexp.MustCompile(re)
	return func(v any) bool {
		switch x := v.(type) {
		case string:
			return r.MatchString(x)
		case json.Number:
			return r.MatchString(x.String())
		}
		return false
	}
}

func isFakeRepo(v any) bool { s, ok := str(v); return ok && slices.Contains(fakeRepos, s) }

func isFakeIdentityName(v any) bool {
	s, ok := str(v)
	return ok && slices.Contains(fakeIdentityNames, s)
}

func isFakeIdentityEmail(v any) bool {
	s, ok := str(v)
	return ok && slices.Contains(fakeIdentityEmails, s)
}

func isOwnerSlashRepo(v any) bool {
	s, ok := str(v)
	owner, name, found := strings.Cut(s, "/")
	return ok && found && owner == fakeOwner && slices.Contains(fakeRepos, name)
}

// presetRef finds owner/repo pairs where Renovate would read them: after a
// github> or local> prefix, or bare at the start of a string or after a
// quote, bracket or space. ".github/renovate.json" is a path, not a pair,
// and is left alone because a dot cannot start a match.
var presetRef = regexp.MustCompile(`(?:github>|local>|^|["'\[\s])([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)`)

// namesOnlyFakeRepos accepts a string whose every owner/repo pair is
// gh-owner and a fake repository. A Renovate preset reference or the error
// text quoting one names a repository the same way a full_name does, so it
// is held to the same vocabulary.
func namesOnlyFakeRepos(v any) bool {
	s, ok := str(v)
	if !ok {
		return false
	}
	for _, m := range presetRef.FindAllStringSubmatch(s, -1) {
		if m[1] != fakeOwner || !slices.Contains(fakeRepos, m[2]) {
			return false
		}
	}
	return true
}

// flakeRef finds a flake reference to a GitHub repository, as flake.nix
// writes one ("github:<owner>/<repo>"), and lockOwner and lockRepo find the
// same two halves as flake.lock spells them, as separate JSON keys. Neither
// form is a pair presetRef sees: a colon or a key/value split is not one of
// its separators.
var (
	flakeRef  = regexp.MustCompile(`github:([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)`)
	lockOwner = regexp.MustCompile(`"owner"\s*:\s*"([^"]*)"`)
	lockRepo  = regexp.MustCompile(`"repo"\s*:\s*"([^"]*)"`)
)

// publicPair matches the owner/repository pairs a file fixture may name beyond
// the fake vocabulary: nixpkgs, which every flake names and which belongs to
// no audited account, and the synthetic actions the allowlist fixtures
// already use.
var publicPair = regexp.MustCompile(`^(NixOS/nixpkgs|example-org/action-\d{2})$`)

// isFakeFileText holds the text of a file fixture (flake.nix, flake.lock, a
// workflow, a composite action, a gate script) to the fake vocabulary: every
// repository it names, in any of the ways those files name one, is gh-owner
// and a fake repository, or one of publicPair.
func isFakeFileText(v any) bool {
	s, ok := str(v)
	if !ok {
		return false
	}
	allowed := func(owner, repo string) bool {
		return (owner == fakeOwner && slices.Contains(fakeRepos, repo)) || publicPair.MatchString(owner+"/"+repo)
	}
	for _, re := range []*regexp.Regexp{presetRef, flakeRef} {
		for _, m := range re.FindAllStringSubmatch(s, -1) {
			if !allowed(m[1], m[2]) {
				return false
			}
		}
	}
	for _, m := range lockOwner.FindAllStringSubmatch(s, -1) {
		if m[1] != fakeOwner && m[1] != "NixOS" {
			return false
		}
	}
	for _, m := range lockRepo.FindAllStringSubmatch(s, -1) {
		if !slices.Contains(fakeRepos, m[1]) && m[1] != "nixpkgs" {
			return false
		}
	}
	return true
}

// lockedRef is a flake reference as audit.json records one: flake.nix's
// input URL as written, or flake.lock's GitHub node lower-cased.
var lockedRef = regexp.MustCompile(`^github:([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)(?:/[^?#]*)?$`)

// isFakeFlakeRef accepts a recorded flake reference naming gh-owner and a fake
// repository, or nixpkgs, in any letter case: flake.lock's form is
// lower-cased, so "github:gh-owner/mixedcase-flake" is still the fake name.
func isFakeFlakeRef(v any) bool {
	s, ok := str(v)
	if !ok {
		return false
	}
	m := lockedRef.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	if strings.EqualFold(m[1]+"/"+m[2], "NixOS/nixpkgs") {
		return true
	}
	return strings.EqualFold(m[1], fakeOwner) &&
		slices.ContainsFunc(fakeRepos, func(r string) bool { return strings.EqualFold(r, m[2]) })
}

// pipeDescription is the one deliberate escaping fixture: a description
// containing a pipe character, which proves the Markdown renderer escapes
// table cells correctly. It names no account.
const pipeDescription = "a flake | with a pipe in its description"

func isDescription(v any) bool {
	s, ok := str(v)
	if !ok {
		return v == nil
	}
	if s == "" || s == "Placeholder description." || s == pipeDescription {
		return true
	}
	name, found := strings.CutPrefix(s, "the ")
	name, suffixed := strings.CutSuffix(name, " repository")
	return found && suffixed && slices.Contains(fakeRepos, name)
}

// isHomepage allows the scrubbed API fixtures' https://example.com/<fake>
// form and the render fixtures' https://example.invalid/<slug> form, where
// slug is the lowercased fake repository name.
func isHomepage(v any) bool {
	s, ok := str(v)
	if !ok {
		return v == nil
	}
	if s == "" {
		return true
	}
	if name, found := strings.CutPrefix(s, "https://example.com/"); found {
		return slices.Contains(fakeRepos, name)
	}
	if slug, found := strings.CutPrefix(s, "https://example.invalid/"); found {
		for _, name := range fakeRepos {
			if strings.ToLower(name) == slug {
				return true
			}
		}
	}
	return false
}

func anything(any) bool { return true }

// isNil rejects everything but JSON null: for a field this repository's
// fixtures never populate with a non-null value, null is the only shape that
// carries no risk of a future re-recording smuggling in real content.
func isNil(v any) bool { return v == nil }

// optional wraps a scalar rule so a JSON null also passes: several timestamp
// and content-marker fields are null whenever GitHub has nothing to report
// (an unpushed repository, a missing file) and a real value otherwise.
func optional(ok func(any) bool) func(any) bool {
	return func(v any) bool { return v == nil || ok(v) }
}

var (
	oid       = `^(a{36}[0-9a-f]{4}|(0123456789abcdef){2}01234567)$`
	timestamp = `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$`
	// workflow also admits a README, the one file a workflows directory
	// holds that is not a workflow, so the fixtures can show it is skipped.
	workflow = `^(ci\.ya?ml|workflow-\d{2}\.ya?ml|README\.md)$`
	ruleset  = `^(ruleset-\d{2}|protect-(main|tags))$`
	// checkContext covers the API fixtures' synthetic "check-NN" contexts
	// and the render snapshot golden's real CI job names (commitlint,
	// gate (ubuntu-latest), merge-gate): generic job names, not account data.
	checkContext = `^(check-\d{2}|commitlint|gate \(ubuntu-latest\)|merge-gate)$`
	// pattern covers the synthetic "example-org/action-NN@*" fixture and
	// "step-security/*", a real, publicly documented GitHub Actions
	// marketplace publisher used as an allowed-actions example; neither
	// names the audited account.
	pattern = `^(example-org/action-\d{2}@\*|step-security/\*)$`
	// ciWorkflow and ciJob are generic names: conventional workflow names and
	// common job names. A real repository's own workflow and job names could
	// say what it is, so nothing else passes.
	ciWorkflow = `^(ci|docs)$`
	ciJob      = `^(check|test|lint|docs)$`
)

// keep is a scalar that carries no account data: a flag, an enum, a count, a
// generic ref pattern or GitHub's own error text.
func keep(paths ...string) rule {
	return rule{path: regexp.MustCompile(`^(` + strings.Join(paths, "|") + `)$`), ok: anything, want: "kept"}
}

// keepNil is a scalar this fixture set only ever records as null: a content
// marker or optional REST field with no populated example. A future
// re-recording that fills one in forces this test to make a decision
// instead of letting the real content pass silently.
func keepNil(paths ...string) rule {
	return rule{path: regexp.MustCompile(`^(` + strings.Join(paths, "|") + `)$`), ok: isNil, want: "null (never populated in this fixture set)"}
}

// renovateProbes is the alternation of the Renovate config probes. A
// present one is an object whose oid, text and flags are ruled separately;
// an absent one is null.
const renovateProbes = `(renovate_json|renovate_jsonc|renovate_json5|renovate_github_json|renovate_github_jsonc|renovate_github_json5|renovate_gitlab_json|renovate_gitlab_jsonc|renovate_gitlab_json5|renovaterc|renovaterc_json|renovaterc_jsonc|renovaterc_json5)`

// fileProbes is the alternation of the file query's single-file probes. The
// flake_nix alias is shared with the repository query, whose probe answers
// an oid where this one answers text.
const fileProbes = `(flake_nix|flake_lock|run_all_checks|justfile|dot_justfile|capital_justfile)`

// isFakeBase64Content decodes a contents-API content field — base64 wrapped
// with newlines, as GitHub sends it — and holds the text to namesOnlyFakeRepos.
// Content that does not decode is rejected: it cannot be checked.
func isFakeBase64Content(v any) bool {
	s, ok := str(v)
	if !ok {
		return false
	}
	text, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(s, "\n", ""))
	return err == nil && namesOnlyFakeRepos(string(text))
}

// treePath is every path a fixture git tree may list.
const treePath = `^(README\.md|CHANGELOG\.md|CONTRIBUTING\.md|SECURITY\.md|CLAUDE\.md|LICENSE|` +
	`\.gitignore|\.editorconfig|flake\.nix|[Jj]ustfile|\.justfile|renovate\.json|` +
	`\.commitlintrc\.yml|cliff\.toml|treefmt\.toml|compose\.yaml|typos\.toml|\.yamllint\.yml|` +
	`\.markdownlint-cli2\.jsonc|\.prettierrc\.json|\.editorconfig-checker\.json|lychee\.toml|mkdocs\.yml|` +
	`stacks|stacks/media|stacks/media/docker-compose\.yml|vendor/sub|` +
	`\.github|\.github/(SECURITY\.md|renovate\.json|workflows|workflows/(ci|workflow-33)\.yml|` +
	`zizmor\.yml|codecov\.yml|CODEOWNERS|pull_request_template\.md))$`

var apiRules = []rule{
	{regexp.MustCompile(`^(\[\]\.)?name$`), isFakeRepo, "a fake repository name"},
	{regexp.MustCompile(`^((\[\]\.)?full_name|\[\]\.ruleset_source)$`), isOwnerSlashRepo, "gh-owner/<fake>"},
	{regexp.MustCompile(`^login$`), func(v any) bool { return v == fakeOwner }, "gh-owner"},
	{regexp.MustCompile(`^(\[\]\.)?id$`), matches(`^1000\d{3}$`), "a synthetic repository id"},
	{regexp.MustCompile(`^(\[\]\.)?node_id$`), matches(`^R_ghowner\d{6}$`), "a synthetic node id"},
	{regexp.MustCompile(`^\[\]\.ruleset_id$`), matches(`^2000\d{3}$`), "a synthetic ruleset id"},
	{regexp.MustCompile(`integration_id$`), matches(`^3000\d{3}$`), "a synthetic integration id"},
	{regexp.MustCompile(`\.oid$`), matches(oid), "a pattern oid"},
	{regexp.MustCompile(`^sha$`), matches(oid), "a pattern oid"},
	// A tree listing is repository content, so a path is held to the closed
	// set of names the tools' configuration tables accept plus a few
	// structural ones; a real repository's file names would not match.
	{regexp.MustCompile(`^tree\[\]\.path$`), matches(treePath), "a path from the fixture tree vocabulary"},
	{regexp.MustCompile(`^(data\.repository\.)?description$`), isDescription, "empty or the placeholder"},
	{regexp.MustCompile(`^(homepage|data\.repository\.homepage_url)$`), isHomepage, "empty or https://example.com/<fake>"},
	{regexp.MustCompile(`^selected_actions_url$`), matches(`^https://api\.github\.com/repositories/1000\d{3}/actions/permissions/selected-actions$`), "a url with a synthetic id"},
	{regexp.MustCompile(`rulesets\.nodes\[\]\.name$`), matches(ruleset), "a synthetic ruleset name"},
	{regexp.MustCompile(`workflows\.entries\[\]\.name$`), matches(workflow), "a synthetic workflow file"},
	// The file query's listings. Each entry's name is a file a real
	// repository could be recognised by, so it is held to generic names, and
	// each file's text to isFakeFileText.
	{regexp.MustCompile(`^data\.repository\.ci\.entries\[\]\.name$`), matches(`^(check-\d{2}|lib\.sh)$`), "a generic .ci script name"},
	{regexp.MustCompile(`^data\.repository\.actions\.entries\[\]\.name$`), matches(`^(setup|action-\d{2}|README\.md)$`), "a generic action directory name"},
	{regexp.MustCompile(`^data\.repository\.actions\.entries\[\]\.object\.entries\[\]\.name$`), matches(`^(action\.ya?ml|README\.md)$`), "an action file name"},
	{regexp.MustCompile(`^data\.repository\.` + fileProbes + `\.text$`), optional(isFakeFileText), "null or text naming only gh-owner/<fake> repositories"},
	{regexp.MustCompile(`^data\.repository\.(workflows|ci|actions\.entries\[\]\.object)\.entries\[\]\.object\.text$`), optional(isFakeFileText), "null or text naming only gh-owner/<fake> repositories"},
	{regexp.MustCompile(`required_status_checks\[\]\.context$`), matches(checkContext), "a synthetic check context"},
	{regexp.MustCompile(`^patterns_allowed\[\]$`), matches(pattern), "a synthetic actions pattern"},
	{regexp.MustCompile(`(^|\.)(created_at|updated_at|pushed_at|published_at)$`), optional(matches(timestamp)), "empty or a timestamp"},
	{regexp.MustCompile(`^size$`), matches(`^(0|1024)$`), "0 or 1024"},
	// A Renovate config's text is repository content, and the only account
	// data it can carry is a preset reference naming a repository, so it is
	// held to the same vocabulary as a full_name.
	{regexp.MustCompile(`^data\.repository\.` + renovateProbes + `\.text$`), namesOnlyFakeRepos, "text naming only gh-owner/<fake> repositories"},
	// A preset file read through the contents API is the same content,
	// base64-encoded, so it is decoded before the same check.
	{regexp.MustCompile(`^content$`), isFakeBase64Content, "base64 of text naming only gh-owner/<fake> repositories"},
	// A commit's author and committer are the one place a fixture could carry
	// a person rather than a repository, so both halves are held to the fake
	// identity vocabulary. History lives under two roots — page one rides with
	// the repository query, later pages come back under object — so these are
	// anchored at the end only. GitHub answers null for a half it cannot read.
	{regexp.MustCompile(`history\.nodes\[\]\.(author|committer)\.name$`), optional(isFakeIdentityName), "null or a fake identity name"},
	{regexp.MustCompile(`history\.nodes\[\]\.(author|committer)\.email$`), optional(isFakeIdentityEmail), "null or a fake identity email"},
	{regexp.MustCompile(`history\.page_info\.end_cursor$`), optional(matches(`^cursor-\d{2}$`)), "null or a synthetic cursor"},
	// The Actions runs and jobs lists. A run id identifies a run on the real
	// account as surely as a repository id does, so it is synthetic too.
	{regexp.MustCompile(`^workflow_runs\[\]\.id$`), matches(`^4000\d{3}$`), "a synthetic run id"},
	{regexp.MustCompile(`^workflow_runs\[\]\.name$`), matches(ciWorkflow), "a generic workflow name"},
	{regexp.MustCompile(`^workflow_runs\[\]\.head_sha$`), matches(oid), "a pattern oid"},
	{regexp.MustCompile(`^jobs\[\]\.name$`), matches(ciJob), "a generic job name"},
	{regexp.MustCompile(`^jobs\[\]\.(started_at|completed_at)$`), optional(matches(timestamp)), "null or a timestamp"},
	{regexp.MustCompile(`^data\.repository\.merged_pull_requests\.nodes\[\]\.merged_at$`), matches(timestamp), "a timestamp"},
	// The Actions storage lists. A cache key is written by the workflow that
	// saved it and can carry a hash of the repository's own files, and an
	// artifact name is the workflow's choice, so both are synthetic; the ids
	// identify entries on the real account and are synthetic too. A ref is a
	// generic branch or pull-request ref.
	{regexp.MustCompile(`^actions_caches\[\]\.id$`), matches(`^6000\d{3}$`), "a synthetic cache id"},
	{regexp.MustCompile(`^actions_caches\[\]\.key$`), matches(`^((go|nix)-(Linux|macOS)-key-\d{2}|example-action-\d{2})$`), "a synthetic cache key"},
	{regexp.MustCompile(`^actions_caches\[\]\.ref$`), matches(`^refs/(heads/main|pull/\d+/merge)$`), "a generic ref"},
	{regexp.MustCompile(`^actions_caches\[\]\.last_accessed_at$`), matches(timestamp), "a timestamp"},
	{regexp.MustCompile(`^artifacts\[\]\.id$`), matches(`^5000\d{3}$`), "a synthetic artifact id"},
	{regexp.MustCompile(`^artifacts\[\]\.name$`), matches(`^artifact-\d{2}$`), "a synthetic artifact name"},
	{regexp.MustCompile(`^artifacts\[\]\.expires_at$`), matches(timestamp), "a timestamp"},
	{regexp.MustCompile(`^data\.repository\.merged_pull_requests\.nodes\[\]\.author\.type$`), matches(`^(User|Bot)$`), "User or Bot"},
	keep(
		`access_level`, `allowed_actions`, `\[\]\.archived`, `archived`, `\[\]\.disabled`, `disabled`,
		`\[\]\.fork`, `fork`, `\[\]\.private`, `private`, `\[\]\.visibility`, `visibility`,
		`\[\]\.default_branch`, `default_branch`, `has_issues`, `has_wiki`, `has_projects`,
		`has_discussions`, `has_pages`, `is_template`, `allow_forking`, `web_commit_signoff_required`,
		`paused`, `verified_allowed`, `can_approve_pull_request_reviews`,
		`default_workflow_permissions`, `enabled`, `github_owned_allowed`, `sha_pinning_required`,
		`open_issues_count`, `truncated`, `message`, `documentation_url`, `status`, `\[\]\.type`, `tree\[\]\.type`,
		`\[\]\.ruleset_source_type`,
		// A ruleset parameter's value is never account data (a branch-name
		// pattern, a boolean, a count): keep accepts it unconditionally;
		// this is deliberate, not an oversight.
		`\[\]\.parameters\.[a-z_]+(\[\])?`,
		`security_and_analysis\.[a-z_]+\.status`,
		`data\.repository\.(auto_merge_allowed|delete_branch_on_merge|has_issues|has_wiki|has_projects|has_discussions|merge_commit_allowed|merge_commit_message|merge_commit_title|rebase_merge_allowed|squash_merge_allowed|visibility|vulnerability_alerts)`,
		`data\.repository\.(branches|open_pull_requests|releases|topics)\.total_count`,
		`data\.repository\.default_branch_ref\.name`,
		`data\.repository\.default_branch_ref\.target\.status_check_rollup\.state`,
		`data\.repository\.license_info\.spdx_id`,
		`data\.repository\.releases\.nodes\[\]\.is_draft`,
		`data\.repository\.rulesets\.nodes\[\]\.(enforcement|target|conditions\.ref_name\.include\[\]|rules\.nodes\[\]\.type)`,
		`data\.repository\.`+renovateProbes+`\.(is_truncated|is_binary)`,
		`data\.repository\.`+fileProbes+`\.(is_truncated|is_binary)`,
		`data\.repository\.(workflows|ci|actions\.entries\[\]\.object)\.entries\[\]\.(type|object\.is_truncated|object\.is_binary)`,
		`data\.repository\.actions\.entries\[\]\.type`,
		`data\.repository\.(default_branch_ref\.target|object)\.history\.page_info\.has_next_page`,
		// A pull request number, a run's event, a run's or job's status and a
		// list's total are counts and enums.
		`data\.repository\.merged_pull_requests\.nodes\[\]\.number`,
		`(workflow_runs|jobs)\[\]\.status`, `workflow_runs\[\]\.event`, `total_count`,
		// Storage sizes, counts and the expired flag.
		`(actions_caches|artifacts)\[\]\.size_in_bytes`, `artifacts\[\]\.expired`,
		`active_caches_size_in_bytes`, `active_caches_count`, `max_cache_size_gb`,
		// The contents API's envelope: "file" and "base64" are GitHub's words.
		`type`, `encoding`,
	),
	// keepNil covers fields this fixture set never records with a real
	// value: a REST field only some repositories' fixtures include, or a
	// GraphQL content marker (a file's presence) whose non-null shape is an
	// object recursed into and already ruled (an .oid, a .name, a .state).
	keepNil(
		`license`, `language`, `security_and_analysis`,
		`data\.repository\.(license_info|default_branch_ref|workflows)`,
		`data\.repository\.default_branch_ref\.target\.status_check_rollup`,
		`data\.repository\.(readme_md|readme_github_md|readme_docs_md|readme_rst|readme_txt|readme_plain)`,
		`data\.repository\.(gitignore|editorconfig|flake_nix|changelog_md)`,
		`data\.repository\.(security_root|security_github|security_docs)`,
		`data\.repository\.(contributing_root|contributing_github|contributing_docs)`,
		`data\.repository\.(coc_root|coc_github|coc_docs)`,
		`data\.repository\.`+renovateProbes,
		`data\.repository\.(`+fileProbes+`|actions|ci)`,
		// A null actor: GitHub could not resolve who wrote or committed it.
		`data\.repository\.(default_branch_ref\.target|object)\.history\.nodes\[\]\.(author|committer)`,
		// A deleted account's pull request.
		`data\.repository\.merged_pull_requests\.nodes\[\]\.author`,
	),
}

var snapshotRules = []rule{
	{regexp.MustCompile(`^owner$`), func(v any) bool { return v == fakeOwner }, "gh-owner"},
	{regexp.MustCompile(`^repos\[\]\.name$`), isFakeRepo, "a fake repository name"},
	// A drifted name is a repository name like any other, and in a real
	// report it is a real one.
	{regexp.MustCompile(`^config_drift\.(unconfigured|stale_entries)\[\]$`), isFakeRepo, "a fake repository name"},
	{regexp.MustCompile(`^repos\[\]\.description$`), isDescription, "empty, the placeholder or 'the <fake> repository'"},
	{regexp.MustCompile(`^repos\[\]\.homepage$`), isHomepage, "empty or https://example.com/<fake> or https://example.invalid/<slug>"},
	{regexp.MustCompile(`^repos\[\]\.head_oid$`), matches(oid), "a pattern oid"},
	{regexp.MustCompile(`^repos\[\]\.files\.workflows\[\]$`), matches(workflow), "a synthetic workflow file"},
	{regexp.MustCompile(`^repos\[\]\.rulesets\[\]\.name$`), matches(ruleset), "a synthetic ruleset name"},
	{regexp.MustCompile(`^repos\[\]\.branch\.required_checks\[\]$`), matches(checkContext), "a synthetic check context"},
	{regexp.MustCompile(`^repos\[\]\.settings\.allowed_actions\.patterns\[\]$`), matches(pattern), "a synthetic actions pattern"},
	{regexp.MustCompile(`^(generated_at|repos\[\]\.pushed_at|repos\[\]\.releases\.last_published_at)$`), optional(matches(timestamp)), "empty or a timestamp"},
	{regexp.MustCompile(`^repos\[\]\.identities\[\]\.name$`), isFakeIdentityName, "a fake identity name"},
	{regexp.MustCompile(`^repos\[\]\.identities\[\]\.email$`), isFakeIdentityEmail, "a fake identity email"},
	// The [identity] table is the account holder's real identity in a real
	// report, so a golden may carry only the fixture standard.
	{regexp.MustCompile(`^identity\.canonical$`), func(v any) bool { return v == "Pat Example <pat@example.com>" }, "the fixture canonical identity"},
	{regexp.MustCompile(`^identity\.match$`), func(v any) bool { return v == " Example <" }, "the fixture identity match"},
	{regexp.MustCompile(`^identity\.accepted\[\]$`), func(v any) bool { return v == "Pat Example <12345+pat@users.noreply.github.com>" }, "the fixture accepted identity"},
	// The [hermetic] table names the account and a repository of it in a
	// real report, so a golden may carry only the fixture standard.
	{regexp.MustCompile(`^hermetic\.flake$`), func(v any) bool { return v == "github:gh-owner/lint-flake" }, "the fixture hermetic flake"},
	{regexp.MustCompile(`^hermetic\.lint$`), func(v any) bool { return v == "check-workflow-hermetic" }, "the fixture hermetic lint"},
	{regexp.MustCompile(`^repos\[\]\.flake\.(inputs|locked_inputs)\[\]$`), isFakeFlakeRef, "github:gh-owner/<fake> or nixpkgs"},
	{regexp.MustCompile(`^repos\[\]\.flake\.gate_files\[\]$`), matches(`^(run-all-checks|justfile|\.justfile|Justfile|\.ci/[A-Za-z0-9._-]+)$`), "a gate file path"},
	{regexp.MustCompile(`^repos\[\]\.flake\.nixpkgs_last_modified$`), matches(timestamp), "a timestamp"},
	// Recorded CI facts: commits carry the synthetic oid, dates are
	// timestamps, and a job label is a generic "<workflow> / <job>" pair like
	// the API fixtures'.
	{regexp.MustCompile(`^repos\[\]\.ci\.(pull_requests|default_branch)\[\]\.oid$`), matches(oid), "a pattern oid"},
	{regexp.MustCompile(`^repos\[\]\.ci\.pull_requests\[\]\.merged_at$`), matches(timestamp), "a timestamp"},
	{regexp.MustCompile(`^repos\[\]\.ci\.(pull_requests|default_branch)\[\]\.jobs\[\]\.label$`), matches(`^ci / (check|test|lint|docs)$`), "a generic job label"},
	{regexp.MustCompile(`^repos\[\]\.renovate\.(min_release_age_source|min_release_age_error)$`), namesOnlyFakeRepos, "text naming only gh-owner/<fake> repositories"},
	keep(
		`types\.[a-z0-9_-]+\.[a-z_]+`,
		`repos\[\]\.(branches|ci_state|default_branch|empty|license|open_pull_requests|published|topics|type|visibility)`,
		`repos\[\]\.branch\.(known|types\[\])`,
		`repos\[\]\.ci\.(pull_requests|default_branch)\[\]\.(author_bot|complete|pr_number|wait_seconds)`,
		`repos\[\]\.ci\.(pull_requests|default_branch)\[\]\.jobs\[\]\.seconds`,
		`repos\[\]\.files\.[a-z_]+`,
		`repos\[\]\.flake\.(has_lock|names_lint)`,
		`repos\[\]\.overrides\.[a-z_]+`,
		`repos\[\]\.releases\.(drafts|total)`,
		// A duration such as "7 days" is never account data.
		`repos\[\]\.renovate\.min_release_age`,
		`repos\[\]\.rulesets\[\]\.(enforcement|include\[\]|rules\[\]|target)`,
		`repos\[\]\.settings\.[a-z_]+`,
		`repos\[\]\.settings\.allowed_actions\.(github_owned_allowed|verified_allowed)`,
	),
}

// check walks a decoded JSON document and returns one problem per scalar that
// no rule allows. A path with no rule at all is a problem too, so a field
// added by a re-recording forces a decision instead of slipping through.
func check(doc any, rules []rule) []string {
	var problems []string
	var walk func(node any, path string)
	walk = func(node any, path string) {
		switch n := node.(type) {
		case map[string]any:
			for k, v := range n {
				p := k
				if path != "" {
					p = path + "." + k
				}
				walk(v, p)
			}
		case []any:
			for _, v := range n {
				walk(v, path+"[]")
			}
		default:
			for _, r := range rules {
				if r.path.MatchString(path) {
					if !r.ok(node) {
						problems = append(problems, fmt.Sprintf("%s: want %s", path, r.want))
					}
					return
				}
			}
			problems = append(problems, path+": no rule for this key path")
		}
	}
	walk(doc, "")
	slices.Sort(problems)
	return slices.Compact(problems)
}

// checkFixtureDirNames requires every directory segment beneath dir (the
// recorded API fixtures' repos/ directory) to be a name in fakeRepos. The
// fixture scrub's one manual step was renaming these directories away from
// real repository names, and nothing else checks that it stuck: the content
// scans in check() and checkMarkdown() never look at a path. Problems name
// the offending segment's position, never its text, matching this test's
// own rule of never printing the leaked value.
func checkFixtureDirNames(dir string) ([]string, error) {
	var problems []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == dir || !d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		for i, seg := range strings.Split(rel, string(filepath.Separator)) {
			if !slices.Contains(fakeRepos, seg) {
				problems = append(problems, fmt.Sprintf("directory segment %d: not a fake repository name", i+1))
			}
		}
		return nil
	})
	return problems, err
}

func decode(t *testing.T, raw []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return doc
}

// githubHostPatterns decomposes a link on a GitHub-adjacent host into the two
// path segments that name an owner and a repository, one pattern per
// GitHub-adjacent host shape a golden could plausibly link to (only
// github.com appears in the goldens; the others exist so a re-recording on
// those hosts is decomposed rather than failed closed by githubHostLike). A
// pattern that matches
// pins down exactly where owner and repository sit in that host's URL. The
// leading (?i) matches the host case-insensitively (DNS names are), while
// the capture groups stay literal so "mixedCase-flake" is still compared
// against fakeRepos exactly, case included.
var githubHostPatterns = []*regexp.Regexp{
	// github.com and www.github.com: /<owner>/<repo>.
	regexp.MustCompile(`(?i)(?:www\.)?github\.com/([^/\s)"]+)/([^/\s)"#?]+)`),
	// gist.github.com: /<owner>/<gist-id>.
	regexp.MustCompile(`(?i)gist\.github\.com/([^/\s)"]+)/([^/\s)"#?]+)`),
	// raw.githubusercontent.com: /<owner>/<repo>/<ref>/<path>.
	regexp.MustCompile(`(?i)raw\.githubusercontent\.com/([^/\s)"]+)/([^/\s)"#?]+)`),
	// codeload.github.com: /<owner>/<repo>/<archive form>.
	regexp.MustCompile(`(?i)codeload\.github\.com/([^/\s)"]+)/([^/\s)"#?]+)`),
	// img.shields.io/github/<metric>/<owner>/<repo>[/...].
	regexp.MustCompile(`(?i)img\.shields\.io/github/[^/\s)"]+/([^/\s)"]+)/([^/\s)"#?]+)`),
}

// githubHostLike flags any URL that names a GitHub-adjacent host, whether or
// not one of githubHostPatterns can decompose it into owner and repository.
// A host this test has never seen must fail rather than pass silently, since
// a future re-recording could carry a real owner and repository on it.
// Matched case-insensitively for the same reason as githubHostPatterns.
var githubHostLike = regexp.MustCompile(`(?i)github\.com|githubusercontent\.com|github\.io|shields\.io/github`)

// avatarLink identifies an account by its numeric id without naming it, so
// it is rejected outright rather than run through owner/repository checks.
var avatarLink = regexp.MustCompile(`(?i)avatars\.githubusercontent\.com/u/\d+`)

// githubIOLink matches a github.io Pages host case-insensitively.
var githubIOLink = regexp.MustCompile(`(?i)github\.io`)

// checkMarkdown allows only gh-owner/<fake> links on known GitHub-adjacent
// hosts, rejects a github.io or an account-avatar host outright, and fails
// closed on any other GitHub-adjacent host it cannot decompose. It also
// requires the gap lists' bare repository names and every table's leading
// "Repository" column to be in fakeRepos, runs a "Description" column
// through isDescription, and holds every line of the Git identities section
// to fake repositories and fake identities, because those are prose and free
// text that no link-shaped pattern ever reaches.
//
// gapListName matches a gap-list bullet's trailing "— name[, name...]",
// e.g. "- **No README** (1) — go-linter, web-app".
var gapListName = regexp.MustCompile(`^- \*\*.+\*\* \(\d+\) — (.+)$`)

// identityBullet matches a Git identities bullet, e.g.
// "- **web-app** — `Pat Example <pat@example.com>` (canonical) — mixed".
// Group 1 is the repository, group 2 the identity list.
var identityBullet = regexp.MustCompile(`^- \*\*(.+?)\*\* — (.+?)(?: — (?:mixed|not canonical))?$`)

// identityList is the whole of a bullet's identity list: backticked
// identities, each optionally tagged canonical or accepted — never both —
// separated by ", ". Anything else between the spans — free text that could
// carry a real name — fails it.
var identityList = regexp.MustCompile("^`[^`]+`(?: \\((?:canonical|accepted)\\))?(?:, `[^`]+`(?: \\((?:canonical|accepted)\\))?)*$")

// identitySpan extracts each backticked identity from an identityList.
var identitySpan = regexp.MustCompile("`([^`]+)`")

// isFakeIdentity reports whether s, written "Name <email>", is a fake name and
// a fake address. Split by hand on the last " <" rather than through
// internal/rules, so this test depends on nothing it is policing.
func isFakeIdentity(s string) bool {
	i := strings.LastIndex(s, " <")
	if i < 0 || !strings.HasSuffix(s, ">") {
		return false
	}
	return isFakeIdentityName(s[:i]) && isFakeIdentityEmail(s[i+2:len(s)-1])
}

// checkIdentityLine holds one line of the Git identities section to the fake
// vocabulary. The section is prose, so it fails closed: any line in it that is
// not a well-formed bullet over fake repositories and fake identities is a
// problem.
func checkIdentityLine(n int, line string) []string {
	m := identityBullet.FindStringSubmatch(line)
	if m == nil || !identityList.MatchString(m[2]) {
		return []string{fmt.Sprintf("line %d: a Git identities line that is not a well-formed bullet", n)}
	}
	var problems []string
	if !slices.Contains(fakeRepos, m[1]) {
		problems = append(problems, fmt.Sprintf("line %d: a Git identities repository outside the fake vocabulary", n))
	}
	for _, span := range identitySpan.FindAllStringSubmatch(m[2], -1) {
		if !isFakeIdentity(span[1]) {
			problems = append(problems, fmt.Sprintf("line %d: a git identity outside the fake vocabulary", n))
		}
	}
	return problems
}

// tableRow splits a GFM table row on "|", trimming each cell and the row's
// own bounding pipes. A backslash-escaped pipe (how escape() in
// internal/render/markdown.go represents a literal "|" inside a cell, as
// the mixedCase-flake description golden exercises) does not split the row;
// the escape is left in place for the caller to undo where it matters.
func tableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	var fields []string
	var cur strings.Builder
	escaped := false
	for _, r := range trimmed {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			cur.WriteRune(r)
			escaped = true
		case r == '|':
			fields = append(fields, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	fields = append(fields, strings.TrimSpace(cur.String()))
	return fields
}

// tableSeparatorCell matches a GFM table separator cell: a bare run of
// dashes with optional alignment colons.
var tableSeparatorCell = regexp.MustCompile(`^:?-+:?$`)

// tableSeparatorRow reports whether every cell of a table row is a
// tableSeparatorCell, the row GFM requires directly below a table's header.
func tableSeparatorRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if !tableSeparatorCell.MatchString(c) {
			return false
		}
	}
	return true
}

// tableLinkCell matches a Markdown link cell's display text ("[name](url)").
var tableLinkCell = regexp.MustCompile(`^\[([^\]]+)\]`)

// tableCellName extracts the display text of a Markdown link cell; a cell
// that is not a link is taken as the name itself.
func tableCellName(cell string) string {
	if m := tableLinkCell.FindStringSubmatch(cell); m != nil {
		return m[1]
	}
	return cell
}

// driftIntroLine is the one prose line the Configuration drift section carries.
// It is spelled out here, not imported, so this test depends on nothing it is
// policing; a section line that is not this one or a well-formed bullet fails.
const driftIntroLine = "`repos.toml` and the account disagree. " +
	"Add a `[repos.<name>]` table for each repository that needs one, and remove each stale entry: " +
	"its repository was deleted, archived or turned into a fork. " +
	"Everything below covers only the repositories both name."

// driftUnconfiguredBullet and driftStaleBullet match the section's two
// bullets, e.g. "- **Stale `repos.toml` entry** (1) — `media-server`". Group 1
// is the name list. The names carry markup (a link, or backticks), so the
// generic gap-list check, which compares a bare name, cannot hold them.
var (
	driftUnconfiguredBullet = regexp.MustCompile("^- \\*\\*Needs a `repos\\.toml` entry\\*\\* \\(\\d+\\) — (.+)$")
	driftStaleBullet        = regexp.MustCompile("^- \\*\\*Stale `repos\\.toml` entry\\*\\* \\(\\d+\\) — (.+)$")
	driftLink               = regexp.MustCompile(`^\[([^\]]+)\]\(https://github\.com/gh-owner/([^)]+)\)$`)
	driftCode               = regexp.MustCompile("^`([^`]+)`$")
)

// checkDriftLine holds one line of the Configuration drift section to the fake
// vocabulary. The section names repositories in link text, in link targets and
// inside backticks, so it fails closed: a line that is not the intro or a
// well-formed bullet over fakeRepos is a problem, and so is any name that is
// not in fakeRepos, whichever of the three places it sits.
func checkDriftLine(n int, line string) []string {
	if line == driftIntroLine {
		return nil
	}
	var problems []string
	switch {
	case driftUnconfiguredBullet.MatchString(line):
		for item := range strings.SplitSeq(driftUnconfiguredBullet.FindStringSubmatch(line)[1], ", ") {
			m := driftLink.FindStringSubmatch(item)
			if m == nil || m[1] != m[2] || !slices.Contains(fakeRepos, m[1]) {
				problems = append(problems, fmt.Sprintf("line %d: an unconfigured drift name that is not a link to a fake repository", n))
			}
		}
	case driftStaleBullet.MatchString(line):
		for item := range strings.SplitSeq(driftStaleBullet.FindStringSubmatch(line)[1], ", ") {
			m := driftCode.FindStringSubmatch(item)
			if m == nil || !slices.Contains(fakeRepos, m[1]) {
				problems = append(problems, fmt.Sprintf("line %d: a stale drift entry that is not a fake repository in backticks", n))
			}
		}
	default:
		problems = append(problems, fmt.Sprintf("line %d: a Configuration drift line that is neither the intro nor a well-formed bullet", n))
	}
	return problems
}

func checkMarkdown(text string) []string {
	var problems []string
	lines := strings.Split(text, "\n")
	// inDrift is true from the "## Configuration drift" heading to the next
	// heading; blank lines inside it are separators and carry no text.
	inDrift := false
	descCol := -1 // index of the current table's Description column, if any; -1 outside a table or when it has none.
	inTable := false
	// inIdentities is true from the "## Git identities" heading until the blank
	// line that closes its bullet list; identityLines counts the lines seen.
	inIdentities, identityLines := false, 0
	for i, line := range lines {
		n := i + 1
		switch {
		case strings.HasPrefix(line, "## "):
			inDrift = line == "## Configuration drift"
			inIdentities, identityLines = line == "## Git identities", 0
		case inIdentities && strings.TrimSpace(line) == "":
			inIdentities = identityLines == 0 // the blank below the heading
		case inIdentities:
			identityLines++
			problems = append(problems, checkIdentityLine(n, line)...)
		case inDrift && strings.TrimSpace(line) != "":
			problems = append(problems, checkDriftLine(n, line)...)
		}
		switch {
		case githubIOLink.MatchString(line):
			problems = append(problems, fmt.Sprintf("line %d: a github.io link", n))
		case avatarLink.MatchString(line):
			problems = append(problems, fmt.Sprintf("line %d: an avatar link that identifies an account by id", n))
		case inIdentities:
			// A Git identities line has already been held, span by span, to the
			// fake identity vocabulary by checkIdentityLine, which also rejects
			// anything that is not a well-formed bullet; the only other lines
			// that reach here are the fixed "## Git identities" heading and the
			// blank line below it, both safe. An address there may legitimately
			// end in users.noreply.github.com — the account's GitHub noreply
			// identity — which is not a link and names no repository, so the
			// host scan has nothing to decompose and would only misfire.
		default:
			decomposed := false
			for _, re := range githubHostPatterns {
				for _, m := range re.FindAllStringSubmatch(line, -1) {
					decomposed = true
					if m[1] != fakeOwner || !slices.Contains(fakeRepos, m[2]) {
						problems = append(problems, fmt.Sprintf("line %d: a GitHub-adjacent link outside the fake vocabulary", n))
					}
				}
			}
			if !decomposed && githubHostLike.MatchString(line) {
				problems = append(problems, fmt.Sprintf("line %d: a GitHub-adjacent host this check cannot decompose into owner and repository", n))
			}
		}

		// The drift bullets have the gap lists' shape but their names carry
		// markup, and checkDriftLine has already held them.
		if m := gapListName.FindStringSubmatch(line); m != nil && !inDrift {
			for name := range strings.SplitSeq(m[1], ", ") {
				if !slices.Contains(fakeRepos, name) {
					problems = append(problems, fmt.Sprintf("line %d: a gap-list name outside the fake vocabulary", n))
				}
			}
		}

		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			inTable, descCol = false, -1
			continue
		}
		cells := tableRow(trimmed)
		if tableSeparatorRow(cells) {
			inTable = true
			continue
		}
		if i+1 < len(lines) && tableSeparatorRow(tableRow(strings.TrimSpace(lines[i+1]))) {
			// This row is the header directly above a separator row: record
			// where its Description column is, but do not check its own
			// text against the fake vocabulary or isDescription.
			descCol = -1
			for col, header := range cells {
				if header == "Description" {
					descCol = col
					break
				}
			}
			continue
		}
		if !inTable || len(cells) == 0 {
			continue
		}
		if name := tableCellName(cells[0]); !slices.Contains(fakeRepos, name) {
			problems = append(problems, fmt.Sprintf("line %d: a table Repository cell outside the fake vocabulary", n))
		}
		if descCol >= 0 && descCol < len(cells) {
			desc := strings.ReplaceAll(cells[descCol], `\|`, "|")
			if !isDescription(desc) {
				problems = append(problems, fmt.Sprintf("line %d: a table Description cell outside the fake vocabulary", n))
			}
		}
	}
	return problems
}

func TestCheck_rejectsAccountData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  string
	}{
		{name: "real-looking owner", doc: `{"full_name": "someone/web-app"}`},
		{name: "unknown repository", doc: `{"name": "not-in-the-vocabulary"}`},
		{name: "id outside the synthetic pattern", doc: `{"id": 987654321}`},
		{name: "unknown key path", doc: `{"brand_new_field": true}`},
		{name: "an unconfigured name outside the vocabulary", doc: `{"config_drift": {"unconfigured": ["not-in-the-vocabulary"]}}`},
		{name: "a stale entry outside the vocabulary", doc: `{"config_drift": {"stale_entries": ["not-in-the-vocabulary"]}}`},
		{name: "real description", doc: `{"description": "My personal media stack"}`},
		{name: "renovate text naming a real preset", doc: `{"data": {"repository": {"renovate_json": {"text": "{\"extends\": [\"github>someone/real-repo\"]}"}}}}`},
		// base64 of {"extends": ["github>someone/real-repo"]}
		{name: "preset content naming a real preset", doc: `{"content": "eyJleHRlbmRzIjogWyJnaXRodWI+c29tZW9uZS9yZWFsLXJlcG8iXX0=\n"}`},
		{name: "preset content that is not base64", doc: `{"content": "not base64!"}`},
		{name: "a present renovate probe with a real flag field", doc: `{"data": {"repository": {"renovate_json": {"is_huge": true}}}}`},
		{name: "a history author outside the identity vocabulary", doc: `{"data": {"repository": {"default_branch_ref": {"target": {"history": {"nodes": [{"author": {"name": "Someone Real", "email": "pat@example.com"}}]}}}}}}`},
		{name: "a later history page's committer email outside the vocabulary", doc: `{"data": {"repository": {"object": {"history": {"nodes": [{"committer": {"name": "Pat Example", "email": "someone@real.invalid"}}]}}}}}`},
		{name: "flake.nix naming a real input", doc: `{"data": {"repository": {"flake_nix": {"text": "inputs.x.url = \"github:someone/real-repo\";"}}}}`},
		{name: "flake.lock naming a real owner", doc: `{"data": {"repository": {"flake_lock": {"text": "{\"original\": {\"owner\": \"someone\", \"repo\": \"lint-flake\"}}"}}}}`},
		{name: "flake.lock naming a real repository", doc: `{"data": {"repository": {"flake_lock": {"text": "{\"original\": {\"owner\": \"gh-owner\", \"repo\": \"real-repo\"}}"}}}}`},
		{name: "a workflow using a real action", doc: `{"data": {"repository": {"workflows": {"entries": [{"object": {"text": "- uses: someone/real-action@v1"}}]}}}}`},
		{name: "a gate script under a real name", doc: `{"data": {"repository": {"ci": {"entries": [{"name": "deploy-media-stack"}]}}}}`},
		{name: "a history cursor that is not synthetic", doc: `{"data": {"repository": {"object": {"history": {"page_info": {"end_cursor": "Y3Vyc29yOnYyOpK5"}}}}}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := check(decode(t, []byte(tc.doc)), apiRules); len(got) == 0 {
				t.Errorf("check(%s) found no problem, want one", tc.name)
			}
		})
	}
}

// TestCheck_rejectsAForeignIdentityStandard: a golden audit.json may record
// only the fixture [identity] table, never an account holder's own.
func TestCheck_rejectsAForeignIdentityStandard(t *testing.T) {
	t.Parallel()

	for _, doc := range []string{
		`{"identity": {"canonical": "Jane Doe <jane@corp.test>", "match": " Example <"}}`,
		`{"identity": {"canonical": "Pat Example <pat@example.com>", "match": "(?i)jane"}}`,
		`{"identity": {"canonical": "Pat Example <pat@example.com>", "match": " Example <", "accepted": ["Jane Doe <jane@corp.test>"]}}`,
	} {
		if got := check(decode(t, []byte(doc)), snapshotRules); len(got) == 0 {
			t.Errorf("check(%s) found no problem, want one", doc)
		}
	}
	fixture := `{"identity": {"canonical": "Pat Example <pat@example.com>", "match": " Example <", "accepted": ["Pat Example <12345+pat@users.noreply.github.com>"]}}`
	if got := check(decode(t, []byte(fixture)), snapshotRules); len(got) != 0 {
		t.Errorf("check(the fixture standard) = %q, want no problem", got)
	}
}

// TestCheck_rejectsAForeignHermeticStandard: a golden audit.json may record
// only the fixture [hermetic] table and flake references to the fake
// vocabulary, never an account's own flake.
func TestCheck_rejectsAForeignHermeticStandard(t *testing.T) {
	t.Parallel()

	for _, doc := range []string{
		`{"hermetic": {"flake": "github:someone/real-flake", "lint": "check-workflow-hermetic"}}`,
		`{"hermetic": {"flake": "github:gh-owner/lint-flake", "lint": "someone-lint"}}`,
		`{"repos": [{"flake": {"inputs": ["github:someone/lint-flake"]}}]}`,
		`{"repos": [{"flake": {"locked_inputs": ["github:gh-owner/real-repo"]}}]}`,
		`{"repos": [{"flake": {"inputs": ["git+https://example.invalid/lint-flake"]}}]}`,
		`{"repos": [{"flake": {"gate_files": ["scripts/real-name"]}}]}`,
	} {
		if got := check(decode(t, []byte(doc)), snapshotRules); len(got) == 0 {
			t.Errorf("check(%s) found no problem, want one", doc)
		}
	}
	fixture := `{"hermetic": {"flake": "github:gh-owner/lint-flake", "lint": "check-workflow-hermetic"}, ` +
		`"repos": [{"flake": {"inputs": ["github:NixOS/nixpkgs/nixos-unstable", "github:gh-owner/lint-flake"], ` +
		`"has_lock": true, "locked_inputs": ["github:gh-owner/mixedcase-flake", "github:nixos/nixpkgs"], ` +
		`"names_lint": true, "gate_files": [".ci/check", "run-all-checks"]}}]}`
	if got := check(decode(t, []byte(fixture)), snapshotRules); len(got) != 0 {
		t.Errorf("check(the fixture standard) = %q, want no problem", got)
	}
}

// TestNamesOnlyFakeRepos: a preset reference, and error text quoting one,
// may name only gh-owner and a fake repository; a config path is not a pair.
func TestNamesOnlyFakeRepos(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want bool
	}{
		{in: "github>gh-owner/preset-store", want: true},
		{in: "preset github>gh-owner/preset-store:go: not found", want: true},
		{in: ".github/renovate.json5", want: true},
		{in: "github>someone/real-repo", want: false},
	}
	for _, tc := range tests {
		if got := namesOnlyFakeRepos(tc.in); got != tc.want {
			t.Errorf("namesOnlyFakeRepos(%q) = %t, want %t", tc.in, got, tc.want)
		}
	}
}

func TestCheck_problemsNeverQuoteTheValue(t *testing.T) {
	t.Parallel()

	got := check(decode(t, []byte(`{"name": "secret-marker-value"}`)), apiRules)
	if len(got) == 0 || strings.Contains(strings.Join(got, "\n"), "secret-marker-value") {
		t.Errorf("check() = %q, want a problem that does not quote the value", got)
	}
}

func TestCheckFixtureDirNames_rejectsUnknownDirectoryName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "marker-repo"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	problems, err := checkFixtureDirNames(dir)
	if err != nil {
		t.Fatalf("checkFixtureDirNames: %v", err)
	}
	if len(problems) == 0 {
		t.Error("checkFixtureDirNames found no problem, want one for a directory named outside the fake vocabulary")
	}
	if strings.Contains(strings.Join(problems, "\n"), "marker-repo") {
		t.Errorf("checkFixtureDirNames(%q) quoted the offending name", problems)
	}
}

func TestCheckFixtureDirNames_acceptsFakeRepoDirectories(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "web-app"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	problems, err := checkFixtureDirNames(dir)
	if err != nil {
		t.Fatalf("checkFixtureDirNames: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("checkFixtureDirNames() = %v, want none", problems)
	}
}

func TestCheckMarkdown_rejectsForeignLinks(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"[x](https://github.com/someone/web-app)",
		"[x](https://github.com/gh-owner/not-in-the-vocabulary)",
		"[x](https://someone.github.io/web-app/)",
		"[x](https://www.github.com/someone/web-app)",
		"[x](https://gist.github.com/someone/web-app)",
		"[x](https://raw.githubusercontent.com/someone/web-app/main/README.md)",
		"[x](https://codeload.github.com/someone/web-app/tar.gz/refs/heads/main)",
		"![badge](https://img.shields.io/github/license/someone/web-app)",
		"![avatar](https://avatars.githubusercontent.com/u/12345)",
		// media.githubusercontent.com looks like a GitHub host but matches
		// none of the decomposable shapes, so it must fail closed.
		"[x](https://media.githubusercontent.com/media/gh-owner/web-app/main/img.png)",
		// Hostnames are case-insensitive; an uppercased or mixed-case host
		// must not slip past githubHostLike/githubHostPatterns unmatched.
		"[x](https://GitHub.com/someone/private-repo)",
		"[x](https://Raw.GithubUserContent.com/someone/web-app/main/README.md)",
	} {
		if got := checkMarkdown(text); len(got) == 0 {
			t.Errorf("checkMarkdown(%q) found no problem, want one", text)
		}
	}

	for _, text := range []string{
		"[web-app](https://github.com/gh-owner/web-app)",
		"[web-app](https://www.github.com/gh-owner/web-app)",
		"[gist](https://gist.github.com/gh-owner/web-app)",
		"[raw](https://raw.githubusercontent.com/gh-owner/web-app/main/README.md)",
		"[tarball](https://codeload.github.com/gh-owner/web-app/tar.gz/refs/heads/main)",
		"![badge](https://img.shields.io/github/license/gh-owner/web-app)",
		// The case-insensitive host match must not lowercase the whole
		// line: mixedCase-flake is deliberately mixed case and must still
		// compare exactly against fakeRepos.
		"[mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)",
	} {
		if got := checkMarkdown(text); len(got) != 0 {
			t.Errorf("checkMarkdown(%q) = %q, want no problem", text, got)
		}
	}
}

// TestCheckMarkdown_rejectsForeignProse guards the gap lists and the
// metadata table against a real repository name or description that never
// forms a GitHub-shaped link, which githubHostPatterns cannot see.
func TestCheckMarkdown_rejectsForeignProse(t *testing.T) {
	t.Parallel()

	for name, text := range map[string]string{
		"a gap-list name outside the vocabulary": "- **No README** (1) — marker-repo",
		"one of several gap-list names":          "- **No .gitignore** (2) — web-app, marker-repo",
		"a table Repository cell, unlinked":      "| marker-repo | tools |\n| --- | --- |\n| marker-repo | x |",
		"a table Repository cell's link text": "" +
			"| Repository | Description |\n" +
			"| --- | --- |\n" +
			"| [marker-repo](https://github.com/gh-owner/web-app) | the web-app repository |",
		"a table Description cell": "" +
			"| Repository | Description |\n" +
			"| --- | --- |\n" +
			"| [web-app](https://github.com/gh-owner/web-app) | a real description |",
		"a Git identities bullet naming a foreign repository": "" +
			"## Git identities\n\n" +
			"- **marker-repo** — `Pat Example <pat@example.com>` (canonical)",
		"a Git identities bullet carrying a real-shaped identity": "" +
			"## Git identities\n\n" +
			"- **web-app** — `Jane Doe <jane@corp.test>` — not canonical",
		"a real-shaped identity beside a fake one": "" +
			"## Git identities\n\n" +
			"- **web-app** — `Pat Example <pat@example.com>` (canonical), `Jane Doe <jane@corp.test>` — mixed",
		"a fake name with a foreign address": "" +
			"## Git identities\n\n" +
			"- **web-app** — `Pat Example <jane@corp.test>` — not canonical",
		"free text between the identities": "" +
			"## Git identities\n\n" +
			"- **web-app** — `Pat Example <pat@example.com>` and Jane Doe — mixed",
		"a line in the section that is not a bullet": "" +
			"## Git identities\n\n" +
			"Jane Doe committed here too.",
		"a real-shaped identity tagged accepted": "" +
			"## Git identities\n\n" +
			"- **web-app** — `Pat Example <pat@example.com>` (canonical), `Jane Doe <jane@corp.test>` (accepted)",
		"a real-shaped noreply identity tagged accepted": "" +
			"## Git identities\n\n" +
			"- **web-app** — `Pat Example <pat@example.com>` (canonical), `Jane Doe <1+jane@users.noreply.github.com>` (accepted)",
		"an identity tagged twice": "" +
			"## Git identities\n\n" +
			"- **web-app** — `Pat Example <pat@example.com>` (canonical) (accepted)",
		"a foreign backticked name in a Stale drift bullet": "" +
			"## Configuration drift\n\n" +
			"- **Stale `repos.toml` entry** (1) — `marker-repo`",
		"a foreign name beside a fake one in a Stale drift bullet": "" +
			"## Configuration drift\n\n" +
			"- **Stale `repos.toml` entry** (2) — `media-server`, `marker-repo`",
		"a foreign name in an unconfigured drift link": "" +
			"## Configuration drift\n\n" +
			"- **Needs a `repos.toml` entry** (1) — [marker-repo](https://github.com/gh-owner/marker-repo)",
		"a foreign link text over a fake target": "" +
			"## Configuration drift\n\n" +
			"- **Needs a `repos.toml` entry** (1) — [marker-repo](https://github.com/gh-owner/web-app)",
		"a bare foreign name in an unconfigured drift bullet": "" +
			"## Configuration drift\n\n" +
			"- **Needs a `repos.toml` entry** (1) — marker-repo",
		"a stale drift name that is not in backticks": "" +
			"## Configuration drift\n\n" +
			"- **Stale `repos.toml` entry** (1) — media-server",
		"free prose in the drift section": "" +
			"## Configuration drift\n\n" +
			"marker-repo has no entry.",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := checkMarkdown(text); len(got) == 0 {
				t.Errorf("checkMarkdown(%q) found no problem, want one", text)
			}
		})
	}

	for name, text := range map[string]string{
		"the Configuration drift section": "" +
			"## Configuration drift\n\n" +
			driftIntroLine + "\n\n" +
			"- **Needs a `repos.toml` entry** (2) — [hook-guard](https://github.com/gh-owner/hook-guard), [web-app](https://github.com/gh-owner/web-app)\n" +
			"- **Stale `repos.toml` entry** (1) — `media-server`\n\n" +
			"## Metadata",
		"a fake gap-list name": "- **No README** (1) — web-app, go-linter",
		"a clean metadata table row": "" +
			"| Repository | Description |\n" +
			"| --- | --- |\n" +
			"| [web-app](https://github.com/gh-owner/web-app) | the web-app repository |",
		"an empty description cell": "" +
			"| Repository | Description |\n" +
			"| --- | --- |\n" +
			"| [web-app](https://github.com/gh-owner/web-app) |  |",
		"the Git identities bullets": "" +
			"## Git identities\n\n" +
			"- **alpha** — `Pat Example <pat@example.com>` (canonical), `Patrick Example <patrick@example.org>` — mixed\n" +
			"- **web-app** — `Pat Example <pat@example.org>` — not canonical\n" +
			"- **bravo** — `Pat Example <pat@example.com>` (canonical)\n" +
			"- **go-linter** — `Pat Example <pat@example.com>` (canonical), `Pat Example <12345+pat@users.noreply.github.com>` (accepted)\n" +
			"\n" +
			"## Overrides\n\n" +
			"- **web-app** — `flake_nix` is `not_required`",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := checkMarkdown(text); len(got) != 0 {
				t.Errorf("checkMarkdown(%q) = %q, want no problem", text, got)
			}
		})
	}
}

// TestExample holds both example configurations to the fake vocabulary.
// repos.invalid.toml is invalid to the validator, not to the vocabulary: it
// still must name only fakeRepos repositories.
func TestExample(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"repos.toml", "repos.invalid.toml"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var file struct {
				Repos map[string]any `toml:"repos"`
			}
			path := filepath.Join("..", "..", "examples", name)
			if _, err := toml.DecodeFile(path, &file); err != nil {
				t.Fatalf("decode examples/%s: %v", name, err)
			}
			if len(file.Repos) == 0 {
				t.Fatalf("examples/%s declares no repositories", name)
			}
			for repo := range file.Repos {
				if !slices.Contains(fakeRepos, repo) {
					t.Errorf("examples/%s names a repository outside the fake vocabulary", name)
				}
			}
		})
	}
}

// TestCorpus is the gate: every fixture and golden in the tree.
func TestCorpus(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	sets := []struct {
		dir   string
		rules []rule
	}{
		{dir: filepath.Join(root, "internal", "github", "testdata", "api"), rules: apiRules},
		{dir: filepath.Join(root, "internal", "render", "testdata", "golden"), rules: snapshotRules},
	}
	seen := 0
	for _, set := range sets {
		err := filepath.WalkDir(set.dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var problems []string
			switch filepath.Ext(path) {
			case ".json":
				problems = check(decode(t, raw), set.rules)
			case ".md":
				problems = checkMarkdown(string(raw))
			default:
				return nil
			}
			seen++
			for _, p := range problems {
				t.Errorf("%s: %s", path, p)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", set.dir, err)
		}
	}
	reposDir := filepath.Join(root, "internal", "github", "testdata", "api", "repos")
	problems, err := checkFixtureDirNames(reposDir)
	if err != nil {
		t.Fatalf("walk %s: %v", reposDir, err)
	}
	for _, p := range problems {
		t.Errorf("%s: %s", reposDir, p)
	}
	if seen == 0 {
		t.Fatal("no fixture or golden was checked; the walk looks broken")
	}
}
