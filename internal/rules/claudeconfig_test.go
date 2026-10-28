package rules_test

import (
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// TestEvaluate_claudeConfigFiles: claude_mcp_config and claude_settings name
// the Claude Code configuration files a repository commits, as inline code,
// and are never judged: a repository with none is information too, never a
// gap.
func TestEvaluate_claudeConfigFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		files        func(*audit.Files)
		wantMCP      string
		wantSettings string
	}{
		{"none", func(*audit.Files) {}, "", ""},
		{"MCP config only", func(f *audit.Files) { f.MCPConfig = ".mcp.json" }, ".mcp.json", ""},
		{
			"settings files",
			func(f *audit.Files) {
				f.ClaudeSettings = []string{".claude/settings.json", "sub/.claude/settings.local.json"}
			},
			"", ".claude/settings.json, sub/.claude/settings.local.json",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := fixture("alpha", "tools")
			tc.files(&r.Files)
			rep := mustEvaluateOne(t, r)
			for c, want := range map[rules.Check]string{
				rules.CheckClaudeMCPConfig: tc.wantMCP,
				rules.CheckClaudeSettings:  tc.wantSettings,
			} {
				got := rep.Repos[0].Cell(c)
				if got.Verdict != rules.Info || got.Value != want || !got.Scalar || got.Mark || !got.Code {
					t.Errorf("%s = %v %q scalar=%t mark=%t code=%t, want an unmarked scalar info %q as code",
						c, got.Verdict, got.Value, got.Scalar, got.Mark, got.Code, want)
				}
			}
			for _, g := range rep.Gaps {
				if g.Check == rules.CheckClaudeMCPConfig || g.Check == rules.CheckClaudeSettings {
					t.Errorf("Gaps holds %s %v, want it never a gap", g.Check, g)
				}
			}
		})
	}
}

// TestEvaluate_claudeConfigFilesNotRequired: a type that turns either check
// off still shows the files, as every value-only check keeps its value.
func TestEvaluate_claudeConfigFilesNotRequired(t *testing.T) {
	t.Parallel()

	for c, want := range map[rules.Check]string{
		rules.CheckClaudeMCPConfig: ".mcp.json",
		rules.CheckClaudeSettings:  ".claude/settings.json",
	} {
		r := fixture("alpha", "tools")
		r.Files.MCPConfig = ".mcp.json"
		r.Files.ClaudeSettings = []string{".claude/settings.json"}
		rep, err := rules.Evaluate(&audit.Snapshot{
			GeneratedAt: cacheNow, Types: typesWith("tools", c, rules.OverrideNotRequired),
			Identity: fixtureIdentity(), Repos: []audit.Repo{r},
		})
		if err != nil {
			t.Fatalf("Evaluate() error = %v, want nil", err)
		}
		if got := rep.Repos[0].Cell(c); got.Verdict != rules.NA || got.Value != want || !got.Scalar {
			t.Errorf("%s = %v %q scalar=%t; want n/a %q scalar", c, got.Verdict, got.Value, got.Scalar, want)
		}
	}
}
