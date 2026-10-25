package rules_test

import (
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// skillChecks is every check the Skills sub-table carries, in its column
// order.
var skillChecks = []rules.Check{
	rules.CheckSkillSlashOnly,
	rules.CheckSkillNameMatchesDir,
	rules.CheckSkillKnownKeys,
	rules.CheckSkillDescriptionLength,
	rules.CheckSkillNoWrapperCommands,
	rules.CheckSkillSize,
}

// goodSkill is a skill that passes every skill check: slash-only, named for
// its directory, with only known keys of the right type and a description
// well inside the limit.
func goodSkill(dir string) audit.ClaudeSkill {
	return audit.ClaudeSkill{
		Path: "skills/" + dir + "/SKILL.md",
		Dir:  dir,
		Name: dir,
		Keys: map[string]string{
			"name": "!!str", "description": "!!str", "disable-model-invocation": "!!bool",
		},
		DescriptionChars:       40,
		DisableModelInvocation: true,
		Lines:                  30,
	}
}

// goodCommand is a command file that passes the checks that judge commands:
// slash-only, with only known keys, wrapping no skill.
func goodCommand(dir string) audit.ClaudeSkill {
	return audit.ClaudeSkill{
		Path:                   "commands/" + dir + ".md",
		Dir:                    dir,
		Keys:                   map[string]string{"description": "!!str", "disable-model-invocation": "!!bool"},
		DescriptionChars:       20,
		DisableModelInvocation: true,
		Lines:                  5,
	}
}

func withClaude(c audit.Claude) audit.Repo {
	r := fixture("a", "tools")
	r.Claude = c
	return r
}

type claudeWant struct {
	verdict rules.Verdict
	value   string
}

func TestObserveClaude_skills(t *testing.T) {
	t.Parallel()

	na := claudeWant{rules.NA, ""}
	pass := func(v string) claudeWant { return claudeWant{rules.Pass, v} }
	fail := func(v string) claudeWant { return claudeWant{rules.Fail, v} }

	mutate := func(s audit.ClaudeSkill, f func(*audit.ClaudeSkill)) audit.ClaudeSkill {
		f(&s)
		return s
	}
	skills := func(s ...audit.ClaudeSkill) audit.Repo { return withClaude(audit.Claude{Skills: s}) }

	tests := []struct {
		name string
		repo audit.Repo
		want map[rules.Check]claudeWant
	}{
		{
			name: "no skills or commands is n/a throughout",
			repo: fixture("a", "tools"),
			want: map[rules.Check]claudeWant{
				rules.CheckSkillSlashOnly: na, rules.CheckSkillNameMatchesDir: na, rules.CheckSkillKnownKeys: na,
				rules.CheckSkillDescriptionLength: na, rules.CheckSkillNoWrapperCommands: na, rules.CheckSkillSize: na,
			},
		},
		{
			name: "a good skill passes and counts",
			repo: skills(goodSkill("skill-01")),
			want: map[rules.Check]claudeWant{
				rules.CheckSkillSlashOnly:         pass("1/1"),
				rules.CheckSkillNameMatchesDir:    pass("1/1"),
				rules.CheckSkillKnownKeys:         pass("1/1"),
				rules.CheckSkillDescriptionLength: pass("1/1"),
				rules.CheckSkillNoWrapperCommands: na,
				rules.CheckSkillSize:              {rules.Info, "30"},
			},
		},
		{
			name: "user-invocable false fails slash-only even with model invocation disabled",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) { s.UserInvocableFalse = true })),
			want: map[rules.Check]claudeWant{rules.CheckSkillSlashOnly: fail("0/1 skill-01")},
		},
		{
			name: "a command without disable-model-invocation fails slash-only",
			repo: withClaude(audit.Claude{
				Skills:   []audit.ClaudeSkill{goodSkill("skill-01")},
				Commands: []audit.ClaudeSkill{mutate(goodCommand("command-01"), func(s *audit.ClaudeSkill) { s.DisableModelInvocation = false })},
			}),
			want: map[rules.Check]claudeWant{
				rules.CheckSkillSlashOnly:         fail("1/2 command-01"),
				rules.CheckSkillNoWrapperCommands: pass("1/1"),
				// Name and description are judged on skills only.
				rules.CheckSkillNameMatchesDir:    pass("1/1"),
				rules.CheckSkillDescriptionLength: pass("1/1"),
			},
		},
		{
			name: "commands only: the skills-only checks are n/a",
			repo: withClaude(audit.Claude{Commands: []audit.ClaudeSkill{goodCommand("command-01")}}),
			want: map[rules.Check]claudeWant{
				rules.CheckSkillSlashOnly:         pass("1/1"),
				rules.CheckSkillKnownKeys:         pass("1/1"),
				rules.CheckSkillNoWrapperCommands: pass("1/1"),
				rules.CheckSkillNameMatchesDir:    na,
				rules.CheckSkillDescriptionLength: na,
				rules.CheckSkillSize:              na,
			},
		},
		{
			name: "a missing name fails name = dir",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) { s.Name = "" })),
			want: map[rules.Check]claudeWant{rules.CheckSkillNameMatchesDir: fail("0/1 skill-01")},
		},
		{
			name: "a name differing from the directory fails",
			repo: skills(goodSkill("skill-01"), mutate(goodSkill("skill-02"), func(s *audit.ClaudeSkill) { s.Name = "skill-03" })),
			want: map[rules.Check]claudeWant{rules.CheckSkillNameMatchesDir: fail("1/2 skill-02")},
		},
		{
			name: "agent as a boolean is mistyped",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) { s.Keys["agent"] = "!!bool" })),
			want: map[rules.Check]claudeWant{rules.CheckSkillKnownKeys: fail("0/1 skill-01")},
		},
		{
			name: "agent as a string is fine",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) { s.Keys["agent"] = "!!str" })),
			want: map[rules.Check]claudeWant{rules.CheckSkillKnownKeys: pass("1/1")},
		},
		{
			name: "an Agent Skills standard key is known",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) { s.Keys["license"] = "!!str" })),
			want: map[rules.Check]claudeWant{rules.CheckSkillKnownKeys: pass("1/1")},
		},
		{
			name: "a quoted disable-model-invocation fails known keys and slash-only",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) {
				s.Keys["disable-model-invocation"] = "!!str"
				s.DisableModelInvocation = false
			})),
			want: map[rules.Check]claudeWant{
				rules.CheckSkillKnownKeys: fail("0/1 skill-01"),
				rules.CheckSkillSlashOnly: fail("0/1 skill-01"),
			},
		},
		{
			name: "a quoted user-invocable is mistyped",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) { s.Keys["user-invocable"] = "!!str" })),
			want: map[rules.Check]claudeWant{rules.CheckSkillKnownKeys: fail("0/1 skill-01")},
		},
		{
			name: "an unknown key fails",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) { s.Keys["bogus"] = "!!str" })),
			want: map[rules.Check]claudeWant{rules.CheckSkillKnownKeys: fail("0/1 skill-01")},
		},
		{
			name: "an unknown key on a command fails",
			repo: withClaude(audit.Claude{Commands: []audit.ClaudeSkill{mutate(goodCommand("command-01"), func(s *audit.ClaudeSkill) { s.Keys["bogus"] = "!!str" })}}),
			want: map[rules.Check]claudeWant{rules.CheckSkillKnownKeys: fail("0/1 command-01")},
		},
		{
			name: "a description at the limit passes",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) { s.DescriptionChars = rules.SkillDescriptionMax })),
			want: map[rules.Check]claudeWant{rules.CheckSkillDescriptionLength: pass("1/1")},
		},
		{
			name: "a description one past the limit fails",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) { s.DescriptionChars = rules.SkillDescriptionMax + 1 })),
			want: map[rules.Check]claudeWant{rules.CheckSkillDescriptionLength: fail("0/1 skill-01")},
		},
		{
			name: "a skill with no description fails",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) {
				delete(s.Keys, "description")
				s.DescriptionChars = 0
			})),
			want: map[rules.Check]claudeWant{rules.CheckSkillDescriptionLength: fail("0/1 skill-01")},
		},
		{
			name: "a when_to_use with no description still fails",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) {
				delete(s.Keys, "description")
				s.Keys["when_to_use"] = "!!str"
			})),
			want: map[rules.Check]claudeWant{rules.CheckSkillDescriptionLength: fail("0/1 skill-01")},
		},
		{
			name: "a wrapper command fails and is named",
			repo: withClaude(audit.Claude{
				Skills: []audit.ClaudeSkill{goodSkill("skill-01")},
				Commands: []audit.ClaudeSkill{
					goodCommand("command-01"),
					mutate(goodCommand("command-02"), func(s *audit.ClaudeSkill) { s.WrapsSkill = "skill-01" }),
				},
			}),
			want: map[rules.Check]claudeWant{rules.CheckSkillNoWrapperCommands: fail("1/2 command-02")},
		},
		{
			name: "failing names are listed in path order across skills and commands",
			repo: withClaude(audit.Claude{
				Skills: []audit.ClaudeSkill{
					mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) { s.DisableModelInvocation = false }),
					goodSkill("skill-02"),
				},
				Commands: []audit.ClaudeSkill{
					mutate(goodCommand("command-01"), func(s *audit.ClaudeSkill) { s.DisableModelInvocation = false }),
				},
			}),
			want: map[rules.Check]claudeWant{rules.CheckSkillSlashOnly: fail("1/3 command-01, skill-01")},
		},
		{
			name: "an unreadable skill fails every check that judges it",
			repo: skills(goodSkill("skill-01"), audit.ClaudeSkill{
				Path: "skills/skill-02/SKILL.md", Dir: "skill-02",
				Unreadable: "no YAML frontmatter: the file does not start with ---",
			}),
			want: map[rules.Check]claudeWant{
				rules.CheckSkillSlashOnly:         fail("unreadable skills/skill-02/SKILL.md"),
				rules.CheckSkillNameMatchesDir:    fail("unreadable skills/skill-02/SKILL.md"),
				rules.CheckSkillKnownKeys:         fail("unreadable skills/skill-02/SKILL.md"),
				rules.CheckSkillDescriptionLength: fail("unreadable skills/skill-02/SKILL.md"),
				rules.CheckSkillNoWrapperCommands: na,
				rules.CheckSkillSize:              {rules.Info, "unreadable skills/skill-02/SKILL.md"},
			},
		},
		{
			name: "an unreadable command fails the checks that judge commands",
			repo: withClaude(audit.Claude{
				Skills:   []audit.ClaudeSkill{goodSkill("skill-01")},
				Commands: []audit.ClaudeSkill{{Path: "commands/command-01.md", Dir: "command-01", Unreadable: "frontmatter is not a YAML mapping"}},
			}),
			want: map[rules.Check]claudeWant{
				rules.CheckSkillSlashOnly:         fail("unreadable commands/command-01.md"),
				rules.CheckSkillKnownKeys:         fail("unreadable commands/command-01.md"),
				rules.CheckSkillNoWrapperCommands: fail("unreadable commands/command-01.md"),
				rules.CheckSkillNameMatchesDir:    pass("1/1"),
				rules.CheckSkillDescriptionLength: pass("1/1"),
				rules.CheckSkillSize:              {rules.Info, "30"},
			},
		},
		{
			name: "skill_size is the longest SKILL.md",
			repo: skills(
				mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) { s.Lines = 120 }),
				mutate(goodSkill("skill-02"), func(s *audit.ClaudeSkill) { s.Lines = 612 }),
			),
			want: map[rules.Check]claudeWant{rules.CheckSkillSize: {rules.Info, "612"}},
		},
		{
			name: "unreadable Claude files fail every skill check",
			repo: withClaude(audit.Claude{Unreadable: "too many Claude files"}),
			want: map[rules.Check]claudeWant{
				rules.CheckSkillSlashOnly:         fail("unreadable too many Claude files"),
				rules.CheckSkillNameMatchesDir:    fail("unreadable too many Claude files"),
				rules.CheckSkillKnownKeys:         fail("unreadable too many Claude files"),
				rules.CheckSkillDescriptionLength: fail("unreadable too many Claude files"),
				rules.CheckSkillNoWrapperCommands: fail("unreadable too many Claude files"),
				rules.CheckSkillSize:              {rules.Info, "unreadable too many Claude files"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cells, err := evaluateOne(tt.repo)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			for _, c := range skillChecks {
				want, ok := tt.want[c]
				if !ok {
					continue
				}
				got := cells[c]
				if got.Verdict != want.verdict || got.Value != want.value {
					t.Errorf("%s = %v %q; want %v %q", c, got.Verdict, got.Value, want.verdict, want.value)
				}
				// A failing count or an unreadable file shows its cross beside
				// the value; a passing count stands alone, as community_files
				// does.
				if wantMark := want.verdict == rules.Fail; got.Mark != wantMark && c != rules.CheckSkillSize {
					t.Errorf("%s.Mark = %t, want %t", c, got.Mark, wantMark)
				}
			}
		})
	}
}

// TestObserveClaude_skillGaps: each failing pass/fail skill check is one gap
// naming the repository, and skill_size, a plain fact, is never one.
func TestObserveClaude_skillGaps(t *testing.T) {
	t.Parallel()

	bad := audit.ClaudeSkill{
		Path: "skills/skill-01/SKILL.md", Dir: "skill-01",
		Keys: map[string]string{"bogus": "!!str"}, DescriptionChars: rules.SkillDescriptionMax + 1, Lines: 900,
	}
	cmd := goodCommand("command-01")
	cmd.WrapsSkill = "skill-01"
	rep := mustEvaluateOne(t, withClaude(audit.Claude{Skills: []audit.ClaudeSkill{bad}, Commands: []audit.ClaudeSkill{cmd}}))

	want := map[rules.Check]string{
		rules.CheckSkillSlashOnly:         "Skill can run without its / command",
		rules.CheckSkillNameMatchesDir:    "Skill name differs from its directory",
		rules.CheckSkillKnownKeys:         "Skill frontmatter has unknown or mistyped keys",
		rules.CheckSkillDescriptionLength: "Skill description too long",
		rules.CheckSkillNoWrapperCommands: "Command file only wraps a skill",
	}
	got := map[rules.Check]string{}
	for _, g := range rep.Gaps {
		if g.Check == rules.CheckSkillSize {
			t.Errorf("Gaps holds skill_size %v, want it never a gap", g)
		}
		if _, ok := want[g.Check]; ok {
			got[g.Check] = g.Label
			if len(g.Repos) != 1 || g.Repos[0] != "a" {
				t.Errorf("gap %s repos = %v, want [a]", g.Check, g.Repos)
			}
		}
	}
	for c, label := range want {
		if got[c] != label {
			t.Errorf("gap %s label = %q, want %q", c, got[c], label)
		}
	}
}

// TestObserveClaude_skillSizeNotRequired: a type that turns skill_size off
// still shows the line count, as every value-only check keeps its value.
func TestObserveClaude_skillSizeNotRequired(t *testing.T) {
	t.Parallel()

	r := withClaude(audit.Claude{Skills: []audit.ClaudeSkill{goodSkill("skill-01")}})
	rep, err := rules.Evaluate(&audit.Snapshot{
		GeneratedAt: cacheNow, Types: typesWith("tools", rules.CheckSkillSize, rules.OverrideNotRequired),
		Identity: fixtureIdentity(), Repos: []audit.Repo{r},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	got := rep.Repos[0].Cell(rules.CheckSkillSize)
	if got.Verdict != rules.NA || got.Value != "30" || !got.Scalar {
		t.Errorf("skill_size = %v %q scalar=%t; want n/a \"30\" scalar", got.Verdict, got.Value, got.Scalar)
	}
}

// hookChecks is every check the Hooks sub-table carries, in its column order.
var hookChecks = []rules.Check{
	rules.CheckHookKnownEvents,
	rules.CheckHookCommandExecutable,
	rules.CheckHookPluginRoot,
	rules.CheckHookTimeout,
	rules.CheckHookMatcher,
}

// goodHook is a plugin command hook that passes every hook check: a known
// event with a tool matcher, an explicit timeout, and an executable script
// named through the plugin root.
func goodHook() audit.ClaudeHook {
	return audit.ClaudeHook{
		Source: "hooks/hooks.json", Plugin: true, Event: "PreToolUse", Type: "command",
		Matcher: "Bash", Timeout: 10, TimeoutSet: true,
		Script: "hooks/hook-01.sh", PathForm: "plugin_root", ScriptMode: "100755",
	}
}

func TestObserveClaude_hooks(t *testing.T) {
	t.Parallel()

	na := claudeWant{rules.NA, ""}
	pass := func(v string) claudeWant { return claudeWant{rules.Pass, v} }
	fail := func(v string) claudeWant { return claudeWant{rules.Fail, v} }
	mutate := func(f func(*audit.ClaudeHook)) audit.ClaudeHook {
		h := goodHook()
		f(&h)
		return h
	}
	hooks := func(h ...audit.ClaudeHook) audit.Repo { return withClaude(audit.Claude{Hooks: h}) }
	allNA := map[rules.Check]claudeWant{}
	for _, c := range hookChecks {
		allNA[c] = na
	}
	all := func(w claudeWant) map[rules.Check]claudeWant {
		m := map[rules.Check]claudeWant{}
		for _, c := range hookChecks {
			m[c] = w
		}
		return m
	}
	// only is the all-pass row with one check replaced.
	only := func(c rules.Check, w claudeWant) map[rules.Check]claudeWant {
		m := all(pass("1/1"))
		m[c] = w
		return m
	}
	const name = "hooks/hooks.json PreToolUse"

	tests := []struct {
		name string
		repo audit.Repo
		want map[rules.Check]claudeWant
	}{
		{"no hooks are n/a", withClaude(audit.Claude{}), allNA},
		{"a good hook passes every check", hooks(goodHook()), all(pass("1/1"))},
		{
			"a misspelled event",
			hooks(mutate(func(h *audit.ClaudeHook) { h.Event = "PreToolUSe" })),
			// The misspelled event is also not a tool event, so the matcher
			// check skips it.
			map[rules.Check]claudeWant{
				rules.CheckHookKnownEvents:       fail("0/1 hooks/hooks.json PreToolUSe"),
				rules.CheckHookCommandExecutable: pass("1/1"),
				rules.CheckHookPluginRoot:        pass("1/1"),
				rules.CheckHookTimeout:           pass("1/1"),
				rules.CheckHookMatcher:           na,
			},
		},
		{
			"a script that is not executable",
			hooks(mutate(func(h *audit.ClaudeHook) { h.ScriptMode = "100644" })),
			only(rules.CheckHookCommandExecutable, fail("0/1 "+name)),
		},
		{
			"an interpreted script needs no execute bit",
			hooks(mutate(func(h *audit.ClaudeHook) { h.ScriptMode = "100644"; h.Interpreter = true })),
			all(pass("1/1")),
		},
		{
			"a script that is not tracked fails even through an interpreter",
			hooks(mutate(func(h *audit.ClaudeHook) { h.ScriptMode = ""; h.Interpreter = true })),
			only(rules.CheckHookCommandExecutable, fail("0/1 "+name)),
		},
		{
			"a command naming no repository file is skipped",
			hooks(mutate(func(h *audit.ClaudeHook) { h.Script = ""; h.ScriptMode = ""; h.PathForm = "" })),
			map[rules.Check]claudeWant{
				rules.CheckHookKnownEvents:       pass("1/1"),
				rules.CheckHookCommandExecutable: na,
				rules.CheckHookPluginRoot:        na,
				rules.CheckHookTimeout:           pass("1/1"),
				rules.CheckHookMatcher:           pass("1/1"),
			},
		},
		{
			"an interpreter with no script is skipped",
			hooks(mutate(func(h *audit.ClaudeHook) { h.Script = ""; h.ScriptMode = ""; h.Interpreter = true })),
			map[rules.Check]claudeWant{
				rules.CheckHookCommandExecutable: na,
				rules.CheckHookPluginRoot:        na,
			},
		},
		{
			"a relative plugin path",
			hooks(mutate(func(h *audit.ClaudeHook) { h.PathForm = "relative" })),
			only(rules.CheckHookPluginRoot, fail("0/1 "+name)),
		},
		{
			"a non-plugin hook is not judged for the plugin root",
			hooks(mutate(func(h *audit.ClaudeHook) { h.Plugin = false; h.PathForm = "project_dir" })),
			only(rules.CheckHookPluginRoot, na),
		},
		{
			"an http handler is judged by events, timeout and matcher only",
			hooks(mutate(func(h *audit.ClaudeHook) {
				h.Type = "http"
				h.Script, h.ScriptMode, h.PathForm = "", "", ""
				h.Event = "PreToolUSe"
				h.TimeoutSet = false
			})),
			map[rules.Check]claudeWant{
				rules.CheckHookKnownEvents:       fail("0/1 hooks/hooks.json PreToolUSe"),
				rules.CheckHookCommandExecutable: na,
				rules.CheckHookPluginRoot:        na,
				rules.CheckHookTimeout:           fail("0/1 hooks/hooks.json PreToolUSe"),
				rules.CheckHookMatcher:           na,
			},
		},
		{
			"an http handler that still carries a script path is not judged as a command",
			hooks(mutate(func(h *audit.ClaudeHook) { h.Type = "http"; h.ScriptMode = ""; h.PathForm = "relative" })),
			map[rules.Check]claudeWant{
				rules.CheckHookCommandExecutable: na,
				rules.CheckHookPluginRoot:        na,
			},
		},
		{
			"no timeout",
			hooks(mutate(func(h *audit.ClaudeHook) { h.TimeoutSet = false; h.Timeout = 0 })),
			only(rules.CheckHookTimeout, fail("0/1 "+name)),
		},
		{
			"a tool event without a matcher",
			hooks(mutate(func(h *audit.ClaudeHook) { h.Matcher = "" })),
			only(rules.CheckHookMatcher, fail("0/1 "+name)),
		},
		{
			"a non-tool event needs no matcher",
			hooks(mutate(func(h *audit.ClaudeHook) { h.Event = "SessionStart"; h.Matcher = "" })),
			only(rules.CheckHookMatcher, na),
		},
		{
			"counts across hooks name the failing ones in order",
			hooks(
				mutate(func(h *audit.ClaudeHook) { h.Matcher = "" }),
				mutate(func(h *audit.ClaudeHook) { h.Event = "PostToolUse"; h.Matcher = "" }),
				goodHook(),
			),
			map[rules.Check]claudeWant{
				rules.CheckHookKnownEvents: pass("3/3"),
				rules.CheckHookTimeout:     pass("3/3"),
				rules.CheckHookMatcher:     fail("1/3 hooks/hooks.json PreToolUse, hooks/hooks.json PostToolUse"),
			},
		},
		{
			"an unreadable hook file fails every hook check",
			hooks(audit.ClaudeHook{Source: "hooks/hooks.json", Plugin: true, Unreadable: "not valid JSON: x"}),
			all(fail("unreadable hooks/hooks.json")),
		},
		{
			"unreadable Claude files fail every hook check",
			withClaude(audit.Claude{Unreadable: "too many Claude files"}),
			all(fail("unreadable too many Claude files")),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cells, err := evaluateOne(tt.repo)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			for _, c := range hookChecks {
				want, ok := tt.want[c]
				if !ok {
					continue
				}
				got := cells[c]
				if got.Verdict != want.verdict || got.Value != want.value {
					t.Errorf("%s = %v %q; want %v %q", c, got.Verdict, got.Value, want.verdict, want.value)
				}
				if wantMark := want.verdict == rules.Fail; got.Mark != wantMark {
					t.Errorf("%s.Mark = %t, want %t", c, got.Mark, wantMark)
				}
			}
		})
	}
}

// TestObserveClaude_hookGaps: each failing hook check is one gap with its
// label, naming the repository.
func TestObserveClaude_hookGaps(t *testing.T) {
	t.Parallel()

	bad := audit.ClaudeHook{
		Source: "hooks/hooks.json", Plugin: true, Event: "PreToolUSe", Type: "command",
		Script: "hooks/hook-01.sh", PathForm: "relative", ScriptMode: "100644",
	}
	badTool := bad
	badTool.Event = "PreToolUse"
	rep := mustEvaluateOne(t, withClaude(audit.Claude{Hooks: []audit.ClaudeHook{bad, badTool}}))

	want := map[rules.Check]string{
		rules.CheckHookKnownEvents:       "Hook on an unknown event",
		rules.CheckHookCommandExecutable: "Hook script missing or not executable",
		rules.CheckHookPluginRoot:        "Plugin hook path not under ${CLAUDE_PLUGIN_ROOT}",
		rules.CheckHookTimeout:           "Hook without a timeout",
		rules.CheckHookMatcher:           "Tool hook without a matcher",
	}
	got := map[rules.Check]string{}
	for _, g := range rep.Gaps {
		if _, ok := want[g.Check]; ok {
			got[g.Check] = g.Label
			if len(g.Repos) != 1 || g.Repos[0] != "a" {
				t.Errorf("gap %s repos = %v, want [a]", g.Check, g.Repos)
			}
		}
	}
	for c, label := range want {
		if got[c] != label {
			t.Errorf("gap %s label = %q, want %q", c, got[c], label)
		}
	}
}
