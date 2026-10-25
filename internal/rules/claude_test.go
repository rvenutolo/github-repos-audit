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
		HasDescription:         true,
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
		HasDescription:         true,
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
				s.HasDescription = false
				s.DescriptionChars = 0
			})),
			want: map[rules.Check]claudeWant{rules.CheckSkillDescriptionLength: fail("0/1 skill-01")},
		},
		{
			name: "a when_to_use with no description still fails",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) {
				delete(s.Keys, "description")
				s.HasDescription = false
				s.Keys["when_to_use"] = "!!str"
			})),
			want: map[rules.Check]claudeWant{rules.CheckSkillDescriptionLength: fail("0/1 skill-01")},
		},
		{
			// What internal/claude records for `description:` with no
			// value, `description: ""` or `description: [a]`: the key is
			// there, but it says nothing.
			name: "a description key with no text fails",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) {
				s.Keys["description"] = "!!null"
				s.HasDescription = false
				s.DescriptionChars = 0
			})),
			want: map[rules.Check]claudeWant{rules.CheckSkillDescriptionLength: fail("0/1 skill-01")},
		},
		{
			name: "a description list fails even with when_to_use text",
			repo: skills(mutate(goodSkill("skill-01"), func(s *audit.ClaudeSkill) {
				s.Keys["description"] = "!!seq"
				s.Keys["when_to_use"] = "!!str"
				s.HasDescription = false
				s.DescriptionChars = 12
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
			// What internal/claude records for a file with no frontmatter:
			// readable, with no keys. It fails what it lacks rather than
			// failing everything as unreadable.
			name: "a skill without frontmatter fails on its merits",
			repo: skills(audit.ClaudeSkill{Path: "skills/skill-01/SKILL.md", Dir: "skill-01", Lines: 4}),
			want: map[rules.Check]claudeWant{
				rules.CheckSkillSlashOnly:         fail("0/1 skill-01"),
				rules.CheckSkillNameMatchesDir:    fail("0/1 skill-01"),
				rules.CheckSkillKnownKeys:         pass("1/1"),
				rules.CheckSkillDescriptionLength: fail("0/1 skill-01"),
				rules.CheckSkillNoWrapperCommands: na,
				rules.CheckSkillSize:              {rules.Info, "4"},
			},
		},
		{
			name: "an unreadable skill fails every check that judges it",
			repo: skills(goodSkill("skill-01"), audit.ClaudeSkill{
				Path: "skills/skill-02/SKILL.md", Dir: "skill-02",
				Unreadable: "the frontmatter is not closed by a --- line",
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
			// What internal/claude records for a handler that omits type:
			// it is judged as the command it runs.
			"a non-executable script on a handler with no type",
			hooks(mutate(func(h *audit.ClaudeHook) { h.Type = ""; h.ScriptMode = "100644" })),
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
			// hook_plugin_root judges only plugin hooks, so a project
			// settings file it would never judge is not its to fail.
			"an unreadable project settings file is skipped by the plugin-root check",
			hooks(audit.ClaudeHook{Source: ".claude/settings.json", Unreadable: "not valid JSON: x"}),
			map[rules.Check]claudeWant{
				rules.CheckHookKnownEvents:       fail("unreadable .claude/settings.json"),
				rules.CheckHookCommandExecutable: fail("unreadable .claude/settings.json"),
				rules.CheckHookPluginRoot:        na,
				rules.CheckHookTimeout:           fail("unreadable .claude/settings.json"),
				rules.CheckHookMatcher:           fail("unreadable .claude/settings.json"),
			},
		},
		{
			"an unreadable project settings file beside a good plugin hook",
			hooks(goodHook(), audit.ClaudeHook{Source: ".claude/settings.json", Unreadable: "not valid JSON: x"}),
			map[rules.Check]claudeWant{
				rules.CheckHookKnownEvents:       fail("unreadable .claude/settings.json"),
				rules.CheckHookCommandExecutable: fail("unreadable .claude/settings.json"),
				rules.CheckHookPluginRoot:        pass("1/1"),
				rules.CheckHookTimeout:           fail("unreadable .claude/settings.json"),
				rules.CheckHookMatcher:           fail("unreadable .claude/settings.json"),
			},
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

// pluginChecks is every check the Plugin sub-table carries so far, in its
// column order.
var pluginChecks = []rules.Check{
	rules.CheckPluginManifestFields,
	rules.CheckPluginNoOwnMarketplace,
	rules.CheckPluginReleases,
	rules.CheckPluginVersionMatchesTag,
}

// allPluginFields is every field plugin_manifest_fields requires, as
// internal/claude records them.
func allPluginFields() []string {
	return []string{"$schema", "author", "description", "homepage", "license", "name", "repository", "version"}
}

func TestObserveClaude_plugin(t *testing.T) {
	t.Parallel()

	na := claudeWant{rules.NA, ""}
	pass := func(v string) claudeWant { return claudeWant{rules.Pass, v} }
	fail := func(v string) claudeWant { return claudeWant{rules.Fail, v} }
	plugin := func(tag string, mutate func(*audit.ClaudePlugin)) audit.Repo {
		p := audit.ClaudePlugin{Present: true, Name: "plugin-kit", Version: "1.2.0", Fields: allPluginFields()}
		if mutate != nil {
			mutate(&p)
		}
		r := withClaude(audit.Claude{Plugin: p})
		r.Releases.LatestTag = tag
		return r
	}
	row := func(fields, market, releases, match claudeWant) map[rules.Check]claudeWant {
		return map[rules.Check]claudeWant{
			rules.CheckPluginManifestFields:    fields,
			rules.CheckPluginNoOwnMarketplace:  market,
			rules.CheckPluginReleases:          releases,
			rules.CheckPluginVersionMatchesTag: match,
		}
	}
	const unreadable = "unreadable .claude-plugin/plugin.json"

	tests := []struct {
		name string
		repo audit.Repo
		want map[rules.Check]claudeWant
	}{
		{"no plugin.json is n/a throughout", withClaude(audit.Claude{}), row(na, na, na, na)},
		{
			"a complete plugin at its release passes",
			plugin("v1.2.0", nil),
			row(pass(""), pass(""), pass(""), pass("1.2.0")),
		},
		{
			"missing fields are listed in sorted order",
			plugin("v1.2.0", func(p *audit.ClaudePlugin) {
				p.Fields = []string{"$schema", "author", "description", "name", "repository", "version"}
			}),
			row(fail("homepage, license"), pass(""), pass(""), pass("1.2.0")),
		},
		{
			"the repository's own marketplace.json fails",
			plugin("v1.2.0", func(p *audit.ClaudePlugin) { p.OwnMarketplace = true }),
			row(pass(""), fail(""), pass(""), pass("1.2.0")),
		},
		{
			"no release fails releases and leaves the version match n/a",
			plugin("", nil),
			row(pass(""), pass(""), fail(""), na),
		},
		{
			"a version ahead of the release passes",
			plugin("v1.2.0", func(p *audit.ClaudePlugin) { p.Version = "1.3.0" }),
			row(pass(""), pass(""), pass(""), pass("1.3.0 ≥ v1.2.0")),
		},
		{
			"a version behind the release fails",
			plugin("v1.2.0", func(p *audit.ClaudePlugin) { p.Version = "1.1.0" }),
			row(pass(""), pass(""), pass(""), fail("1.1.0 < v1.2.0")),
		},
		{
			"a version that is not semver fails with the raw values",
			plugin("v1.2.0", func(p *audit.ClaudePlugin) { p.Version = "abc" }),
			row(pass(""), pass(""), pass(""), fail("abc ? v1.2.0")),
		},
		{
			"a missing version fails",
			plugin("v1.2.0", func(p *audit.ClaudePlugin) { p.Version = "" }),
			row(pass(""), pass(""), pass(""), fail("none ? v1.2.0")),
		},
		{
			"a tag that is not semver fails with the raw values",
			plugin("latest", nil),
			row(pass(""), pass(""), pass(""), fail("1.2.0 ? latest")),
		},
		{
			"a prerelease is behind its release",
			plugin("v1.2.0", func(p *audit.ClaudePlugin) { p.Version = "1.2.0-rc.1" }),
			row(pass(""), pass(""), pass(""), fail("1.2.0-rc.1 < v1.2.0")),
		},
		{
			"a release is ahead of its prerelease tag",
			plugin("v1.2.0-rc.1", nil),
			row(pass(""), pass(""), pass(""), pass("1.2.0 ≥ v1.2.0-rc.1")),
		},
		{
			"an unreadable manifest fails all four",
			plugin("v1.2.0", func(p *audit.ClaudePlugin) { *p = audit.ClaudePlugin{Present: true, Unreadable: "not valid JSON"} }),
			row(fail(unreadable), fail(unreadable), fail(unreadable), fail(unreadable)),
		},
		{
			"an unreadable manifest fails even with no release",
			plugin("", func(p *audit.ClaudePlugin) { *p = audit.ClaudePlugin{Present: true, Unreadable: "not valid JSON"} }),
			row(fail(unreadable), fail(unreadable), fail(unreadable), fail(unreadable)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rep := mustEvaluateOne(t, tt.repo)
			for _, c := range pluginChecks {
				got := rep.Repos[0].Cell(c)
				want := tt.want[c]
				if got.Verdict != want.verdict || got.Value != want.value {
					t.Errorf("%s = %v %q, want %v %q", c, got.Verdict, got.Value, want.verdict, want.value)
				}
				if got.Verdict == rules.Fail && want.value != "" && !got.Mark {
					t.Errorf("%s: a failing value should carry a mark", c)
				}
			}
		})
	}
}

func TestObserveClaude_pluginGaps(t *testing.T) {
	t.Parallel()

	r := withClaude(audit.Claude{Plugin: audit.ClaudePlugin{
		Present: true, Name: "plugin-kit", Version: "1.0.0", Fields: []string{"name"}, OwnMarketplace: true,
	}})
	r.Releases.LatestTag = "v1.1.0"
	rep := mustEvaluateOne(t, r)

	want := map[rules.Check]string{
		rules.CheckPluginManifestFields:    "Plugin manifest missing fields",
		rules.CheckPluginNoOwnMarketplace:  "Plugin repository has its own marketplace.json",
		rules.CheckPluginVersionMatchesTag: "Plugin version behind its latest release",
	}
	got := map[rules.Check]string{}
	for _, g := range rep.Gaps {
		if _, ok := want[g.Check]; ok {
			got[g.Check] = g.Label
		}
	}
	for c, label := range want {
		if got[c] != label {
			t.Errorf("gap %s label = %q, want %q", c, got[c], label)
		}
	}

	none := withClaude(audit.Claude{Plugin: audit.ClaudePlugin{Present: true, Fields: allPluginFields()}})
	for _, g := range mustEvaluateOne(t, none).Gaps {
		if g.Check == rules.CheckPluginReleases && g.Label != "Plugin has no release" {
			t.Errorf("plugin_releases gap label = %q", g.Label)
		}
	}
}

// TestObserveClaude_pluginSemver: plugin_version_matches_tag orders versions
// as Semantic Versioning 2.0.0 does, and shows what is not one with a `?`.
func TestObserveClaude_pluginSemver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		version, tag string
		want         rules.Verdict
	}{
		{"1.2.0", "v1.2.0", rules.Pass},
		{"1.2.0+build.5", "v1.2.0", rules.Pass},
		{"2.0.0", "v1.9.9", rules.Pass},
		{"1.10.0", "v1.9.0", rules.Pass},
		{"1.2.10", "v1.2.9", rules.Pass},
		{"0.9.0", "v1.0.0", rules.Fail},
		{"1.1.9", "v1.2.0", rules.Fail},
		{"1.2.0", "1.2.0", rules.Pass},
		// Prerelease ordering, from the semver.org precedence example.
		{"1.0.0-alpha.1", "v1.0.0-alpha", rules.Pass},
		{"1.0.0-alpha", "v1.0.0-alpha.1", rules.Fail},
		{"1.0.0-alpha.1", "v1.0.0-alpha.beta", rules.Fail},
		{"1.0.0-alpha.beta", "v1.0.0-alpha.1", rules.Pass},
		{"1.0.0-beta.2", "v1.0.0-beta.11", rules.Fail},
		{"1.0.0-beta.11", "v1.0.0-beta.2", rules.Pass},
		{"1.0.0-beta", "v1.0.0-alpha", rules.Pass},
		{"1.0.0-rc.1", "v1.0.0", rules.Fail},
		// Not versions.
		{"1.2", "v1.2.0", rules.Fail},
		{"1.2.0.0", "v1.2.0", rules.Fail},
		{"01.2.0", "v1.2.0", rules.Fail},
		{"1.2.x", "v1.2.0", rules.Fail},
		{"1.2.-1", "v1.2.0", rules.Fail},
		{"1.2.0-", "v1.2.0", rules.Fail},
		{"1.2.0-rc..1", "v1.2.0", rules.Fail},
		{"1.2.0-rc.01", "v1.2.0", rules.Fail},
		{"1.2.0-rc_1", "v1.2.0", rules.Fail},
		{"v1.2.0", "v1.2.0", rules.Fail},
		{"1.2.0", "vv1.2.0", rules.Fail},
		{"99999999999999999999.0.0", "v1.0.0", rules.Fail},
	}
	for _, tt := range tests {
		t.Run(tt.version+" vs "+tt.tag, func(t *testing.T) {
			t.Parallel()
			r := withClaude(audit.Claude{Plugin: audit.ClaudePlugin{Present: true, Version: tt.version, Fields: allPluginFields()}})
			r.Releases.LatestTag = tt.tag
			got := mustEvaluateOne(t, r).Repos[0].Cell(rules.CheckPluginVersionMatchesTag)
			if got.Verdict != tt.want {
				t.Errorf("verdict = %v (%q), want %v", got.Verdict, got.Value, tt.want)
			}
		})
	}
}
