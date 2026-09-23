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
// golden legitimately carries alongside the 15 fake names (it audits itself
// in that fixture). "alpha" and "bravo" are synthetic literals from
// internal/render/block_test.go: they were never fetched from a real
// account, so internal/render/testdata/golden/clean.md — which predates the
// account scrub and was untouched by it — carries them safely.
var fakeRepos = []string{
	"blank-repo", "cipher-lib", "compiler-config", "config-files", "go-linter",
	"hook-guard", "java-demo", "media-server", "mixedCase-flake", "pkg-index",
	"private-notes", "recipe-site", "shell-scripts", "updater", "web-app",
	"github-repos-audit", "alpha", "bravo", "preset-store",
}

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

// pipeDescription is the one deliberate escaping fixture: a description
// containing a pipe character, used to prove the Markdown renderer escapes
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
	workflow  = `^(ci\.ya?ml|workflow-\d{2}\.ya?ml)$`
	ruleset   = `^(ruleset-\d{2}|protect-(main|tags))$`
	// checkContext covers the API fixtures' synthetic "check-NN" contexts
	// and the render snapshot golden's real CI job names (commitlint,
	// gate (ubuntu-latest), merge-gate): generic job names, not account data.
	checkContext = `^(check-\d{2}|commitlint|gate \(ubuntu-latest\)|merge-gate)$`
	// pattern covers the synthetic "example-org/action-NN@*" fixture and
	// "step-security/*", a real, publicly documented GitHub Actions
	// marketplace publisher used as an allowed-actions example; neither
	// names the audited account.
	pattern = `^(example-org/action-\d{2}@\*|step-security/\*)$`
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

// renovateProbes is the alternation of the seven Renovate config probes. A
// present one is an object whose oid, text and flags are ruled separately;
// an absent one is null.
const renovateProbes = `(renovate_json|renovate_json5|renovaterc|renovaterc_json|renovaterc_json5|renovate_github_json|renovate_github_json5)`

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

var apiRules = []rule{
	{regexp.MustCompile(`^(\[\]\.)?name$`), isFakeRepo, "a fake repository name"},
	{regexp.MustCompile(`^((\[\]\.)?full_name|\[\]\.ruleset_source)$`), isOwnerSlashRepo, "gh-owner/<fake>"},
	{regexp.MustCompile(`^login$`), func(v any) bool { return v == fakeOwner }, "gh-owner"},
	{regexp.MustCompile(`^(\[\]\.)?id$`), matches(`^1000\d{3}$`), "a synthetic repository id"},
	{regexp.MustCompile(`^(\[\]\.)?node_id$`), matches(`^R_ghowner\d{6}$`), "a synthetic node id"},
	{regexp.MustCompile(`^\[\]\.ruleset_id$`), matches(`^2000\d{3}$`), "a synthetic ruleset id"},
	{regexp.MustCompile(`integration_id$`), matches(`^3000\d{3}$`), "a synthetic integration id"},
	{regexp.MustCompile(`\.oid$`), matches(oid), "a pattern oid"},
	{regexp.MustCompile(`^(data\.repository\.)?description$`), isDescription, "empty or the placeholder"},
	{regexp.MustCompile(`^(homepage|data\.repository\.homepage_url)$`), isHomepage, "empty or https://example.com/<fake>"},
	{regexp.MustCompile(`^selected_actions_url$`), matches(`^https://api\.github\.com/repositories/1000\d{3}/actions/permissions/selected-actions$`), "a url with a synthetic id"},
	{regexp.MustCompile(`rulesets\.nodes\[\]\.name$`), matches(ruleset), "a synthetic ruleset name"},
	{regexp.MustCompile(`workflows\.entries\[\]\.name$`), matches(workflow), "a synthetic workflow file"},
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
	keep(
		`access_level`, `allowed_actions`, `\[\]\.archived`, `archived`, `\[\]\.disabled`, `disabled`,
		`\[\]\.fork`, `fork`, `\[\]\.private`, `private`, `\[\]\.visibility`, `visibility`,
		`\[\]\.default_branch`, `default_branch`, `has_issues`, `has_wiki`, `has_projects`,
		`has_discussions`, `has_pages`, `is_template`, `allow_forking`, `web_commit_signoff_required`,
		`paused`, `verified_allowed`, `can_approve_pull_request_reviews`,
		`default_workflow_permissions`, `enabled`, `github_owned_allowed`, `sha_pinning_required`,
		`open_issues_count`, `message`, `documentation_url`, `status`, `\[\]\.type`,
		`\[\]\.ruleset_source_type`,
		// A ruleset parameter's value is never account data (a branch-name
		// pattern, a boolean, a count): keep accepts it unconditionally,
		// which is the "Kept" row the spec intends here, not an oversight.
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
		`data\.repository\.(gitignore|editorconfig|flake_nix|changelog_md|justfile_dot|justfile_plain)`,
		`data\.repository\.(security_root|security_github|security_docs)`,
		`data\.repository\.(contributing_root|contributing_github|contributing_docs)`,
		`data\.repository\.(coc_root|coc_github|coc_docs)`,
		`data\.repository\.`+renovateProbes,
	),
}

var snapshotRules = []rule{
	{regexp.MustCompile(`^owner$`), func(v any) bool { return v == fakeOwner }, "gh-owner"},
	{regexp.MustCompile(`^repos\[\]\.name$`), isFakeRepo, "a fake repository name"},
	{regexp.MustCompile(`^repos\[\]\.description$`), isDescription, "empty, the placeholder or 'the <fake> repository'"},
	{regexp.MustCompile(`^repos\[\]\.homepage$`), isHomepage, "empty or https://example.com/<fake> or https://example.invalid/<slug>"},
	{regexp.MustCompile(`^repos\[\]\.head_oid$`), matches(oid), "a pattern oid"},
	{regexp.MustCompile(`^repos\[\]\.files\.workflows\[\]$`), matches(workflow), "a synthetic workflow file"},
	{regexp.MustCompile(`^repos\[\]\.rulesets\[\]\.name$`), matches(ruleset), "a synthetic ruleset name"},
	{regexp.MustCompile(`^repos\[\]\.branch\.required_checks\[\]$`), matches(checkContext), "a synthetic check context"},
	{regexp.MustCompile(`^repos\[\]\.settings\.allowed_actions\.patterns\[\]$`), matches(pattern), "a synthetic actions pattern"},
	{regexp.MustCompile(`^(generated_at|repos\[\]\.pushed_at|repos\[\]\.releases\.last_published_at)$`), optional(matches(timestamp)), "empty or a timestamp"},
	{regexp.MustCompile(`^repos\[\]\.renovate\.(min_release_age_source|min_release_age_error)$`), namesOnlyFakeRepos, "text naming only gh-owner/<fake> repositories"},
	keep(
		`types\.[a-z0-9_-]+\.[a-z_]+`,
		`repos\[\]\.(branches|ci_state|default_branch|empty|license|open_pull_requests|published|topics|type|visibility)`,
		`repos\[\]\.branch\.(known|types\[\])`,
		`repos\[\]\.files\.[a-z_]+`,
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
// path segments that name an owner and a repository, one pattern per host
// shape this project's golden Markdown links to. A pattern that matches
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
// "Repository" column to be in fakeRepos, and runs a "Description" column
// through isDescription, because those are prose and free text that no
// link-shaped pattern ever reaches.
//
// gapListName matches a gap-list bullet's trailing "— name[, name...]",
// e.g. "- **No README** (1) — go-linter, web-app".
var gapListName = regexp.MustCompile(`^- \*\*.+\*\* \(\d+\) — (.+)$`)

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

func checkMarkdown(text string) []string {
	var problems []string
	lines := strings.Split(text, "\n")
	descCol := -1 // index of the current table's Description column, if any; -1 outside a table or when it has none.
	inTable := false
	for i, line := range lines {
		n := i + 1
		switch {
		case githubIOLink.MatchString(line):
			problems = append(problems, fmt.Sprintf("line %d: a github.io link", n))
		case avatarLink.MatchString(line):
			problems = append(problems, fmt.Sprintf("line %d: an avatar link that identifies an account by id", n))
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

		if m := gapListName.FindStringSubmatch(line); m != nil {
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
		{name: "real description", doc: `{"description": "My personal media stack"}`},
		{name: "renovate text naming a real preset", doc: `{"data": {"repository": {"renovate_json": {"text": "{\"extends\": [\"github>someone/real-repo\"]}"}}}}`},
		// base64 of {"extends": ["github>someone/real-repo"]}
		{name: "preset content naming a real preset", doc: `{"content": "eyJleHRlbmRzIjogWyJnaXRodWI+c29tZW9uZS9yZWFsLXJlcG8iXX0=\n"}`},
		{name: "preset content that is not base64", doc: `{"content": "not base64!"}`},
		{name: "a present renovate probe with a real flag field", doc: `{"data": {"repository": {"renovate_json": {"is_huge": true}}}}`},
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
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := checkMarkdown(text); len(got) == 0 {
				t.Errorf("checkMarkdown(%q) found no problem, want one", text)
			}
		})
	}

	for name, text := range map[string]string{
		"a fake gap-list name": "- **No README** (1) — web-app, go-linter",
		"a clean metadata table row": "" +
			"| Repository | Description |\n" +
			"| --- | --- |\n" +
			"| [web-app](https://github.com/gh-owner/web-app) | the web-app repository |",
		"an empty description cell": "" +
			"| Repository | Description |\n" +
			"| --- | --- |\n" +
			"| [web-app](https://github.com/gh-owner/web-app) |  |",
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
