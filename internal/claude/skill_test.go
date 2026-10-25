package claude_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/claude"
)

func TestParseSkill(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		front string
		check func(t *testing.T, s claude.Skill)
	}{
		{
			name:  "description and when_to_use are counted together in runes",
			front: "name: a\ndescription: héllo wörl\nwhen_to_use: abcde\ndisable-model-invocation: true",
			check: func(t *testing.T, s claude.Skill) {
				t.Helper()
				if s.DescriptionChars != 15 || s.Name != "a" || !s.DisableModelInvocation {
					t.Errorf("got %+v", s)
				}
				if s.Keys["disable-model-invocation"] != "!!bool" || s.Keys["description"] != "!!str" {
					t.Errorf("Keys = %v", s.Keys)
				}
			},
		},
		{
			name:  "a quoted true is a string, not a boolean",
			front: `disable-model-invocation: "true"`,
			check: func(t *testing.T, s claude.Skill) {
				t.Helper()
				if s.DisableModelInvocation || s.Keys["disable-model-invocation"] != "!!str" {
					t.Errorf("got %+v", s)
				}
			},
		},
		{
			name:  "yes is not the YAML boolean true",
			front: `disable-model-invocation: yes`,
			check: func(t *testing.T, s claude.Skill) {
				t.Helper()
				if s.DisableModelInvocation {
					t.Errorf("yes counted as true")
				}
			},
		},
		{
			name:  "user-invocable false",
			front: `user-invocable: false`,
			check: func(t *testing.T, s claude.Skill) {
				t.Helper()
				if !s.UserInvocableFalse {
					t.Errorf("UserInvocableFalse not set")
				}
			},
		},
		{
			name:  "quoted user-invocable false is not false",
			front: `user-invocable: "false"`,
			check: func(t *testing.T, s claude.Skill) {
				t.Helper()
				if s.UserInvocableFalse {
					t.Errorf("a string counted as false")
				}
			},
		},
		{
			name:  "agent true keeps its bool tag",
			front: `agent: true`,
			check: func(t *testing.T, s claude.Skill) {
				t.Helper()
				if s.Keys["agent"] != "!!bool" {
					t.Errorf("Keys = %v", s.Keys)
				}
			},
		},
		{
			name:  "a mapping value is tagged !!map",
			front: "metadata:\n  a: b",
			check: func(t *testing.T, s claude.Skill) {
				t.Helper()
				if s.Keys["metadata"] != "!!map" {
					t.Errorf("Keys = %v", s.Keys)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := claude.ParseSkill("p/SKILL.md", "p", "---\n"+tc.front+"\n---\nbody\n", false)
			if s.Unreadable != "" {
				t.Fatalf("unreadable: %s", s.Unreadable)
			}
			if s.Path != "p/SKILL.md" || s.Dir != "p" {
				t.Errorf("Path/Dir = %q/%q", s.Path, s.Dir)
			}
			tc.check(t, s)
		})
	}
}

func TestParseSkill_lines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text string
		want int
	}{
		{"---\na: b\n---\nx\n", 4},
		{"---\na: b\n---\nx", 4},
		{"---\na: b\n---", 3},
		{"---\r\na: b\r\n---\r\nx\r\n", 4},
	}
	for _, tc := range tests {
		if got := claude.ParseSkill("p", "p", tc.text, false).Lines; got != tc.want {
			t.Errorf("Lines(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}

func TestParseSkill_aliases(t *testing.T) {
	t.Parallel()
	s := claude.ParseSkill("p", "p", "---\nx: &a true\ndisable-model-invocation: *a\n---\n", false)
	if !s.DisableModelInvocation {
		t.Errorf("an alias to true was not followed: %+v", s)
	}
}

func TestParseSkill_body(t *testing.T) {
	t.Parallel()
	s := claude.ParseSkill("p", "p", "---\na: b\n---\nline one\n---\nline two\n", false)
	if s.Body != "line one\n---\nline two\n" {
		t.Errorf("Body = %q", s.Body)
	}
	if got := claude.ParseSkill("p", "p", "---\na: b\n---", false).Body; got != "" {
		t.Errorf("Body = %q, want empty", got)
	}
}

func TestParseSkill_unreadable(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"not closed":                  "---\nname: a\nbody\n",
		"a lone opening fence":        "---",
		"a YAML list":                 "---\n- a\n- b\n---\nbody\n",
		"a YAML scalar":               "---\njust text\n---\n",
		"YAML syntax error":           "---\nname: [unclosed\n---\nbody\n",
		"duplicate top-level key":     "---\ndisable-model-invocation: true\ndisable-model-invocation: false\n---\n",
		"empty frontmatter":           "---\n---\nbody\n",
		"empty and unterminated":      "---\n",
		"empty and closed at the end": "---\n---",
	}
	for name, text := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := claude.ParseSkill("p/SKILL.md", "p", text, true)
			want := claude.Skill{Path: "p/SKILL.md", Dir: "p", Unreadable: s.Unreadable}
			if s.Unreadable == "" {
				t.Fatalf("not unreadable: %+v", s)
			}
			if diff := cmp.Diff(want, s); diff != "" {
				t.Errorf("other facts must be zero (-want +got):\n%s", diff)
			}
		})
	}
}

// A file that does not open with a --- line has no frontmatter, which Claude
// Code loads as a skill or command with no metadata. It is readable, with no
// keys, so each check judges the missing keys on their merits. Keys stays nil
// rather than empty so audit.json omits it, as its schema requires.
func TestParseSkill_noFrontmatter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, text string
		lines      int
	}{
		{"plain markdown", "# Title\nbody\n", 2},
		{"CRLF markdown", "# Title\r\nbody\r\n", 2},
		{"a fence that is not the first line", "\n---\nname: a\n---\n", 4},
		{"a longer rule is not a fence", "----\nname: a\n", 2},
		{"empty file", "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := claude.ParseSkill("p/SKILL.md", "p", tc.text, true)
			want := claude.Skill{
				Path: "p/SKILL.md", Dir: "p", Lines: tc.lines,
				Body: strings.ReplaceAll(tc.text, "\r\n", "\n"),
			}
			if diff := cmp.Diff(want, s); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseSkill_hooks(t *testing.T) {
	t.Parallel()
	text := `---
name: a
hooks:
  PreToolUse:
    - matcher: Bash
      hooks:
        - type: command
          command: ./hooks/x.sh
          timeout: 5
---
`
	s := claude.ParseSkill("p/SKILL.md", "p", text, true)
	if len(s.Hooks) != 1 {
		t.Fatalf("Hooks = %+v", s.Hooks)
	}
	h := s.Hooks[0]
	if h.Event != "PreToolUse" || h.Source != "p/SKILL.md" || !h.Plugin || h.Matcher != "Bash" ||
		!h.TimeoutSet || h.Timeout != 5 || h.Script != "hooks/x.sh" {
		t.Errorf("hook = %+v", h)
	}
}

func TestParseSkill_hooksWrongShape(t *testing.T) {
	t.Parallel()
	s := claude.ParseSkill("p", "p", "---\nhooks: nope\n---\n", false)
	if s.Unreadable != "" || len(s.Hooks) != 1 || s.Hooks[0].Unreadable == "" {
		t.Errorf("got %+v", s)
	}
}

func TestParseSkill_hooksUndecodable(t *testing.T) {
	t.Parallel()
	s := claude.ParseSkill("p", "p", "---\nhooks:\n  Stop: []\n  Stop: []\n---\n", false)
	if s.Unreadable != "" || len(s.Hooks) != 1 || s.Hooks[0].Unreadable == "" {
		t.Errorf("got %+v", s)
	}
}

func TestWrappedSkill(t *testing.T) {
	t.Parallel()
	skills := []string{"docs-audit", "docs-fix"}
	tests := []struct {
		name, body, want string
	}{
		{"one skill", "Use the `docs-audit` skill", "docs-audit"},
		{"same skill twice", "docs-audit then docs-audit", "docs-audit"},
		{"two skills", "Run docs-audit, then docs-fix.", ""},
		{"longer name is a different word", "Use docs-audit-extra", ""},
		{"prefix of a longer word", "Use xdocs-audit", ""},
		{"none", "Do the thing", ""},
		{"at the start and end", "docs-fix", "docs-fix"},
		{"case sensitive", "Docs-Audit", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := claude.WrappedSkill(tc.body, skills); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
	if got := claude.WrappedSkill(strings.Repeat("x ", 3), []string{""}); got != "" {
		t.Errorf("an empty skill name matched: %q", got)
	}
}
