package claude_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/claude"
	"github.com/rvenutolo/github-repos-audit/internal/filepaths"
)

func tree(paths ...string) []filepaths.Entry {
	out := make([]filepaths.Entry, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepaths.Entry{Path: p, Mode: "100644"})
	}
	return out
}

func TestDiscover(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		paths    []string
		manifest *claude.Plugin
		want     claude.Layout
	}{
		{
			name:  "project skills at any depth of repository",
			paths: []string{".claude/skills/a/SKILL.md", "sub/.claude/skills/b/SKILL.md"},
			want:  claude.Layout{Skills: []string{".claude/skills/a/SKILL.md", "sub/.claude/skills/b/SKILL.md"}},
		},
		{
			name:  "a deeper copy is not loaded",
			paths: []string{".claude/skills/a/deep/SKILL.md", ".claude/skills/SKILL.md", ".claude/skills/a/other.md"},
			want:  claude.Layout{},
		},
		{
			name:  "top-level skills without a manifest are ignored",
			paths: []string{"skills/c/SKILL.md"},
			want:  claude.Layout{},
		},
		{
			name:     "top-level skills with a manifest",
			paths:    []string{"skills/c/SKILL.md", "skills/c/deep/SKILL.md"},
			manifest: &claude.Plugin{},
			want:     claude.Layout{Skills: []string{"skills/c/SKILL.md"}},
		},
		{
			name:     "declared skills directory adds to the default",
			paths:    []string{"extra/d/SKILL.md", "skills/c/SKILL.md"},
			manifest: &claude.Plugin{SkillDirs: []string{"extra"}, SkillsSet: true},
			want:     claude.Layout{Skills: []string{"extra/d/SKILL.md", "skills/c/SKILL.md"}},
		},
		{
			name:     "declared directory holding SKILL.md directly",
			paths:    []string{"extra/SKILL.md"},
			manifest: &claude.Plugin{SkillDirs: []string{"extra"}, SkillsSet: true},
			want:     claude.Layout{Skills: []string{"extra/SKILL.md"}},
		},
		{
			name:     "declared plugin root as skills directory",
			paths:    []string{"d/SKILL.md", "SKILL.md", "d/deep/SKILL.md"},
			manifest: &claude.Plugin{SkillDirs: []string{"."}, SkillsSet: true},
			want:     claude.Layout{Skills: []string{"SKILL.md", "d/SKILL.md"}},
		},
		{
			name:     "root SKILL.md of a plugin with no skills",
			paths:    []string{"SKILL.md"},
			manifest: &claude.Plugin{Name: "skill-pack"},
			want:     claude.Layout{Skills: []string{"SKILL.md"}},
		},
		{
			name:     "root SKILL.md is ignored when skills/ exists",
			paths:    []string{"SKILL.md", "skills/a/SKILL.md"},
			manifest: &claude.Plugin{},
			want:     claude.Layout{Skills: []string{"skills/a/SKILL.md"}},
		},
		{
			name:     "root SKILL.md is ignored when a skills key is set",
			paths:    []string{"SKILL.md"},
			manifest: &claude.Plugin{SkillsSet: true},
			want:     claude.Layout{},
		},
		{
			name:  "root SKILL.md without a manifest",
			paths: []string{"SKILL.md"},
			want:  claude.Layout{},
		},
		{
			name:     "default commands",
			paths:    []string{"commands/x.md", "commands/sub/y.md", "commands/z.txt"},
			manifest: &claude.Plugin{},
			want:     claude.Layout{Commands: []string{"commands/x.md"}},
		},
		{
			name:  "top-level commands without a manifest are ignored",
			paths: []string{"commands/x.md"},
			want:  claude.Layout{},
		},
		{
			name:     "declared commands replace the default",
			paths:    []string{"commands/x.md", "cmds/a.md", "cmds/b.md", "cmds/sub/c.md", "cmds/d.txt"},
			manifest: &claude.Plugin{CommandsSet: true, CommandPaths: []string{"cmds"}},
			want:     claude.Layout{Commands: []string{"cmds/a.md", "cmds/b.md"}},
		},
		{
			// A declared command file that is not tracked is still listed:
			// reading it finds nothing, so it is recorded as unreadable
			// rather than dropped from a plugin Claude Code cannot load.
			name:     "declared command file, tracked or not",
			paths:    []string{"cmds/a.md", "cmds/b.md"},
			manifest: &claude.Plugin{CommandsSet: true, CommandPaths: []string{"cmds/a.md", "missing.md"}},
			want:     claude.Layout{Commands: []string{"cmds/a.md", "missing.md"}},
		},
		{
			name:     "the plugin root declared as the commands directory",
			paths:    []string{"a.md", "commands/x.md", "sub/b.md"},
			manifest: &claude.Plugin{CommandsSet: true, CommandPaths: []string{"."}},
			want:     claude.Layout{Commands: []string{"a.md"}},
		},
		{
			name:     "a declared commands directory that does not exist is missing",
			paths:    []string{"commands/x.md"},
			manifest: &claude.Plugin{CommandsSet: true, CommandPaths: []string{"cmds"}},
			want:     claude.Layout{Missing: []string{"cmds"}},
		},
		{
			name:     "a declared command that is a tracked file but not markdown is missing",
			paths:    []string{"cmds/a.txt"},
			manifest: &claude.Plugin{CommandsSet: true, CommandPaths: []string{"cmds/a.txt"}},
			want:     claude.Layout{Missing: []string{"cmds/a.txt"}},
		},
		{
			name:     "a declared command file outside the repository is missing",
			manifest: &claude.Plugin{CommandsSet: true, CommandPaths: []string{"../a.md"}},
			want:     claude.Layout{Missing: []string{"../a.md"}},
		},
		{
			// Named by the key, so the file is not also in Commands under
			// its stem; two keys may name one file.
			name:     "object-map source commands",
			paths:    []string{"commands/x.md", "cmds/a.md"},
			manifest: &claude.Plugin{CommandsSet: true, SourceCommands: map[string]string{"one": "cmds/a.md", "two": "cmds/a.md"}},
			want:     claude.Layout{SourceCommands: map[string]string{"one": "cmds/a.md", "two": "cmds/a.md"}},
		},
		{
			name:     "an object-map source that is a directory, not markdown or outside the repository is missing",
			paths:    []string{"cmds/a.md", "cmds/b.txt"},
			manifest: &claude.Plugin{CommandsSet: true, SourceCommands: map[string]string{"d": "cmds", "t": "cmds/b.txt", "o": "../a.md"}},
			want:     claude.Layout{Missing: []string{"../a.md", "cmds", "cmds/b.txt"}},
		},
		{
			name:     "a declared skills directory that does not exist is missing",
			paths:    []string{"skills/c/SKILL.md"},
			manifest: &claude.Plugin{SkillDirs: []string{"extra", "."}, SkillsSet: true},
			want:     claude.Layout{Skills: []string{"skills/c/SKILL.md"}, Missing: []string{"extra"}},
		},
		{
			name:     "a declared skills path that is a file is missing",
			paths:    []string{"extra"},
			manifest: &claude.Plugin{SkillDirs: []string{"extra"}, SkillsSet: true},
			want:     claude.Layout{Missing: []string{"extra"}},
		},
		{
			name:     "inline-only commands find no files",
			paths:    []string{"commands/x.md"},
			manifest: &claude.Plugin{CommandsSet: true, InlineCommands: []string{"about"}},
			want:     claude.Layout{},
		},
		{
			name:  "project commands are always included",
			paths: []string{".claude/commands/y.md", "sub/.claude/commands/w.md", ".claude/commands/deep/v.md", ".claude/commands/n.txt"},
			want:  claude.Layout{Commands: []string{".claude/commands/deep/v.md", ".claude/commands/y.md", "sub/.claude/commands/w.md"}},
		},
		{
			name:     "project commands survive a commands key",
			paths:    []string{".claude/commands/y.md"},
			manifest: &claude.Plugin{CommandsSet: true},
			want:     claude.Layout{Commands: []string{".claude/commands/y.md"}},
		},
		{
			name:     "plugin hooks file",
			paths:    []string{"hooks/hooks.json"},
			manifest: &claude.Plugin{},
			want:     claude.Layout{HookFiles: []string{"hooks/hooks.json"}},
		},
		{
			name:  "hooks.json without a manifest is ignored",
			paths: []string{"hooks/hooks.json"},
			want:  claude.Layout{},
		},
		{
			// As with a command file, a declared hooks file that is not
			// tracked is listed so reading it records it as unreadable.
			name:     "declared hooks file, tracked or not",
			paths:    []string{"hooks/hooks.json", "config/h.json"},
			manifest: &claude.Plugin{HooksFile: "config/h.json", HooksFiles: []string{"config/h.json", "config/gone.json", "hooks/hooks.json"}},
			want:     claude.Layout{HookFiles: []string{"config/gone.json", "config/h.json", "hooks/hooks.json"}},
		},
		{
			name:     "a declared hooks file outside the repository is missing",
			manifest: &claude.Plugin{HooksFile: "/etc/h.json", HooksFiles: []string{"/etc/h.json", ".."}},
			want:     claude.Layout{Missing: []string{"..", "/etc/h.json"}},
		},
		{
			name:     "declared hooks file through HooksFile alone",
			paths:    []string{"x.json"},
			manifest: &claude.Plugin{HooksFile: "x.json"},
			want:     claude.Layout{HookFiles: []string{"x.json"}},
		},
		{
			name:  "settings files",
			paths: []string{".claude/settings.json", "sub/.claude/settings.json", ".claude/settings.local.json", "x.claude/settings.json"},
			want:  claude.Layout{HookFiles: []string{".claude/settings.json", "sub/.claude/settings.json"}},
		},
		{
			name:  "marketplace and manifest",
			paths: []string{".claude-plugin/marketplace.json", ".claude-plugin/plugin.json"},
			want:  claude.Layout{OwnMarketplace: true, PluginManifest: ".claude-plugin/plugin.json"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := claude.Discover(tree(tc.paths...), tc.manifest)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Layout (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDiscover_directoriesAreNotFiles(t *testing.T) {
	t.Parallel()
	entries := []filepaths.Entry{
		{Path: ".claude/skills/a/SKILL.md", Dir: true},
		{Path: ".claude-plugin/plugin.json", Dir: true},
		{Path: ".claude-plugin/marketplace.json", Dir: true},
		{Path: ".claude/settings.json", Dir: true},
	}
	if diff := cmp.Diff(claude.Layout{}, claude.Discover(entries, nil)); diff != "" {
		t.Errorf("Layout (-want +got):\n%s", diff)
	}
}

func TestSkillName(t *testing.T) {
	t.Parallel()
	tests := []struct{ path, plugin, want string }{
		{".claude/skills/a/SKILL.md", "", "a"},
		{"skills/b/SKILL.md", "x", "b"},
		{"SKILL.md", "skill-pack", "skill-pack"},
		{"extra/SKILL.md", "skill-pack", "extra"},
	}
	for _, tc := range tests {
		if got := claude.SkillName(tc.path, tc.plugin); got != tc.want {
			t.Errorf("SkillName(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestCommandName(t *testing.T) {
	t.Parallel()
	tests := []struct{ path, want string }{
		{"commands/sweep-rules.md", "sweep-rules"},
		{".claude/commands/y.md", "y"},
		{"sub/.claude/commands/frontend/component.md", "frontend:component"},
		{".claude/commands/a/b/c.md", "a:b:c"},
	}
	for _, tc := range tests {
		if got := claude.CommandName(tc.path); got != tc.want {
			t.Errorf("CommandName(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
