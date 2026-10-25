package claude

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
)

// Hook is one handler of one hook event.
type Hook struct {
	Source      string  // file the hook is declared in
	Plugin      bool    // declared by a plugin (hooks.json, plugin.json, or a plugin skill's frontmatter)
	Event       string  // hook event name as written
	Type        string  // "command", "http", "mcp_tool", "prompt", "agent", or as written; "" when absent
	Matcher     string  // "" when absent
	Timeout     float64 // 0 when unset
	TimeoutSet  bool
	Command     string
	Args        []string
	Script      string // repository-relative path the command runs, "" when it names none
	Interpreter bool   // Script is an argument to an interpreter
	PathForm    string // "plugin_root", "project_dir", "relative", "absolute", or "" (bare command on PATH)
	Unreadable  string
}

// Path forms a command's script can be written in.
const (
	FormPluginRoot = "plugin_root"
	FormProjectDir = "project_dir"
	FormRelative   = "relative"
	FormAbsolute   = "absolute"
)

// interpreters are the programs a hook command commonly hands a script to. A
// script run this way is read, not executed, so it needs no execute bit. The
// list is this tool's own judgement, not Claude Code's.
func interpreters() []string {
	return []string{
		"bash", "sh", "zsh", "dash", "python", "python3", "node", "deno", "bun",
		"ruby", "perl", "pwsh", "env",
	}
}

// ParseHooks reads the `{"hooks": {Event: [{matcher, hooks: [handler...]}]}}`
// shape that hooks.json and settings.json share. Text that is not JSON, or not
// that shape, is one unreadable Hook; a file with no `hooks` key declares none.
func ParseHooks(source, text string, plugin bool) []Hook {
	bad := func(reason string) []Hook {
		return []Hook{{Source: source, Plugin: plugin, Unreadable: reason}}
	}
	var doc any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return bad("not valid JSON: " + err.Error())
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return bad("the file is not a JSON object")
	}
	v, ok := obj["hooks"]
	if !ok {
		return nil
	}
	return ParseHooksValue(source, v, plugin)
}

// ParseHooksValue reads an already-decoded event map: a skill's frontmatter
// `hooks` or a plugin.json inline `hooks` object. Any shape error makes the
// whole value one unreadable Hook, because handlers read from half a value
// could pass checks the other half fails.
func ParseHooksValue(source string, v any, plugin bool) []Hook {
	if v == nil {
		return nil
	}
	bad := func(format string, args ...any) []Hook {
		return []Hook{{Source: source, Plugin: plugin, Unreadable: fmt.Sprintf(format, args...)}}
	}
	events, ok := v.(map[string]any)
	if !ok {
		return bad("hooks is not an object keyed by event name")
	}
	var out []Hook
	for _, event := range sortedMapKeys(events) {
		groups, ok := events[event].([]any)
		if !ok {
			return bad("hooks.%s is not a list", event)
		}
		for gi, g := range groups {
			group, ok := g.(map[string]any)
			if !ok {
				return bad("hooks.%s[%d] is not an object", event, gi)
			}
			matcher := ""
			if m, present := group["matcher"]; present {
				if matcher, ok = m.(string); !ok {
					return bad("hooks.%s[%d].matcher is not a string", event, gi)
				}
			}
			handlers, present := group["hooks"]
			if !present {
				continue
			}
			list, ok := handlers.([]any)
			if !ok {
				return bad("hooks.%s[%d].hooks is not a list", event, gi)
			}
			for hi, raw := range list {
				h, err := handler(source, plugin, event, matcher, raw)
				if err != "" {
					return bad("hooks.%s[%d].hooks[%d]: %s", event, gi, hi, err)
				}
				out = append(out, h)
			}
		}
	}
	return out
}

func handler(source string, plugin bool, event, matcher string, raw any) (Hook, string) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return Hook{}, "not an object"
	}
	h := Hook{Source: source, Plugin: plugin, Event: event, Matcher: matcher}
	if t, present := obj["type"]; present {
		if h.Type, ok = t.(string); !ok {
			return Hook{}, "type is not a string"
		}
	}
	if t, present := obj["timeout"]; present {
		switch n := t.(type) {
		case float64:
			h.Timeout = n
		case int:
			h.Timeout = float64(n)
		case int64:
			h.Timeout = float64(n)
		case uint64:
			h.Timeout = float64(n)
		default:
			return Hook{}, "timeout is not a number"
		}
		h.TimeoutSet = true
	}
	// A handler with no type is judged as a command: that is the handler a
	// hook naming a file is, and leaving its script unresolved would skip a
	// hook rather than judge it. Other types run no script, so whatever
	// command key they carry is not theirs to resolve.
	if h.Type != "" && h.Type != "command" {
		return h, ""
	}
	if c, present := obj["command"]; present {
		if h.Command, ok = c.(string); !ok {
			return Hook{}, "command is not a string"
		}
	}
	exec := false
	if a, present := obj["args"]; present {
		list, ok := a.([]any)
		if !ok {
			return Hook{}, "args is not a list"
		}
		exec = true
		h.Args = make([]string, 0, len(list))
		for _, item := range list {
			s, ok := item.(string)
			if !ok {
				return Hook{}, "args holds a non-string"
			}
			h.Args = append(h.Args, s)
		}
	}
	h.Script, h.Interpreter, h.PathForm = resolve(h.Command, h.Args, exec)
	return h, ""
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// resolve finds the repository file a command runs. The exec form (command
// plus args) takes the command as the program; the shell form splits the
// string into words the way a shell would, without expanding anything. Only
// the first word is judged as the program: a compound command such as
// `cd x && ./y.sh` names no script here, which skips it rather than passes it.
func resolve(command string, args []string, exec bool) (script string, interpreter bool, form string) {
	var words []string
	if exec {
		words = append([]string{command}, args...)
	} else {
		words = shellWords(command)
	}
	if len(words) == 0 {
		return "", false, ""
	}
	prog, rest := words[0], words[1:]
	if !isInterpreter(prog) {
		script, form = classify(prog, false)
		return script, false, form
	}
	// With env the program is the next word that is not a flag or VAR=value.
	if baseName(prog) == "env" {
		for len(rest) > 0 && (strings.HasPrefix(rest[0], "-") || isAssignment(rest[0])) {
			rest = rest[1:]
		}
		if len(rest) == 0 {
			return "", false, ""
		}
		prog, rest = rest[0], rest[1:]
		if !isInterpreter(prog) || baseName(prog) == "env" {
			script, form = classify(prog, false)
			return script, false, form
		}
	}
	return interpreterScript(baseName(prog), rest)
}

// interpreterScript finds the script among an interpreter's arguments. deno and
// bun take a `run` subcommand first. A flag that takes its value consumes the
// next word, a flag that takes code inline means there is no script file, and
// any other flag is skipped as a switch. A value-taking flag missing from this
// table makes the next word look like the script, which fails a check on a
// file that does not exist rather than passing one.
func interpreterScript(interp string, args []string) (script string, interpreter bool, form string) {
	if (interp == "deno" || interp == "bun") && len(args) > 0 && args[0] == "run" {
		args = args[1:]
	}
	valueFlags := map[string][]string{
		"python":  {"-W", "-X"},
		"python3": {"-W", "-X"},
		"node":    {"-r", "--require", "--import", "--loader"},
		"ruby":    {"-r", "-I"},
		"perl":    {"-I", "-M"},
	}[interp]
	inline := []string{"-c", "-e", "-E", "-p", "--eval", "--print", "--command"}
	if interp == "python" || interp == "python3" {
		inline = append(inline, "-m")
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case !strings.HasPrefix(a, "-"):
			script, form = classify(a, true)
			return script, script != "" || form != "", form
		case slices.Contains(inline, a):
			return "", false, ""
		case slices.Contains(valueFlags, a):
			i++
		}
	}
	return "", false, ""
}

func isInterpreter(word string) bool { return slices.Contains(interpreters(), baseName(word)) }

func baseName(word string) string { return path.Base(word) }

func isAssignment(word string) bool {
	i := strings.Index(word, "=")
	return i > 0 && !strings.ContainsAny(word[:i], "/$")
}

func placeholders() []struct{ prefix, form string } {
	return []struct{ prefix, form string }{
		{"${CLAUDE_PLUGIN_ROOT}", FormPluginRoot},
		{"$CLAUDE_PLUGIN_ROOT", FormPluginRoot},
		{"${CLAUDE_PROJECT_DIR}", FormProjectDir},
		{"$CLAUDE_PROJECT_DIR", FormProjectDir},
	}
}

// classify turns one word into a repository-relative script path and the form
// it was written in. A bare word is a program looked up on PATH, unless it is
// an interpreter's argument, which is a file relative to the working directory.
func classify(word string, isArg bool) (script, form string) {
	for _, p := range placeholders() {
		if rest, ok := strings.CutPrefix(word, p.prefix); ok {
			// An unbraced name is a prefix match only at a word boundary:
			// $CLAUDE_PLUGIN_ROOT_X is a different variable.
			if !strings.HasPrefix(p.prefix, "${") && rest != "" && !strings.HasPrefix(rest, "/") {
				continue
			}
			if rest != "" && !strings.HasPrefix(rest, "/") {
				return "", p.form
			}
			return repoPath(rest), p.form
		}
	}
	switch {
	case strings.HasPrefix(word, "/"), strings.HasPrefix(word, "~"), strings.Contains(word, "$"):
		return "", FormAbsolute
	case strings.Contains(word, "/"), isArg:
		return repoPath(word), FormRelative
	}
	return "", ""
}

// repoPath cleans p into a path inside the repository, or "" when it is empty
// or climbs out of the repository.
func repoPath(p string) string {
	p = strings.TrimLeft(p, "/")
	if p == "" {
		return ""
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return ""
	}
	return c
}

// shellWords splits a command into words with POSIX quoting: single quotes are
// literal, double quotes group (a backslash escapes `"` and `\` inside them),
// and a backslash outside quotes escapes the next character. Adjacent quoted
// and unquoted runs join, so `"${X}"/a.sh` is one word. Nothing is expanded.
func shellWords(s string) []string {
	var words []string
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\'':
			inWord = true
			for i++; i < len(rs) && rs[i] != '\''; i++ {
				cur.WriteRune(rs[i])
			}
		case c == '"':
			inWord = true
			for i++; i < len(rs) && rs[i] != '"'; i++ {
				if rs[i] == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\') {
					i++
				}
				cur.WriteRune(rs[i])
			}
		case c == '\\' && i+1 < len(rs):
			inWord = true
			i++
			cur.WriteRune(rs[i])
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		default:
			inWord = true
			cur.WriteRune(c)
		}
	}
	flush()
	return words
}
