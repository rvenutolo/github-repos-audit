package claude

import (
	"path"
	"slices"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/filepaths"
)

// Manifest and marketplace locations inside a plugin repository
// (https://code.claude.com/docs/en/plugins-reference, "Standard layout").
const (
	pluginManifestPath = ".claude-plugin/plugin.json"
	marketplacePath    = ".claude-plugin/marketplace.json"
	defaultHooksPath   = "hooks/hooks.json"
)

// Layout is the set of files worth reading, decided from a tree listing alone.
type Layout struct {
	PluginManifest string   // ".claude-plugin/plugin.json" when tracked, else ""
	OwnMarketplace bool     // .claude-plugin/marketplace.json is tracked
	Skills         []string // SKILL.md paths, sorted
	Commands       []string // command .md paths, sorted, each named by CommandName
	// SourceCommands is plugin.json's object-map commands that set source:
	// name -> the .md file it names. They are kept apart from Commands
	// because their name is the map key, not anything the path says.
	SourceCommands map[string]string
	HookFiles      []string // hooks/hooks.json, tracked **/.claude/settings.json and declared hooks paths, sorted
	// Missing is every path plugin.json declares that nothing in the tree
	// can answer, sorted: a skills or commands directory that is not one, a
	// command that is neither a directory nor a .md path (an object-map
	// source names one file, so for it a directory is not one either), or
	// any declared path outside the repository. Claude Code cannot load what such a path
	// names, and there is no file to record as unreadable in its place.
	Missing []string
}

// Discover applies Claude Code's loading rules to a tree. manifest is the
// parsed plugin.json, or nil when there is none; without one only the default
// project locations (under a .claude directory) count, because Claude Code
// loads a top-level skills/ or commands/ only from a plugin.
//
// A manifest's skills key adds to the default skills/ scan, its commands key
// replaces the default commands/ scan, and its hooks key merges with
// hooks/hooks.json. A plugin with SKILL.md at its root, no skills/ and no
// skills key loads as a single skill.
func Discover(entries []filepaths.Entry, manifest *Plugin) Layout {
	files := make(map[string]bool, len(entries))
	dirs := make(map[string]bool)
	for _, e := range entries {
		if e.Dir {
			dirs[e.Path] = true
		} else {
			files[e.Path] = true
			// A listing normally names directories too; deriving them keeps
			// the rules right for one that lists only blobs.
			for d := path.Dir(e.Path); d != "."; d = path.Dir(d) {
				dirs[d] = true
			}
		}
	}

	var l Layout
	if files[pluginManifestPath] {
		l.PluginManifest = pluginManifestPath
	}
	l.OwnMarketplace = files[marketplacePath]

	skills := map[string]bool{}
	commands := map[string]bool{}
	hooks := map[string]bool{}

	for p := range files {
		switch {
		case isUnderClaudeDir(p, "skills", 2, "SKILL.md"):
			skills[p] = true
		case isUnderClaudeDir(p, "commands", 0, ""):
			if strings.HasSuffix(p, ".md") {
				commands[p] = true
			}
		case p == ".claude/settings.json" || strings.HasSuffix(p, "/.claude/settings.json"):
			hooks[p] = true
		}
	}

	missing := map[string]bool{}
	sources := map[string]string{}
	if manifest != nil {
		discoverPlugin(files, dirs, manifest, skills, commands, hooks, missing, sources)
	}
	if len(sources) > 0 {
		l.SourceCommands = sources
	}

	l.Skills = sortedKeys(skills)
	l.Commands = sortedKeys(commands)
	l.HookFiles = sortedKeys(hooks)
	l.Missing = sortedKeys(missing)
	return l
}

// discoverPlugin adds what the manifest names. A declared file the tree does
// not hold is kept, so reading it records it as unreadable; a declared
// directory the tree does not hold, a command that is not a .md file, and
// any path outside the repository go to missing instead, because there is
// no file to read in their place. Dropping either would let a plugin Claude
// Code cannot load read as one with nothing to judge.
func discoverPlugin(files, dirs map[string]bool, m *Plugin, skills, commands, hooks, missing map[string]bool, sources map[string]string) {
	addSkills := func(root string) {
		for p := range files {
			rest, ok := cut(p, root)
			if ok && path.Base(rest) == "SKILL.md" && strings.Count(rest, "/") == 1 {
				skills[p] = true
			}
		}
	}
	addSkills("skills")
	for _, d := range m.SkillDirs {
		if d != "." && !dirs[d] {
			missing[d] = true
			continue
		}
		addSkills(d)
		// A declared directory may hold SKILL.md directly.
		if f := join(d, "SKILL.md"); files[f] {
			skills[f] = true
		}
	}

	// Root SKILL.md: a single-skill plugin has no skills/ and no skills key.
	if files["SKILL.md"] && !dirs["skills"] && !m.SkillsSet && len(m.SkillDirs) == 0 {
		skills["SKILL.md"] = true
	}

	if !m.CommandsSet {
		for p := range files {
			if rest, ok := cut(p, "commands"); ok && !strings.Contains(rest, "/") && strings.HasSuffix(rest, ".md") {
				commands[p] = true
			}
		}
	}
	for name, src := range m.SourceCommands {
		// The reference's source is "Path to the command's Markdown file",
		// so a directory has no one file to be the command.
		if !strings.HasSuffix(src, ".md") || !inRepo(src) {
			missing[src] = true
			continue
		}
		sources[name] = src
	}
	for _, c := range m.CommandPaths {
		switch {
		case dirs[c] || c == ".":
			for p := range files {
				if rest, ok := cut(p, c); ok && !strings.Contains(rest, "/") && strings.HasSuffix(rest, ".md") {
					commands[p] = true
				}
			}
		case strings.HasSuffix(c, ".md") && inRepo(c):
			commands[c] = true
		default:
			missing[c] = true
		}
	}

	if files[defaultHooksPath] {
		hooks[defaultHooksPath] = true
	}
	for _, h := range append([]string{m.HooksFile}, m.HooksFiles...) {
		switch {
		case h == "":
		case inRepo(h) && h != ".":
			hooks[h] = true
		default:
			missing[h] = true
		}
	}
}

// inRepo reports whether a cleaned declared path stays inside the repository.
// One that leaves it cannot be a tracked file, and reading it would ask
// GitHub for a path no repository has.
func inRepo(p string) bool {
	return p != ".." && !strings.HasPrefix(p, "../") && !strings.HasPrefix(p, "/")
}

// SkillName is the name a skill at skillPath is known by: its parent
// directory, or the plugin's own name for a SKILL.md at the repository root.
func SkillName(skillPath, pluginName string) string {
	dir := path.Dir(skillPath)
	if dir == "." {
		return pluginName
	}
	return path.Base(dir)
}

// CommandName is a command file's name. A project command's name is its path
// below .claude/commands/ without ".md", with "/" replaced by ":" (so
// frontend/component.md is frontend:component); a plugin's commands/ is flat,
// so its name is the file stem.
func CommandName(commandPath string) string {
	name := strings.TrimSuffix(commandPath, path.Ext(commandPath))
	const marker = ".claude/commands/"
	if rest, ok := strings.CutPrefix(name, marker); ok {
		return strings.ReplaceAll(rest, "/", ":")
	}
	if _, rest, ok := strings.Cut(name, "/"+marker); ok {
		return strings.ReplaceAll(rest, "/", ":")
	}
	return path.Base(name)
}

// isUnderClaudeDir reports whether p is <anything>/.claude/<kind>/ followed by
// exactly depth more segments (any number, at least one, when depth is 0), the
// last being leaf when leaf is not "". Skills count only in the direct layout,
// because Claude Code does not load deeper copies; commands nest, and the
// nesting becomes part of their name.
func isUnderClaudeDir(p, kind string, depth int, leaf string) bool {
	marker := ".claude/" + kind + "/"
	var rest string
	switch {
	case strings.HasPrefix(p, marker):
		rest = p[len(marker):]
	default:
		i := strings.Index(p, "/"+marker)
		if i < 0 {
			return false
		}
		rest = p[i+1+len(marker):]
	}
	segs := strings.Split(rest, "/")
	if (depth != 0 && len(segs) != depth) || slices.Contains(segs, "") {
		return false
	}
	return leaf == "" || segs[len(segs)-1] == leaf
}

// cut returns p relative to dir when p is below it. "." is the repository root.
func cut(p, dir string) (string, bool) {
	if dir == "." || dir == "" {
		return p, true
	}
	return strings.CutPrefix(p, dir+"/")
}

func join(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return dir + "/" + name
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
