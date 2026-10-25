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
			name:     "declared command file",
			paths:    []string{"cmds/a.md", "cmds/b.md"},
			manifest: &claude.Plugin{CommandsSet: true, CommandPaths: []string{"cmds/a.md", "missing.md"}},
			want:     claude.Layout{Commands: []string{"cmds/a.md"}},
		},
		{
			name:     "inline-only commands find no files",
			paths:    []string{"commands/x.md"},
			manifest: &claude.Plugin{CommandsSet: true, InlineCommands: []string{"about"}},
			want:     claude.Layout{},
		},
		{
			name:  "project commands are always included",
			paths: []string{".claude/commands/y.md", "sub/.claude/commands/w.md", ".claude/commands/deep/v.md"},
			want:  claude.Layout{Commands: []string{".claude/commands/y.md", "sub/.claude/commands/w.md"}},
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
			name:     "declared hooks file",
			paths:    []string{"hooks/hooks.json", "config/h.json"},
			manifest: &claude.Plugin{HooksFile: "config/h.json", HooksFiles: []string{"config/h.json", "config/gone.json", "hooks/hooks.json"}},
			want:     claude.Layout{HookFiles: []string{"config/h.json", "hooks/hooks.json"}},
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
	if got := claude.CommandName("commands/sweep-rules.md"); got != "sweep-rules" {
		t.Errorf("got %q", got)
	}
}
