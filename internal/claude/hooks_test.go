package claude_test

import (
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/claude"
)

func wrap(handler string) string {
	return `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[` + handler + `]}]}}`
}

func TestParseHooks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		handler     string
		script      string
		form        string
		interpreter bool
	}{
		{"quoted plugin root", `{"type":"command","command":"\"${CLAUDE_PLUGIN_ROOT}\"/hooks/x.sh"}`, "hooks/x.sh", "plugin_root", false},
		{"plugin root bare variable", `{"type":"command","command":"$CLAUDE_PLUGIN_ROOT/hooks/x.sh"}`, "hooks/x.sh", "plugin_root", false},
		{"interpreter", `{"type":"command","command":"bash ${CLAUDE_PLUGIN_ROOT}/hooks/x.sh"}`, "hooks/x.sh", "plugin_root", true},
		{"interpreter with a flag first", `{"type":"command","command":"bash -eu ${CLAUDE_PLUGIN_ROOT}/hooks/x.sh"}`, "hooks/x.sh", "plugin_root", true},
		{"interpreter by absolute path", `{"type":"command","command":"/usr/bin/python3 ./x.py"}`, "x.py", "relative", true},
		{"env then interpreter", `{"type":"command","command":"env -i FOO=1 python3 ./x.py"}`, "x.py", "relative", true},
		{"env then program", `{"type":"command","command":"env ./x.sh"}`, "x.sh", "relative", false},
		{"env alone", `{"type":"command","command":"env"}`, "", "", false},
		{"deno run subcommand", `{"type":"command","command":"deno run --allow-read ${CLAUDE_PLUGIN_ROOT}/x.ts"}`, "x.ts", "plugin_root", true},
		{"bun run subcommand", `{"type":"command","command":"bun run x.ts"}`, "x.ts", "relative", true},
		{"python value flag", `{"type":"command","command":"python3 -W ignore ./x.py"}`, "x.py", "relative", true},
		{"python -X value flag", `{"type":"command","command":"python3 -X dev ./x.py"}`, "x.py", "relative", true},
		{"node require flag", `{"type":"command","command":"node -r tsx ./x.js"}`, "x.js", "relative", true},
		{"node import flag", `{"type":"command","command":"node --import tsx ./x.js"}`, "x.js", "relative", true},
		{"ruby include flag", `{"type":"command","command":"ruby -I lib ./x.rb"}`, "x.rb", "relative", true},
		{"perl module flag", `{"type":"command","command":"perl -M Foo ./x.pl"}`, "x.pl", "relative", true},
		{"python module is no script", `{"type":"command","command":"python3 -m pkg"}`, "", "", false},
		{"-m outside python is a switch", `{"type":"command","command":"bash -m ./x.sh"}`, "x.sh", "relative", true},
		{"another interpreter's value flag is a switch", `{"type":"command","command":"bash -W ./x.sh"}`, "x.sh", "relative", true},
		{"node eval is no script", `{"type":"command","command":"node -e 'x()'"}`, "", "", false},
		{"unbraced variable that merely starts with the placeholder", `{"type":"command","command":"$CLAUDE_PLUGIN_ROOT_X/y.sh"}`, "", "absolute", false},
		{"unbraced project variable that merely starts with the placeholder", `{"type":"command","command":"$CLAUDE_PROJECT_DIRS/y.sh"}`, "", "absolute", false},
		{"inline code is no script", `{"type":"command","command":"bash -c 'echo hi'"}`, "", "", false},
		{"interpreter alone", `{"type":"command","command":"python3"}`, "", "", false},
		{"interpreter with only flags", `{"type":"command","command":"python3 --version"}`, "", "", false},
		{"interpreter bare script", `{"type":"command","command":"python3 x.py"}`, "x.py", "relative", true},
		{"exec form interpreter", `{"type":"command","command":"python3","args":["${CLAUDE_PLUGIN_ROOT}/x.py"]}`, "x.py", "plugin_root", true},
		{"exec form program", `{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/x.sh","args":["--flag"]}`, "x.sh", "plugin_root", false},
		{"project dir", `{"type":"command","command":"\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/y.sh"}`, ".claude/hooks/y.sh", "project_dir", false},
		{"project dir braces", `{"type":"command","command":"${CLAUDE_PROJECT_DIR}/.claude/hooks/y.sh"}`, ".claude/hooks/y.sh", "project_dir", false},
		{"relative", `{"type":"command","command":"./hooks/z.sh"}`, "hooks/z.sh", "relative", false},
		{"relative without dot", `{"type":"command","command":"hooks/z.sh"}`, "hooks/z.sh", "relative", false},
		{"relative climbing out", `{"type":"command","command":"../z.sh"}`, "", "relative", false},
		{"absolute", `{"type":"command","command":"/usr/local/bin/z"}`, "", "absolute", false},
		{"home", `{"type":"command","command":"~/bin/z"}`, "", "absolute", false},
		{"unknown variable", `{"type":"command","command":"$HOME/z.sh"}`, "", "absolute", false},
		{"placeholder alone", `{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}"}`, "", "plugin_root", false},
		{"placeholder prefix of another variable", `{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}x/y"}`, "", "plugin_root", false},
		{"bare command", `{"type":"command","command":"jq ."}`, "", "", false},
		{"empty command", `{"type":"command","command":""}`, "", "", false},
		{"no command key", `{"type":"command"}`, "", "", false},
		{"single and escaped quotes", `{"type":"command","command":"'./a b.sh' c\\ d"}`, "a b.sh", "relative", false},
		{"escaped quote inside double quotes", `{"type":"command","command":"\"./a\\\"b.sh\""}`, `a"b.sh`, "relative", false},
		{"unterminated quote takes the rest", `{"type":"command","command":"\"./a b.sh"}`, "a b.sh", "relative", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hooks := claude.ParseHooks("hooks/hooks.json", wrap(tc.handler), true)
			if len(hooks) != 1 {
				t.Fatalf("hooks = %+v", hooks)
			}
			h := hooks[0]
			if h.Unreadable != "" {
				t.Fatalf("unreadable: %s", h.Unreadable)
			}
			if h.Script != tc.script || h.PathForm != tc.form || h.Interpreter != tc.interpreter {
				t.Errorf("Script/PathForm/Interpreter = %q/%q/%v, want %q/%q/%v",
					h.Script, h.PathForm, h.Interpreter, tc.script, tc.form, tc.interpreter)
			}
			if h.Event != "PreToolUse" || h.Matcher != "Bash" || h.Source != "hooks/hooks.json" || !h.Plugin {
				t.Errorf("hook = %+v", h)
			}
		})
	}
}

func TestParseHooks_fields(t *testing.T) {
	t.Parallel()

	t.Run("http handler", func(t *testing.T) {
		t.Parallel()
		h := claude.ParseHooks("s", wrap(`{"type":"http","url":"https://x.invalid","command":"./ignored.sh"}`), false)[0]
		if h.Type != "http" || h.Script != "" || h.Command != "" {
			t.Errorf("hook = %+v", h)
		}
	})
	t.Run("a handler with no type resolves its script", func(t *testing.T) {
		t.Parallel()
		h := claude.ParseHooks("s", wrap(`{"command":"${CLAUDE_PLUGIN_ROOT}/hooks/x.sh"}`), true)[0]
		if h.Type != "" || h.Command == "" || h.Script != "hooks/x.sh" || h.PathForm != "plugin_root" || h.Interpreter {
			t.Errorf("hook = %+v", h)
		}
	})
	t.Run("a handler with no type checks its command's shape", func(t *testing.T) {
		t.Parallel()
		h := claude.ParseHooks("s", wrap(`{"command":1}`), true)[0]
		if h.Unreadable == "" {
			t.Errorf("hook = %+v, want unreadable", h)
		}
	})
	t.Run("timeout set", func(t *testing.T) {
		t.Parallel()
		h := claude.ParseHooks("s", wrap(`{"type":"command","command":"x","timeout":5}`), false)[0]
		if !h.TimeoutSet || h.Timeout != 5 {
			t.Errorf("hook = %+v", h)
		}
	})
	t.Run("timeout zero is still set", func(t *testing.T) {
		t.Parallel()
		h := claude.ParseHooks("s", wrap(`{"type":"command","command":"x","timeout":0}`), false)[0]
		if !h.TimeoutSet || h.Timeout != 0 {
			t.Errorf("hook = %+v", h)
		}
	})
	t.Run("timeout unset", func(t *testing.T) {
		t.Parallel()
		h := claude.ParseHooks("s", wrap(`{"type":"command","command":"x"}`), false)[0]
		if h.TimeoutSet || h.Plugin {
			t.Errorf("hook = %+v", h)
		}
	})
	t.Run("matcher absent and type as written", func(t *testing.T) {
		t.Parallel()
		h := claude.ParseHooks("s", `{"hooks":{"Stop":[{"hooks":[{"type":"widget"}]}]}}`, false)[0]
		if h.Matcher != "" || h.Type != "widget" || h.Event != "Stop" {
			t.Errorf("hook = %+v", h)
		}
	})
	t.Run("args are recorded", func(t *testing.T) {
		t.Parallel()
		h := claude.ParseHooks("s", wrap(`{"type":"command","command":"python3","args":["a","b"]}`), false)[0]
		if len(h.Args) != 2 || h.Command != "python3" {
			t.Errorf("hook = %+v", h)
		}
	})
	t.Run("order is by event then position", func(t *testing.T) {
		t.Parallel()
		hs := claude.ParseHooks("s", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"b"}]}],`+
			`"PreToolUse":[{"hooks":[{"type":"command","command":"a1"},{"type":"command","command":"a2"}]}]}}`, false)
		got := make([]string, 0, len(hs))
		for _, h := range hs {
			got = append(got, h.Command)
		}
		if len(got) != 3 || got[0] != "a1" || got[1] != "a2" || got[2] != "b" {
			t.Errorf("order = %v", got)
		}
	})
	t.Run("a group without handlers declares none", func(t *testing.T) {
		t.Parallel()
		if hs := claude.ParseHooks("s", `{"hooks":{"Stop":[{"matcher":"x"}]}}`, false); len(hs) != 0 {
			t.Errorf("hooks = %+v", hs)
		}
	})
}

func TestParseHooks_none(t *testing.T) {
	t.Parallel()
	for _, text := range []string{`{}`, `{"permissions":{}}`, `{"hooks":null}`, `{"hooks":{}}`} {
		if got := claude.ParseHooks("s", text, false); got != nil {
			t.Errorf("ParseHooks(%s) = %+v, want nil", text, got)
		}
	}
}

func TestParseHooks_unreadable(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"invalid JSON":            `{`,
		"not an object":           `[]`,
		"hooks not an object":     `{"hooks":[]}`,
		"event not a list":        `{"hooks":{"Stop":{}}}`,
		"group not an object":     `{"hooks":{"Stop":[1]}}`,
		"matcher not a string":    `{"hooks":{"Stop":[{"matcher":1,"hooks":[]}]}}`,
		"handlers not a list":     `{"hooks":{"Stop":[{"hooks":{}}]}}`,
		"handler not an object":   `{"hooks":{"Stop":[{"hooks":[1]}]}}`,
		"type not a string":       `{"hooks":{"Stop":[{"hooks":[{"type":1}]}]}}`,
		"timeout not a number":    `{"hooks":{"Stop":[{"hooks":[{"type":"command","timeout":"5"}]}]}}`,
		"command not a string":    `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":1}]}]}}`,
		"args not a list":         `{"hooks":{"Stop":[{"hooks":[{"type":"command","args":"x"}]}]}}`,
		"args holds a non-string": `{"hooks":{"Stop":[{"hooks":[{"type":"command","args":[1]}]}]}}`,
	}
	for name, text := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			hs := claude.ParseHooks("f.json", text, true)
			if len(hs) != 1 || hs[0].Unreadable == "" || hs[0].Source != "f.json" || !hs[0].Plugin || hs[0].Event != "" {
				t.Errorf("hooks = %+v", hs)
			}
		})
	}
}

func TestParseHooks_goodHandlerBesideBadOne(t *testing.T) {
	t.Parallel()
	hs := claude.ParseHooks("f", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"a"},1]}]}}`, false)
	if len(hs) != 1 || hs[0].Unreadable == "" {
		t.Errorf("a partial result escaped: %+v", hs)
	}
}

func TestParseHooksValue(t *testing.T) {
	t.Parallel()
	if got := claude.ParseHooksValue("s", nil, false); got != nil {
		t.Errorf("nil value = %+v", got)
	}
	hs := claude.ParseHooksValue("s", map[string]any{"Stop": []any{map[string]any{"hooks": []any{
		map[string]any{"type": "command", "command": "x", "timeout": int64(3)},
		map[string]any{"type": "command", "command": "y", "timeout": uint64(4)},
		map[string]any{"type": "command", "command": "z", "timeout": 5},
	}}}}, true)
	if len(hs) != 3 || hs[0].Timeout != 3 || hs[1].Timeout != 4 || hs[2].Timeout != 5 {
		t.Errorf("hooks = %+v", hs)
	}
}
