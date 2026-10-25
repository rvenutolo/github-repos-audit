package claude_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/claude"
)

const fullManifest = `{
  "$schema": "https://example.invalid/schema.json",
  "name": "plugin-kit",
  "version": "1.2.3",
  "description": "d",
  "author": {"name": "A"},
  "license": "MIT",
  "homepage": "https://example.invalid/h",
  "repository": "https://example.invalid/r"
}`

var allFields = []string{"$schema", "author", "description", "homepage", "license", "name", "repository", "version"}

func TestParsePlugin_fields(t *testing.T) {
	t.Parallel()
	p := claude.ParsePlugin(fullManifest)
	if p.Unreadable != "" {
		t.Fatal(p.Unreadable)
	}
	if diff := cmp.Diff(allFields, p.Fields); diff != "" {
		t.Errorf("Fields (-want +got):\n%s", diff)
	}
	if p.Name != "plugin-kit" || p.Version != "1.2.3" {
		t.Errorf("Name/Version = %q/%q", p.Name, p.Version)
	}
}

func TestParsePlugin_presence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, patch, absent string
	}{
		{"author without a name", `"author": {"name": ""}`, "author"},
		{"author a string", `"author": "A"`, "author"},
		{"author name wrong type", `"author": {"name": 1}`, "author"},
		{"homepage not a URL", `"homepage": "not a url"`, "homepage"},
		{"homepage wrong scheme", `"homepage": "ftp://example.invalid"`, "homepage"},
		{"homepage wrong type", `"homepage": 1`, "homepage"},
		{"repository not a URL", `"repository": "gh-owner/plugin-kit"`, "repository"},
		{"repository an object", `"repository": {"url": "https://example.invalid"}`, "repository"},
		{"license empty", `"license": ""`, "license"},
		{"version blank", `"version": "  "`, "version"},
		{"version a number", `"version": 1`, "version"},
		{"description null", `"description": null`, "description"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// A later duplicate key wins in encoding/json.
			text := fullManifest[:len(fullManifest)-2] + ",\n" + tc.patch + "}"
			p := claude.ParsePlugin(text)
			if p.Unreadable != "" {
				t.Fatal(p.Unreadable)
			}
			for _, f := range p.Fields {
				if f == tc.absent {
					t.Errorf("%s counted as present: %v", tc.absent, p.Fields)
				}
			}
			if len(p.Fields) != len(allFields)-1 {
				t.Errorf("Fields = %v", p.Fields)
			}
		})
	}
}

func TestParsePlugin_missingFields(t *testing.T) {
	t.Parallel()
	p := claude.ParsePlugin(`{}`)
	if p.Unreadable != "" || len(p.Fields) != 0 || p.CommandsSet || p.SkillsSet {
		t.Errorf("got %+v", p)
	}
}

func TestParsePlugin_components(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		json  string
		check func(t *testing.T, p claude.Plugin)
	}{
		{"commands array", `{"commands": ["./cmds/a.md"]}`, func(t *testing.T, p claude.Plugin) {
			t.Helper()
			if !p.CommandsSet || !cmp.Equal(p.CommandPaths, []string{"cmds/a.md"}) {
				t.Errorf("got %+v", p)
			}
		}},
		{"commands string", `{"commands": "./cmds/"}`, func(t *testing.T, p claude.Plugin) {
			t.Helper()
			if !p.CommandsSet || !cmp.Equal(p.CommandPaths, []string{"cmds"}) {
				t.Errorf("got %+v", p)
			}
		}},
		{"commands empty array still replaces the default", `{"commands": []}`, func(t *testing.T, p claude.Plugin) {
			t.Helper()
			if !p.CommandsSet || p.CommandPaths != nil {
				t.Errorf("got %+v", p)
			}
		}},
		{"commands object map", `{"commands": {"about": {"content": "text"}, "status": {"source": "./c/s.md"}}}`, func(t *testing.T, p claude.Plugin) {
			t.Helper()
			if !p.CommandsSet || !cmp.Equal(p.InlineCommands, []string{"about"}) ||
				!cmp.Equal(p.InlineContent, map[string]string{"about": "text"}) || p.CommandPaths != nil ||
				!cmp.Equal(p.SourceCommands, map[string]string{"status": "c/s.md"}) {
				t.Errorf("got %+v", p)
			}
		}},
		{"commands object map descriptions", `{"commands": {"about": {"content": "text", "description": "About it"}, "bare": {"content": "x"}, "status": {"source": "./c/s.md", "description": "Status"}}}`, func(t *testing.T, p claude.Plugin) {
			t.Helper()
			// Both an inline and a source entry's description is what
			// Claude Code shows for the command; an entry without one has
			// none recorded.
			if !cmp.Equal(p.CommandDescriptions, map[string]string{"about": "About it", "status": "Status"}) {
				t.Errorf("CommandDescriptions = %v", p.CommandDescriptions)
			}
		}},
		{"skills string", `{"skills": "./extra"}`, func(t *testing.T, p claude.Plugin) {
			t.Helper()
			if !p.SkillsSet || !cmp.Equal(p.SkillDirs, []string{"extra"}) {
				t.Errorf("got %+v", p)
			}
		}},
		{"skills array", `{"skills": ["./a", "./b/"]}`, func(t *testing.T, p claude.Plugin) {
			t.Helper()
			if !cmp.Equal(p.SkillDirs, []string{"a", "b"}) {
				t.Errorf("got %+v", p)
			}
		}},
		{"skills root", `{"skills": "."}`, func(t *testing.T, p claude.Plugin) {
			t.Helper()
			if !cmp.Equal(p.SkillDirs, []string{"."}) {
				t.Errorf("got %+v", p)
			}
		}},
		{"skills root as ./", `{"skills": "./"}`, func(t *testing.T, p claude.Plugin) {
			t.Helper()
			if !cmp.Equal(p.SkillDirs, []string{"."}) {
				t.Errorf("got %+v", p)
			}
		}},
		{"commands root as ./", `{"commands": "./"}`, func(t *testing.T, p claude.Plugin) {
			t.Helper()
			if !cmp.Equal(p.CommandPaths, []string{"."}) {
				t.Errorf("got %+v", p)
			}
		}},
		{"hooks path", `{"hooks": "./h.json"}`, func(t *testing.T, p claude.Plugin) {
			t.Helper()
			if p.HooksFile != "h.json" || !cmp.Equal(p.HooksFiles, []string{"h.json"}) {
				t.Errorf("got %+v", p)
			}
		}},
		{"hooks inline", `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "x"}]}]}}`, func(t *testing.T, p claude.Plugin) {
			t.Helper()
			if len(p.Hooks) != 1 || p.Hooks[0].Event != "Stop" || !p.Hooks[0].Plugin || p.Hooks[0].Source != ".claude-plugin/plugin.json" {
				t.Errorf("got %+v", p)
			}
		}},
		{"hooks array of both", `{"hooks": ["./h.json", {"Stop": []}, "./i.json"]}`, func(t *testing.T, p claude.Plugin) {
			t.Helper()
			if p.HooksFile != "h.json" || !cmp.Equal(p.HooksFiles, []string{"h.json", "i.json"}) {
				t.Errorf("got %+v", p)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := claude.ParsePlugin(tc.json)
			if p.Unreadable != "" {
				t.Fatal(p.Unreadable)
			}
			tc.check(t, p)
		})
	}
}

func TestParsePlugin_unreadable(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"invalid JSON":                             `{`,
		"not an object":                            `[]`,
		"skills a number":                          `{"skills": 1}`,
		"skills a list of numbers":                 `{"skills": [1]}`,
		"commands a number":                        `{"commands": 1}`,
		"commands entry not object":                `{"commands": {"a": "x"}}`,
		"commands entry has both":                  `{"commands": {"a": {"source": "./a.md", "content": "x"}}}`,
		"commands entry has neither":               `{"commands": {"a": {}}}`,
		"skills empty":                             `{"skills": ""}`,
		"skills array with empty":                  `{"skills": ["./a", ""]}`,
		"commands empty":                           `{"commands": ""}`,
		"commands array with empty":                `{"commands": [""]}`,
		"commands source empty":                    `{"commands": {"a": {"source": ""}}}`,
		"commands description not a string":        `{"commands": {"a": {"content": "x", "description": 1}}}`,
		"commands source description not a string": `{"commands": {"a": {"source": "./a.md", "description": ["x"]}}}`,
		"hooks empty path":                         `{"hooks": ""}`,
		"hooks array with empty":                   `{"hooks": ["./h.json", ""]}`,
		"hooks a number":                           `{"hooks": 1}`,
		"hooks array of numbers":                   `{"hooks": [1]}`,
		// Path rules: every component path starts with "./", and only skills
		// also takes ".".
		"skills without ./":          `{"skills": "extra"}`,
		"skills array without ./":    `{"skills": ["./a", "b"]}`,
		"commands without ./":        `{"commands": "commands/a.md"}`,
		"commands array without ./":  `{"commands": ["./a.md", "b.md"]}`,
		"commands source without ./": `{"commands": {"a": {"source": "a.md"}}}`,
		"commands the root as .":     `{"commands": "."}`,
		"hooks without ./":           `{"hooks": "hooks/extra.json"}`,
		"hooks array without ./":     `{"hooks": [{"Stop": []}, "h.json"]}`,
		"hooks a parent path":        `{"hooks": "../h.json"}`,
	}
	for name, text := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := claude.ParsePlugin(text)
			if p.Unreadable == "" {
				t.Fatalf("readable: %+v", p)
			}
			if diff := cmp.Diff(claude.Plugin{Unreadable: p.Unreadable}, p); diff != "" {
				t.Errorf("other facts must be zero (-want +got):\n%s", diff)
			}
		})
	}
}
