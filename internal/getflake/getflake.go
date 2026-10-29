// Package getflake extracts the argument of every builtins.getFlake call from
// text. It does no I/O and judges nothing: the caller supplies the text, and
// whether an argument reads the working tree outside git is internal/rules'
// decision.
//
// The text is Nix, shell, YAML or Markdown that a repository happens to
// contain, so this is a line-oriented scan and not a Nix parser. An argument
// it cannot classify is left out; callers that must not miss a call count the
// occurrences of the name themselves.
package getflake

import "strings"

const callName = "builtins.getFlake"

// Args returns the raw argument of every builtins.getFlake call in text, in
// order. A call whose line has a `#` before it is a comment and is skipped.
//
// A string argument is returned with double quotes, normalized to `"…"`
// whether it is written `"…"` or, inside a shell string, `\"…\"`, so one
// spelling reaches the caller. `toString X` is returned as written, with X the
// next token. A path or a bare identifier is returned verbatim.
func Args(text string) []string {
	// CRLF and bare CR fold into LF, so a CRLF file's carriage return never
	// lands inside an argument.
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	args := make([]string, 0, strings.Count(text, callName))
	for line := range strings.SplitSeq(text, "\n") {
		args = append(args, lineArgs(line)...)
	}
	if len(args) == 0 {
		return nil
	}
	return args
}

func lineArgs(line string) []string {
	var args []string
	for from := 0; ; {
		i := strings.Index(line[from:], callName)
		if i < 0 {
			return args
		}
		start := from + i
		end := start + len(callName)
		from = end
		if strings.Contains(line[:start], "#") {
			return args
		}
		if end < len(line) && isIdent(line[end]) {
			continue
		}
		if arg, ok := argument(line[end:]); ok {
			args = append(args, arg)
		}
	}
}

// argument classifies what follows the call name: spaces, tabs and at most
// one opening parenthesis, then the argument.
func argument(rest string) (string, bool) {
	rest = skipBlanks(rest)
	if strings.HasPrefix(rest, "(") {
		rest = skipBlanks(rest[1:])
	}
	arg, ok := token(rest)
	if !ok {
		return "", false
	}
	if arg != "toString" {
		return arg, true
	}
	// toString applies to the next token; one that cannot be read leaves the
	// bare identifier, which the caller sees as an unknown argument.
	operand, ok := token(skipBlanks(strings.TrimPrefix(rest, "toString")))
	if !ok {
		return arg, true
	}
	return "toString " + operand, true
}

// token reads one string, parenthesized expression, path or identifier from
// the start of s.
func token(s string) (string, bool) {
	switch {
	case s == "":
		return "", false
	case strings.HasPrefix(s, `\"`):
		return quoted(s[2:], `\"`)
	case s[0] == '"':
		return quoted(s[1:], `"`)
	case s[0] == '(':
		return parenthesized(s)
	case isPath(s):
		return s[:pathLen(s)], true
	case isIdentStart(s[0]):
		return s[:identLen(s)], true
	}
	return "", false
}

// quoted reads a string body up to its closing delimiter on this line. A
// plain string skips backslash pairs so `\"` inside it does not end it; a
// shell-escaped string ends at the first `\"`. The result is always `"…"`.
func quoted(body, closer string) (string, bool) {
	for i := 0; i < len(body); i++ {
		if strings.HasPrefix(body[i:], closer) {
			return `"` + body[:i] + `"`, true
		}
		if body[i] == '\\' && closer == `"` {
			i++
		}
	}
	return "", false
}

// parenthesized reads s, which starts with `(`, through the matching `)`.
func parenthesized(s string) (string, bool) {
	depth := 0
	for i := range len(s) {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[:i+1], true
			}
		}
	}
	return "", false
}

func isPath(s string) bool {
	return strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") ||
		strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/")
}

// pathLen stops at the characters that end a path in the surrounding Nix or
// shell: blanks, closing parentheses, semicolons and quotes.
func pathLen(s string) int {
	n := strings.IndexAny(s, " \t\r);\"'`\\")
	if n < 0 {
		return len(s)
	}
	return n
}

func identLen(s string) int {
	n := 0
	for n < len(s) && isIdent(s[n]) {
		n++
	}
	return n
}

func skipBlanks(s string) string {
	return strings.TrimLeft(s, " \t")
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdent(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '\'' || c == '-'
}
