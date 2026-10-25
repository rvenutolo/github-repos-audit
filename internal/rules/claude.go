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
		// nothing to say about the skill, which fails it.
		return countedCell(skillItems(cl.Skills, func(s audit.ClaudeSkill) bool {
			_, has := s.Keys["description"]
			return has && s.DescriptionChars <= SkillDescriptionMax
		}))
	case CheckSkillNoWrapperCommands:
		// A slash-only skill is its own / command, so a command file that
		// only invokes it is a second name for the same thing.
		return countedCell(skillItems(cl.Commands, func(s audit.ClaudeSkill) bool { return s.WrapsSkill == "" }))
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
