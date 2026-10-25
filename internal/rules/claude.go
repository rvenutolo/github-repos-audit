package rules

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// SkillDescriptionMax is the most characters a skill's `description` and
// `when_to_use` may hold between them. Past it Claude Code truncates the text
// in the skill listing it shows the model, so the part of a description that
// says when to use the skill may never be read. The limit is the one the
// SKILL.md frontmatter table states at https://code.claude.com/docs/en/skills.
const SkillDescriptionMax = 1536

// knownSkillKeys is every frontmatter key a skill or command file may carry:
// the union of the keys Claude Code's skills documentation lists in its
// frontmatter table, https://code.claude.com/docs/en/skills, and the fields
// of the Agent Skills open standard, https://agentskills.io/specification,
// plus `version`, which skills written to that standard commonly carry at the
// top level. A key outside it is a typo or a key some other tool reads, and
// either way Claude Code ignores it, so a setting the author meant to make
// silently does nothing.
//
// It is a function rather than a package variable so no mutable global backs
// the rules.
func knownSkillKeys() []string {
	return []string{
		// Claude Code's frontmatter table.
		"name", "description", "when_to_use", "argument-hint", "arguments",
		"disable-model-invocation", "user-invocable", "allowed-tools",
		"disallowed-tools", "model", "effort", "context", "agent", "background",
		"hooks", "paths", "shell", "metadata", "license", "compatibility",
		// The Agent Skills standard's fields beyond those, and version.
		"version",
	}
}

// skillKeyType is the YAML tag a key must carry, or "" for a key whose type
// no check depends on. Only these three are held to a type: the two booleans
// decide skill_slash_only, and a quoted "true" is a string Claude Code may or
// may not coerce depending on its version, so it is caught here rather than
// counted as set; `agent` names a subagent, and a boolean there is a
// confusion with `context: fork`.
func skillKeyType(key string) string {
	switch key {
	case "disable-model-invocation", "user-invocable":
		return "!!bool"
	case "agent":
		return "!!str"
	default:
		return ""
	}
}

// item is one thing a counted check judges: a skill, a command file, a hook.
// name is what the cell calls it when it fails. unreadable is the item's path
// when it could not be read, and empty otherwise.
type item struct {
	name       string
	ok         bool
	unreadable string
}

// countedCell turns the items a check judges into its observation. Every
// item passing reads `passing/total`, as community_files reads 3/3. Any
// failing reads `passing/total name, name` with a cross, naming the failing
// items in the order given, which callers keep to path order: the count says
// how much is wrong and the names say where, so the reader need not open
// audit.json to find them. An unreadable item fails the whole check as
// `unreadable <path>`, naming the first: a file whose facts are unknown can
// pass nothing, and a count that left it out would read as complete. No
// items at all is n/a, since there is nothing to judge.
func countedCell(items []item) observation {
	if len(items) == 0 {
		return observation{unknown: true}
	}
	passing := 0
	var failed []string
	for _, it := range items {
		if it.unreadable != "" {
			return observation{value: "unreadable " + it.unreadable, mark: true}
		}
		if it.ok {
			passing++
			continue
		}
		failed = append(failed, it.name)
	}
	count := fmt.Sprintf("%d/%d", passing, len(items))
	if len(failed) == 0 {
		return observation{ok: true, value: count}
	}
	return observation{value: count + " " + strings.Join(failed, ", "), mark: true}
}

// observeClaude judges the checks that read a repository's Claude Code
// tooling. When the Claude files could not be read as a whole, every one of
// them fails as unreadable: judging the part that was read would pass a
// repository on files nobody looked at.
//
// The skill checks apply once the repository has a skill or a command file;
// each judges only the kind of file its rule is about, and is n/a when the
// repository has none of that kind. Command files are held to skill_slash_only
// and skill_known_keys because Claude Code can invoke them on its own as it
// does skills; a command's name is not a directory and its description is
// not truncated in the skill listing, so the other skill checks leave it out.
//
// The hook checks apply once the repository has a hook, and each is n/a when
// no hook is one it judges. A hook file that could not be read fails all five,
// except that hook_plugin_root skips one outside a plugin.
func observeClaude(c Check, r audit.Repo, _ evalContext) observation {
	cl := r.Claude
	if cl.Unreadable != "" {
		return observation{value: "unreadable " + cl.Unreadable, mark: true, scalar: c.ValueOnly()}
	}
	all := byPath(cl.Skills, cl.Commands)

	//nolint:exhaustive // observe hands only the Claude checks here
	switch c {
	case CheckSkillSlashOnly:
		// user-invocable: false hides the skill from the / menu, so with
		// model invocation also off it cannot be run at all.
		return countedCell(skillItems(all, func(s audit.ClaudeSkill) bool {
			return s.DisableModelInvocation && !s.UserInvocableFalse
		}))
	case CheckSkillNameMatchesDir:
		// A missing name fails even though Claude Code falls back to the
		// directory: stating it keeps a rename from touching only one of the
		// two.
		return countedCell(skillItems(cl.Skills, func(s audit.ClaudeSkill) bool {
			return s.Name != "" && s.Name == s.Dir
		}))
	case CheckSkillKnownKeys:
		return countedCell(skillItems(all, func(s audit.ClaudeSkill) bool { return knownKeys(s.Keys) }))
	case CheckSkillDescriptionLength:
		// DescriptionChars counts when_to_use too, so a skill with only that
		// key could pass on length; without a description the listing has
		// nothing to say about the skill, which fails it. The key alone is
		// not enough: one with no value, an empty string or a list says
		// nothing either.
		return countedCell(skillItems(cl.Skills, func(s audit.ClaudeSkill) bool {
			return s.HasDescription && s.DescriptionChars <= SkillDescriptionMax
		}))
	case CheckSkillNoWrapperCommands:
		// A slash-only skill is its own / command, so a command file that
		// only invokes it is a second name for the same thing.
		return countedCell(skillItems(cl.Commands, func(s audit.ClaudeSkill) bool { return s.WrapsSkill == "" }))
	case CheckHookKnownEvents:
		return countedCell(hookItems(cl.Hooks, func(h audit.ClaudeHook) (bool, bool) {
			return slices.Contains(knownHookEvents(), h.Event), true
		}))
	case CheckHookCommandExecutable:
		// An interpreter reads its script, so only a tracked file is needed;
		// anything else is run, so it must also carry the execute bit.
		return countedCell(hookItems(cl.Hooks, func(h audit.ClaudeHook) (bool, bool) {
			if !runsScript(h) {
				return false, false
			}
			return h.ScriptMode != "" && (h.Interpreter || h.ScriptMode == "100755"), true
		}))
	case CheckHookPluginRoot:
		// Only plugin hooks are judged, so an unreadable source outside a
		// plugin (a project settings file) holds nothing this check would
		// judge even once read, and is left out rather than failing it.
		plugin := slices.DeleteFunc(slices.Clone(cl.Hooks), func(h audit.ClaudeHook) bool {
			return h.Unreadable != "" && !h.Plugin
		})
		return countedCell(hookItems(plugin, func(h audit.ClaudeHook) (bool, bool) {
			if !h.Plugin || !runsScript(h) {
				return false, false
			}
			return h.PathForm == "plugin_root", true
		}))
	case CheckHookTimeout:
		return countedCell(hookItems(cl.Hooks, func(h audit.ClaudeHook) (bool, bool) {
			return h.TimeoutSet, true
		}))
	case CheckHookMatcher:
		return countedCell(hookItems(cl.Hooks, func(h audit.ClaudeHook) (bool, bool) {
			if !slices.Contains(toolMatcherEvents(), h.Event) {
				return false, false
			}
			return h.Matcher != "", true
		}))
	case CheckPluginManifestFields, CheckPluginNoOwnMarketplace,
		CheckPluginReleases, CheckPluginVersionMatchesTag:
		return observePlugin(c, r)
	}
	// CheckSkillSize, the one Claude check left. It closes the function
	// rather than taking a case of its own because observe hands nothing
	// else here, and a default for the impossible would be a block no test
	// can run.
	return observeSkillSize(cl.Skills)
}

// byPath is the skills and command files together, in path order, so the
// names a counted cell lists read in the order the files sit in the tree.
func byPath(lists ...[]audit.ClaudeSkill) []audit.ClaudeSkill {
	var out []audit.ClaudeSkill
	for _, l := range lists {
		out = append(out, l...)
	}
	slices.SortStableFunc(out, func(a, b audit.ClaudeSkill) int { return cmp.Compare(a.Path, b.Path) })
	return out
}

// skillItems judges each skill or command file with ok. A file is named by
// the name Claude Code knows it by, or by its path when it has none (the root
// SKILL.md of a plugin whose manifest names nothing), so a failing file is
// never listed as a blank.
func skillItems(skills []audit.ClaudeSkill, ok func(audit.ClaudeSkill) bool) []item {
	out := make([]item, 0, len(skills))
	for _, s := range skills {
		name := cmp.Or(s.Dir, s.Path)
		if s.Unreadable != "" {
			out = append(out, item{name: name, unreadable: s.Path})
			continue
		}
		out = append(out, item{name: name, ok: ok(s)})
	}
	return out
}

// knownKeys reports whether every key is a known one carrying the type it
// must.
func knownKeys(keys map[string]string) bool {
	known := knownSkillKeys()
	for k, tag := range keys {
		if !slices.Contains(known, k) {
			return false
		}
		if want := skillKeyType(k); want != "" && tag != want {
			return false
		}
	}
	return true
}

// observeSkillSize is the longest SKILL.md's line count, a plain fact with no
// mark: Claude Code loads the whole file when the skill runs, and the 500-line
// guidance the README gives is advice, not a limit anything enforces. An
// unreadable skill has no known length, so the longest is unknown too.
func observeSkillSize(skills []audit.ClaudeSkill) observation {
	if len(skills) == 0 {
		return observation{unknown: true}
	}
	longest := 0
	for _, s := range byPath(skills) {
		if s.Unreadable != "" {
			return observation{value: "unreadable " + s.Path, mark: true, scalar: true}
		}
		longest = max(longest, s.Lines)
	}
	return observation{ok: true, value: strconv.Itoa(longest), scalar: true}
}

// knownHookEvents is every hook event name Claude Code defines, from the
// lifecycle table at https://code.claude.com/docs/en/hooks. An event outside
// it is a typo, and Claude Code never fires it, so the hook silently never
// runs.
//
// It is a function rather than a package variable so no mutable global backs
// the rules.
func knownHookEvents() []string {
	return []string{
		"SessionStart", "Setup", "UserPromptSubmit", "UserPromptExpansion",
		"PreToolUse", "PermissionRequest", "PermissionDenied", "PostToolUse",
		"PostToolUseFailure", "PostToolBatch", "Notification", "MessageDisplay",
		"SubagentStart", "SubagentStop", "TaskCreated", "TaskCompleted", "Stop",
		"StopFailure", "TeammateIdle", "InstructionsLoaded", "ConfigChange",
		"CwdChanged", "DirectoryAdded", "FileChanged", "WorktreeCreate",
		"WorktreeRemove", "PreCompact", "PostCompact", "PreModelSwitch",
		"PostModelSwitch", "Elicitation", "ElicitationResult", "SessionEnd",
	}
}

// toolMatcherEvents are the events whose matcher filters on a tool name, per
// the matcher table at https://code.claude.com/docs/en/hooks. An empty
// matcher there fires the hook on every tool, which is rarely meant. The
// other events match on something else (how a session started, an agent
// type) or take no matcher, so an empty one is ordinary.
func toolMatcherEvents() []string {
	return []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionRequest", "PermissionDenied"}
}

// runsScript reports a command handler that names a repository file. Other
// handler types (http, prompt, agent, mcp_tool) run no script, so a path
// recorded on one is not judged. An absent type is judged: a command is the
// handler Claude Code expects when a hook names a file, and skipping it
// would pass a hook nobody looked at.
func runsScript(h audit.ClaudeHook) bool {
	return h.Script != "" && (h.Type == "" || h.Type == "command")
}

// hookItems judges each hook with judge, which returns whether the hook
// passes and whether the check applies to it at all. A hook the check does
// not apply to is left out of the count, but a hook file that could not be
// read is always counted: its hooks are unknown, so none of them can pass. A
// caller whose check could never judge a source drops it before calling.
// A hook is named by its file and event.
func hookItems(hooks []audit.ClaudeHook, judge func(audit.ClaudeHook) (ok, applies bool)) []item {
	var out []item
	for _, h := range hooks {
		name := h.Source + " " + h.Event
		if h.Unreadable != "" {
			out = append(out, item{name: name, unreadable: h.Source})
			continue
		}
		if ok, applies := judge(h); applies {
			out = append(out, item{name: name, ok: ok})
		}
	}
	return out
}

// requiredPluginFields is every field plugin_manifest_fields requires of
// plugin.json, sorted, which is the order internal/claude records the ones
// present and the order the cell lists the ones missing.
func requiredPluginFields() []string {
	return []string{"$schema", "author", "description", "homepage", "license", "name", "repository", "version"}
}

// observePlugin judges the four plugin checks. They apply once the repository
// tracks .claude-plugin/plugin.json and are n/a before that. A manifest that
// could not be read fails all four: its fields, version and sibling files
// are unknown, and a release check that passed beside them would read as a
// verdict on a plugin nobody looked at.
func observePlugin(c Check, r audit.Repo) observation {
	p := r.Claude.Plugin
	if !p.Present && p.Unreadable == "" {
		return observation{unknown: true}
	}
	if p.Unreadable != "" {
		return observation{value: "unreadable .claude-plugin/plugin.json", mark: true}
	}
	//nolint:exhaustive // observeClaude hands only the plugin checks here
	switch c {
	case CheckPluginManifestFields:
		missing := slices.DeleteFunc(requiredPluginFields(), func(f string) bool { return slices.Contains(p.Fields, f) })
		if len(missing) == 0 {
			return observation{ok: true}
		}
		return observation{value: strings.Join(missing, ", "), mark: true}
	case CheckPluginNoOwnMarketplace:
		return observation{ok: !p.OwnMarketplace}
	case CheckPluginReleases:
		// GitHub's Latest release already leaves out drafts and
		// prereleases, so an empty tag means nothing has been published.
		return observation{ok: r.Releases.LatestTag != ""}
	}
	// CheckPluginVersionMatchesTag, the one plugin check left, for the same
	// reason CheckSkillSize closes observeClaude.
	tag := r.Releases.LatestTag
	if tag == "" {
		// plugin_releases already fails; a second failure would say the
		// same thing about a version with nothing to be compared to.
		return observation{unknown: true}
	}
	version := cmp.Or(p.Version, "none")
	ok, valid := semverAtLeast(p.Version, tag)
	switch {
	case !valid:
		return observation{value: version + " ? " + tag, mark: true}
	case version == strings.TrimPrefix(tag, "v"):
		return observation{ok: true, value: version}
	case ok:
		// Ahead passes, so a nightly run between a release pull request's
		// merge and its publish does not fail.
		return observation{ok: true, value: version + " ≥ " + tag}
	}
	return observation{value: version + " < " + tag, mark: true}
}

// semverAtLeast reports whether version is the same as or later than tag with
// its leading v removed, comparing them as Semantic Versioning 2.0.0 orders
// them (https://semver.org): numeric core first, then a version with no
// prerelease ahead of one with, then prerelease identifiers one by one.
// Build metadata does not take part. valid is false when either side is not
// a semantic version, and ok is then false too. It is local because the
// module would be the project's only use of golang.org/x/mod.
func semverAtLeast(version, tag string) (ok, valid bool) {
	a, aok := parseSemver(version)
	b, bok := parseSemver(strings.TrimPrefix(tag, "v"))
	if !aok || !bok {
		return false, false
	}
	return a.compare(b) >= 0, true
}

type semver struct {
	core [3]int
	pre  []string
}

// parseSemver parses MAJOR.MINOR.PATCH with optional -prerelease and
// +build, rejecting what semver.org's grammar does: leading zeros in the
// numeric parts and numeric prerelease identifiers, empty identifiers, and
// characters outside [0-9A-Za-z-].
func parseSemver(s string) (semver, bool) {
	var v semver
	s, _, _ = strings.Cut(s, "+")
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, ok := numericIdent(p)
		if !ok {
			return v, false
		}
		v.core[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if id == "" || strings.Trim(id, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-") != "" {
				return v, false
			}
			if _, numeric := strconv.Atoi(id); numeric == nil && len(id) > 1 && id[0] == '0' {
				return v, false
			}
		}
	}
	return v, true
}

// numericIdent parses a version number without a sign or a leading zero.
func numericIdent(s string) (int, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func (a semver) compare(b semver) int {
	for i := range a.core {
		if c := cmp.Compare(a.core[i], b.core[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := range min(len(a.pre), len(b.pre)) {
		if c := comparePre(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a.pre), len(b.pre))
}

// comparePre orders two prerelease identifiers: numeric ones numerically and
// below any alphanumeric one, alphanumeric ones in ASCII order.
func comparePre(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		return cmp.Compare(an, bn)
	case aerr == nil:
		return -1
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}
