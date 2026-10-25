package render

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// Cell vocabulary. Everything the report says about one check is one of these
// three marks or a value.
const (
	markPass = "✓"
	markFail = "✗"
	markNA   = "n/a"
)

// deviationSuffix follows a path that differs from the one most of the
// account uses; the File paths intro explains it.
const deviationSuffix = " ≠"

// footerPrefix opens the footer line, which names the date the facts in the
// report were read. The date is the only part of the block that depends on the
// clock rather than on the account.
const footerPrefix = "_Read from GitHub on "

// Block renders everything that lives between the README's generated markers.
//
// doc-rot-exempt: now is the parameter's name, not a narrated change
// now is injected rather than read from the clock, because the renderer turns
// instants into "12d" and "4mo" — a renderer that called time.Now would make
// its own golden files rot daily.
func Block(rep *rules.Report, now func() time.Time) (string, error) {
	if rep == nil {
		return "", errors.New("render: nil report")
	}
	if now == nil {
		return "", errors.New("render: nil clock")
	}
	if rep.Owner == "" {
		return "", errors.New("render: report has no owner")
	}
	at := now().UTC()

	var b strings.Builder
	writeSection(&b, driftSection(rep))
	writeSection(&b, gapsSection(rep))
	writeSection(&b, metadataSection(rep))
	writeSection(&b, treeSection(rep))
	writeSection(&b, filePathsSection(rep))
	writeSection(&b, claudeSection(rep))
	writeSection(&b, nixSection(rep, at))
	writeSection(&b, ciEfficiencySection(rep))
	writeSection(&b, policySection(rep))
	writeSection(&b, activitySection(rep, at))
	writeSection(&b, ciTimingSection(rep))
	writeSection(&b, identitiesSection(rep))
	writeSection(&b, exceptionsSection(rep))
	writeSection(&b, overridesSection(rep))
	b.WriteString(footerPrefix + at.Format(time.DateOnly) + "._\n")
	return b.String(), nil
}

func writeSection(b *strings.Builder, s string) {
	if s == "" {
		return
	}
	b.WriteString(s)
	b.WriteString("\n")
}

// driftIntro is one line on purpose: the block is committed unformatted, and
// a wrapped paragraph is something prettier has an opinion about.
const driftIntro = "`repos.toml` and the account disagree. " +
	"Add a `[repos.<name>]` table for each repository that needs one, and remove each stale entry: " +
	"its repository was deleted, archived or turned into a fork. " +
	"Everything below covers only the repositories both name."

// driftSection says where repos.toml and the account disagree. It comes
// first because it qualifies everything after it: a repository with no entry
// is in none of the tables, so a reader who starts at Gaps would take the
// worklist for complete.
//
// A repository with no entry is linked, since it exists. A stale entry is
// not: its repository may be gone, and a link to nothing reads as a broken
// report rather than as the finding it is. The key is whatever repos.toml
// holds, so it is free text: oneLine keeps a line break in it from ending
// the bullet.
func driftSection(rep *rules.Report) string {
	drift := rep.Drift
	if len(drift.Unconfigured) == 0 && len(drift.StaleEntries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Configuration drift\n\n" + driftIntro + "\n\n")
	if n := len(drift.Unconfigured); n > 0 {
		links := make([]string, 0, n)
		for _, name := range drift.Unconfigured {
			links = append(links, repoLink(rep.Owner, name))
		}
		fmt.Fprintf(&b, "- **Needs a `repos.toml` entry** (%d) — %s\n", n, strings.Join(links, ", "))
	}
	if n := len(drift.StaleEntries); n > 0 {
		names := make([]string, 0, n)
		for _, name := range drift.StaleEntries {
			names = append(names, code(oneLine(name)))
		}
		fmt.Fprintf(&b, "- **Stale `repos.toml` entry** (%d) — %s\n", n, strings.Join(names, ", "))
	}
	return b.String()
}

// gapsSection is the worklist: one bullet per failing check, naming the
// repositories. A check with no failures is omitted entirely, so an account
// with nothing wrong has no Gaps section at all.
func gapsSection(rep *rules.Report) string {
	if len(rep.Gaps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Gaps\n\n")
	for _, g := range rep.Gaps {
		fmt.Fprintf(&b, "- **%s** (%d) — %s\n", g.Label, len(g.Repos), strings.Join(g.Repos, ", "))
	}
	return b.String()
}

func metadataSection(rep *rules.Report) string {
	t := newTable("Repository", "Type", "Visibility", "Published", "Description",
		"Homepage", "Topics", "License", "README")
	for i := range rep.Repos {
		r := &rep.Repos[i]
		t.add(
			repoLink(rep.Owner, r.Repo.Name),
			r.Repo.Type,
			r.Repo.Visibility,
			yesNo(r.Repo.Published),
			escape(r.Repo.Description),
			cell(r.Cell(rules.CheckHomepage)),
			cell(r.Cell(rules.CheckTopics)),
			cell(r.Cell(rules.CheckLicense)),
			cell(r.Cell(rules.CheckREADME)),
		)
	}
	return heading("Metadata", t)
}

func treeSection(rep *rules.Report) string {
	t := newTable("Repository", "Min. release age", "Major automerge", ".gitignore", ".editorconfig",
		"flake.nix", "CHANGELOG", "Community files")
	for i := range rep.Repos {
		r := &rep.Repos[i]
		t.add(
			repoLink(rep.Owner, r.Repo.Name),
			cell(r.Cell(rules.CheckRenovateMinReleaseAge)),
			cell(r.Cell(rules.CheckRenovateAutomergeMajor)),
			cell(r.Cell(rules.CheckGitignore)),
			cell(r.Cell(rules.CheckEditorconfig)),
			cell(r.Cell(rules.CheckFlakeNix)),
			cell(r.Cell(rules.CheckChangelog)),
			cell(r.Cell(rules.CheckCommunityFiles)),
		)
	}
	return heading("Tree", t)
}

// filePathsIntro is one line on purpose, like driftIntro: the block is
// committed unformatted.
const filePathsIntro = "Where each tooling and community file lives. " +
	"`≠` marks a path that differs from the one most repositories use."

// pathColumn is one column of a File paths sub-table: its header and the
// check whose path it shows.
type pathColumn struct {
	header string
	check  rules.Check
}

// pathGroup is one File paths sub-table.
type pathGroup struct {
	title   string
	columns []pathColumn
}

// filePathGroups splits the path checks into sub-tables by what the files are
// for. One table of twenty columns would be wider than any screen; the groups
// keep each table readable while the checks stay one flat list in rules.
//
// It is a function rather than a package variable so no mutable global backs
// the render.
func filePathGroups() []pathGroup {
	return []pathGroup{
		{"Build & release", []pathColumn{
			{"justfile", rules.CheckJustfile},
			{"Renovate", rules.CheckRenovate},
			{"commitlint", rules.CheckCommitlint},
			{"git-cliff", rules.CheckGitCliff},
			{"treefmt", rules.CheckTreefmt},
			{"Compose", rules.CheckCompose},
		}},
		{"Linters", []pathColumn{
			{"typos", rules.CheckTypos},
			{"yamllint", rules.CheckYamllint},
			{"markdownlint", rules.CheckMarkdownlint},
			{"Prettier", rules.CheckPrettier},
			{"editorconfig-checker", rules.CheckEditorconfigChecker},
			{"lychee", rules.CheckLychee},
			{"zizmor", rules.CheckZizmor},
			{"Codecov", rules.CheckCodecov},
			{"MkDocs", rules.CheckMkDocs},
		}},
		{"GitHub & docs", []pathColumn{
			{"LICENSE", rules.CheckLicenseFile},
			{"SECURITY", rules.CheckSecurityPolicy},
			{"CODEOWNERS", rules.CheckCodeowners},
			{"PR template", rules.CheckPRTemplate},
			{"CLAUDE.md", rules.CheckClaudeMD},
		}},
	}
}

// filePathsSection shows where each repository keeps its tooling and
// community files, one sub-table per group. The sub-tables are headed with
// ### beneath a single ## so the section reads as one; a group whose table
// would have no rows is dropped, as heading drops any empty table.
func filePathsSection(rep *rules.Report) string {
	var b strings.Builder
	for _, g := range filePathGroups() {
		headers := make([]string, 0, len(g.columns)+1)
		headers = append(headers, "Repository")
		for _, c := range g.columns {
			headers = append(headers, c.header)
		}
		t := newTable(headers...)
		for i := range rep.Repos {
			r := &rep.Repos[i]
			row := make([]string, 0, len(headers))
			row = append(row, repoLink(rep.Owner, r.Repo.Name))
			for _, c := range g.columns {
				row = append(row, cell(r.Cell(c.check)))
			}
			t.add(row...)
		}
		body := t.String()
		if body == "" {
			continue
		}
		b.WriteString("### " + g.title + "\n\n" + body + "\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return "## File paths\n\n" + filePathsIntro + "\n\n" + strings.TrimSuffix(b.String(), "\n")
}

// claudeIntro is one line on purpose, like driftIntro: the block is
// committed unformatted.
const claudeIntro = "Only repositories with Claude Code skills, commands, hooks or a plugin are listed, " +
	"plus the configured plugin marketplace."

// claudeGroups splits the Claude Code checks into sub-tables by what they
// read. Each group is a table of its own because most repositories carry
// skills without a plugin or hooks without skills, and one wide table would
// be mostly n/a.
//
// It is a function rather than a package variable so no mutable global backs
// the render.
func claudeGroups() []pathGroup {
	return []pathGroup{
		{"Skills", []pathColumn{
			{"Slash-only", rules.CheckSkillSlashOnly},
			{"Name = dir", rules.CheckSkillNameMatchesDir},
			{"Known keys", rules.CheckSkillKnownKeys},
			{"Description", rules.CheckSkillDescriptionLength},
			{"No wrappers", rules.CheckSkillNoWrapperCommands},
			{"Longest SKILL.md", rules.CheckSkillSize},
		}},
		{"Hooks", []pathColumn{
			{"Events", rules.CheckHookKnownEvents},
			{"Executable", rules.CheckHookCommandExecutable},
			{"Plugin root", rules.CheckHookPluginRoot},
			{"Timeout", rules.CheckHookTimeout},
			{"Matcher", rules.CheckHookMatcher},
		}},
		{"Plugin", []pathColumn{
			{"Manifest", rules.CheckPluginManifestFields},
			{"No own marketplace", rules.CheckPluginNoOwnMarketplace},
			{"Releases", rules.CheckPluginReleases},
			{"Version = tag", rules.CheckPluginVersionMatchesTag},
			{"Listed", rules.CheckPluginListed},
			{"Pin valid", rules.CheckPluginPinValid},
			{"Pin current", rules.CheckPluginPinCurrent},
			// The marketplace repository's row: its cells apply there
			// alone, so the row is listed even when that repository is no
			// plugin itself.
			{"Entries valid", rules.CheckMarketplaceEntriesValid},
			{"Complete", rules.CheckMarketplaceComplete},
		}},
	}
}

// claudeSection shows how each repository's Claude Code tooling measures up,
// one sub-table per group. Unlike File paths, a sub-table lists only the
// repositories where one of its checks has something to say: a repository
// without skills is n/a in every Skills column, and a row of nothing but n/a
// for each such repository would bury the few that carry any. A group with
// no rows is dropped, and with none left so is the section.
//
// A cell names the skills and command files at fault, and those names are
// directories and files the owner chose, so each cell is escaped like any
// free-text cell: a pipe in a name would end the cell.
func claudeSection(rep *rules.Report) string {
	var b strings.Builder
	for _, g := range claudeGroups() {
		headers := make([]string, 0, len(g.columns)+1)
		headers = append(headers, "Repository")
		for _, c := range g.columns {
			headers = append(headers, c.header)
		}
		t := newTable(headers...)
		for i := range rep.Repos {
			r := &rep.Repos[i]
			if !anyApplies(r, g.columns) {
				continue
			}
			row := make([]string, 0, len(headers))
			row = append(row, repoLink(rep.Owner, r.Repo.Name))
			for _, c := range g.columns {
				row = append(row, escape(cell(r.Cell(c.check))))
			}
			t.add(row...)
		}
		body := t.String()
		if body == "" {
			continue
		}
		b.WriteString("### " + g.title + "\n\n" + body + "\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return "## Claude\n\n" + claudeIntro + "\n\n" + strings.TrimSuffix(b.String(), "\n")
}

// anyApplies reports whether any of the columns' cells is more than a bare
// n/a: judged, informational, or an n/a still carrying its value.
func anyApplies(r *rules.RepoReport, columns []pathColumn) bool {
	for _, c := range columns {
		cl := r.Cell(c.check)
		if cl.Verdict != rules.NA || (cl.Scalar && cl.Value != "") {
			return true
		}
	}
	return false
}

// nixSection shows how each repository uses Nix beyond having a flake.nix,
// which the Tree section already reports.
func nixSection(rep *rules.Report, at time.Time) string {
	t := newTable("Repository", "Lock age", "Lock refresh", "Nix cache")
	for i := range rep.Repos {
		r := &rep.Repos[i]
		t.add(
			repoLink(rep.Owner, r.Repo.Name),
			markedDateCell(r.Cell(rules.CheckNixLockAge), at),
			cell(r.Cell(rules.CheckNixLockRefresh)),
			cell(r.Cell(rules.CheckNixCache)),
		)
	}
	return heading("Nix", t)
}

// ciEfficiencySection shows whether each repository's CI spends its time and
// storage well: caches saved with something in them, nothing left to pile
// up, no run kept going once a newer one supersedes it, and no job left to
// hang until GitHub's own six-hour limit, with the artifacts held beside
// the caches they share storage with. The workflows and jobs behind a
// count go in lists under the table, as the Git identities section does,
// since a cell holding every path would stretch the table past reading.
// After them come three lists no check judges, shown for every repository
// with an entry whatever its cells say: the caches under keys no check
// covers, the caches common actions keep for themselves, and the Go and Nix
// caches pull requests saved, all of which share the storage the checked
// caches need.
func ciEfficiencySection(rep *rules.Report) string {
	t := newTable("Repository", "Go cache", "Cache use", "Cancel superseded", "Timeouts", "Artifacts")
	for i := range rep.Repos {
		r := &rep.Repos[i]
		t.add(
			repoLink(rep.Owner, r.Repo.Name),
			cell(r.Cell(rules.CheckGoCache)),
			cell(r.Cell(rules.CheckCacheUsage)),
			cell(r.Cell(rules.CheckCICancelSuperseded)),
			cell(r.Cell(rules.CheckJobTimeouts)),
			cell(r.Cell(rules.CheckArtifactStorage)),
		)
	}
	s := heading("CI efficiency", t)
	if s == "" {
		return ""
	}
	return s + repoListSection("Workflows without cancel-in-progress", rep.CancelMissing, pathItem) +
		repoListSection("Jobs without timeouts", rep.TimeoutMissing, pathItem) +
		repoListSection("Unrecognised caches", rep.UnrecognisedCaches, cacheItem) +
		repoListSection("Action-owned caches", rep.ActionOwnedCaches, cacheItem) +
		repoListSection("Pull-request caches", rep.PRCaches, plainItem)
}

// repoListSection renders a list under a section's table: a subheading,
// then one bullet per repository naming its items, each formatted by item.
// An empty list renders nothing, subheading included.
func repoListSection[T any](title string, lists []rules.RepoList[T], item func(T) string) string {
	if len(lists) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n### " + title + "\n\n")
	for _, l := range lists {
		parts := make([]string, 0, len(l.Items))
		for _, it := range l.Items {
			parts = append(parts, item(it))
		}
		fmt.Fprintf(&b, "- **%s** — %s\n", l.Repo, strings.Join(parts, ", "))
	}
	return b.String()
}

// pathItem renders a file path, or a "<workflow file> / <job id>" pair, as
// inline code. An "unreadable <path>" entry keeps its word outside the code
// span, so it reads as what was found about the path rather than as part of
// it. A file name is the owner's choice and may hold a line break, which
// would end the bullet, so the value is put on one line as cacheItem's key
// is.
func pathItem(s string) string {
	if path, ok := strings.CutPrefix(s, "unreadable "); ok {
		return "unreadable " + code(oneLine(path))
	}
	return code(oneLine(s))
}

// cacheItem renders a cache entry as `key` (size, ref), the key as inline
// code. The key is whatever a workflow passed to a cache action, so it is
// free text: code widens its fence around any backtick, and a line break,
// which would end the bullet, becomes a space. Nothing else needs escaping
// inside a code span in a list, and escape's table rules would show there:
// in a code span a "\|" renders as both characters outside a table. The ref
// is a branch name or GitHub's own ref, shown as plain text so the
// parentheses read as one aside; a branch name is the owner's choice, so
// listText keeps it from being read as markup.
func cacheItem(c rules.CacheItem) string {
	return code(oneLine(c.Key)) + " (" + c.Size + ", " + listText(c.Ref) + ")"
}

// listText renders free text as plain text in a list item: on one line,
// with each character that would make it read as markup escaped: emphasis,
// a code span, a link, an HTML tag or a strike-through, and the backslash
// that escapes them.
func listText(s string) string {
	return strings.NewReplacer(
		`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "<", `\<`, ">", `\>`, "~", `\~`,
	).Replace(oneLine(s))
}

// oneLine replaces each line break in s with a space, a CRLF pair counting
// as one break. Table cells (escape) and list items share it, so the two
// cannot disagree on what a line break is.
func oneLine(s string) string {
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
}

// plainItem renders an item the rules package wrote whole, such as a count
// and a size, as it is.
func plainItem(s string) string { return s }

func policySection(rep *rules.Report) string {
	t := newTable("Repository", "Direct push", "Signed", "Identity", "CI workflows", "Required checks",
		"PR CI", "Push CI", "Up to date", "Tag ruleset", "Secret scanning", "Vuln. reporting")
	for i := range rep.Repos {
		r := &rep.Repos[i]
		t.add(
			repoLink(rep.Owner, r.Repo.Name),
			cell(r.Cell(rules.CheckDirectPush)),
			cell(r.Cell(rules.CheckSignedCommits)),
			cell(r.Cell(rules.CheckGitIdentity)),
			cell(r.Cell(rules.CheckCIWorkflows)),
			cell(r.Cell(rules.CheckRequiredChecks)),
			cell(r.Cell(rules.CheckCIOnPR)),
			cell(r.Cell(rules.CheckCIOnPush)),
			cell(r.Cell(rules.CheckBranchUpToDate)),
			cell(r.Cell(rules.CheckTagRuleset)),
			cell(r.Cell(rules.CheckSecretScanning)),
			cell(r.Cell(rules.CheckVulnReporting)),
		)
	}
	return heading("Policy", t)
}

func activitySection(rep *rules.Report, at time.Time) string {
	t := newTable("Repository", "Last push", "CI", "Open PRs", "Branches", "Releases", "Last release")
	for i := range rep.Repos {
		r := &rep.Repos[i]
		t.add(
			repoLink(rep.Owner, r.Repo.Name),
			dateCell(r.Cell(rules.CheckLastPush), at),
			cell(r.Cell(rules.CheckCIGreen)),
			cell(r.Cell(rules.CheckOpenPRs)),
			cell(r.Cell(rules.CheckBranches)),
			cell(r.Cell(rules.CheckReleases)),
			dateCell(r.Cell(rules.CheckLastReleaseAge), at),
		)
	}
	return heading("Activity", t)
}

// ciTimingSection renders rep.CITiming in the order the rules package gave it,
// and is omitted when no repository has a sampled commit. A column with no
// sample is n/a rather than 0s: a zero would read as a repository whose CI is
// instant. The slowest job's label comes from workflow and job names, which
// the repository's owner chose, so it is escaped like any free-text cell.
func ciTimingSection(rep *rules.Report) string {
	if len(rep.CITiming) == 0 {
		return ""
	}
	t := newTable("Repository", "PR CI", "Main CI", "Slowest job", "Slowest job mean")
	for _, l := range rep.CITiming {
		job, mean := markNA, markNA
		if l.SlowestJob != "" {
			job, mean = code(escape(l.SlowestJob)), ciDuration(l.SlowestMean)
		}
		t.add(
			repoLink(rep.Owner, l.Repo),
			ciWait(l.PR, l.HasPR),
			ciWait(l.Main, l.HasMain),
			job,
			mean,
		)
	}
	return heading("CI timing", t)
}

// ciWait is a mean wait, or n/a when there was nothing to average.
func ciWait(d time.Duration, ok bool) string {
	if !ok {
		return markNA
	}
	return ciDuration(d)
}

// ciDuration formats a whole-second duration compactly: 45s, 9m40s, 2m05s.
// From an hour up the seconds are noise next to a wait that long, so they are
// dropped: 1h05m.
func ciDuration(d time.Duration) string {
	secs := int64(d / time.Second)
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
	default:
		return fmt.Sprintf("%dh%02dm", secs/3600, secs%3600/60)
	}
}

// identitiesSection renders rep.Identities, one line per repository with any
// of the account holder's identities, and is omitted when there are none. Why
// the listing exists beside the Policy column, and how accepted identities
// are tagged, is on identityLines in internal/rules/identitylines.go. A name
// is commit metadata, which the owner does not control, so each identity is
// put on one line: a line break would end the bullet.
func identitiesSection(rep *rules.Report) string {
	if len(rep.Identities) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Git identities\n\n")
	for _, line := range rep.Identities {
		parts := make([]string, 0, len(line.Identities))
		for _, id := range line.Identities {
			p := code(oneLine(id.String()))
			switch {
			case id.Canonical:
				p += " (canonical)"
			case id.Accepted:
				p += " (accepted)"
			}
			parts = append(parts, p)
		}
		fmt.Fprintf(&b, "- **%s** — %s", line.Repo, strings.Join(parts, ", "))
		switch {
		case line.Mixed:
			b.WriteString(" — mixed")
		case !line.Clean:
			b.WriteString(" — not canonical")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// exceptionsSection groups the deviations by repository, so a repository that
// differs on three settings reads as one line rather than three. Both values
// are text read from the repository's settings, so each is put on one line: a
// line break would end the bullet.
func exceptionsSection(rep *rules.Report) string {
	if len(rep.Exceptions) == 0 && len(rep.NoConsensus) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Settings exceptions\n\n")

	if len(rep.Exceptions) == 0 {
		b.WriteString("No repository deviates from the account's usual settings.\n")
	}
	for i := 0; i < len(rep.Exceptions); {
		repo := rep.Exceptions[i].Repo
		var parts []string
		for ; i < len(rep.Exceptions) && rep.Exceptions[i].Repo == repo; i++ {
			e := rep.Exceptions[i]
			parts = append(parts, fmt.Sprintf("%s %s, usually %s", e.Setting, code(oneLine(e.Value)), code(oneLine(e.Modal))))
		}
		fmt.Fprintf(&b, "- **%s** — %s\n", repo, strings.Join(parts, "; "))
	}

	if len(rep.NoConsensus) > 0 {
		b.WriteString("\n### No consensus\n\n")
		b.WriteString("The most common value covers half or fewer of the repositories these apply to,\n")
		b.WriteString("so there is no norm to measure against.\n\n")
		for _, s := range rep.NoConsensus {
			fmt.Fprintf(&b, "- %s\n", s)
		}
	}
	return b.String()
}

// overridesSection renders rep.Overrides; see rules.collectOverrides for why
// they are listed. A value is whatever repos.toml says, so it is put on one
// line: a line break would end the bullet.
func overridesSection(rep *rules.Report) string {
	if len(rep.Overrides) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Overrides\n\n")
	for _, o := range rep.Overrides {
		fmt.Fprintf(&b, "- **%s** — %s is %s\n", o.Repo, code(o.Check.String()), code(oneLine(o.Value)))
	}
	return b.String()
}

func heading(title string, t *table) string {
	body := t.String()
	if body == "" {
		return ""
	}
	return "## " + title + "\n\n" + body
}

// repoLink links a repository name to its page on GitHub.
func repoLink(owner, name string) string {
	return "[" + name + "](https://github.com/" + owner + "/" + name + ")"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// cell renders one check's outcome. A value replaces the mark unless the cell
// sets Mark (see rules.Cell.Mark), in which case both are shown.
func cell(c rules.Cell) string {
	value := c.Value
	if c.Code {
		value = code(value)
	}
	// Only a rendered value takes the suffix: a bare tick or cross has no path
	// for it to qualify.
	if c.Deviates && value != "" {
		value += deviationSuffix
	}

	mark := ""
	switch c.Verdict {
	case rules.Pass:
		mark = markPass
	case rules.Fail:
		mark = markFail
	case rules.NA:
		// Scalar cells keep their value on n/a; see rules.Cell.Scalar for why.
		if c.Scalar && value != "" {
			return value
		}
		return markNA
	case rules.Info:
		if value == "" {
			return markNA
		}
		return value
	}

	switch {
	case c.Mark && value != "":
		return mark + " " + value
	case value != "":
		return value
	default:
		return mark
	}
}

// dateCell renders an instant as an age against the render time.
func dateCell(c rules.Cell, at time.Time) string {
	if c.Verdict == rules.NA && c.At.IsZero() {
		return markNA
	}
	if c.At.IsZero() {
		return "never"
	}
	return age(c.At, at)
}

// markedDateCell renders a judged instant as its age beside the verdict's
// mark, "✓ 4d" or "✗ 47d": the age alone would hide whether it is too old,
// and the mark alone would hide by how much. A cell that is not judged shows
// the bare age it found, as a scalar does on n/a; a cell with no instant is
// whatever cell makes of it, which is n/a or the reason it has none, such as
// an unreadable file.
func markedDateCell(c rules.Cell, at time.Time) string {
	if c.At.IsZero() {
		return cell(c)
	}
	switch c.Verdict {
	case rules.Pass:
		return markPass + " " + age(c.At, at)
	case rules.Fail:
		return markFail + " " + age(c.At, at)
	case rules.NA, rules.Info:
		// Not judged, so no mark: the bare age below. Returning here would
		// leave the final return after the switch with no path to it, which
		// Go still requires and the coverage gate would count as unrun.
	}
	return age(c.At, at)
}

// doc-rot-exempt: "today" is a word the renderer emits, not a date relative to the writer
// age is the relative date vocabulary: "today", "12d", "4mo", "3y". Deliberately
// coarse — there is no staleness threshold on any of these, so the number is
// shown to be read rather than compared against anything.
func age(then, now time.Time) string {
	days := int(now.Sub(then).Hours() / 24)
	switch {
	case days < 0:
		// A push timestamped in the future is a clock skew, not an age.
		return "today"
	case days < 1:
		return "today"
	case days < 60:
		return strconv.Itoa(days) + "d"
	case days < 730:
		return strconv.Itoa(days/30) + "mo"
	default:
		return strconv.Itoa(days/365) + "y"
	}
}
