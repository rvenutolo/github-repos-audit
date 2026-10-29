// Package getflake extracts the argument of every builtins.getFlake call from
// text. It does no I/O and judges nothing: the caller supplies the text, and
// whether an argument reads the working tree outside git is internal/rules'
// decision.
//
// The text is Nix, shell, YAML or Markdown that a repository happens to
// contain, so this is a line-oriented scan and not a Nix parser. A `#` starts
// a comment only at the start of a line or after a blank, so the `#` in a
// string or a flake reference (`"a#b"`, `.#foo`) does not hide a call. The
// argument is read from the name's own line, or, when only blanks and an
// optional `(` follow the name, from the next non-blank line. A string, a
// parenthesized expression that closes on its line, a path, an identifier that
// ends the line or comes before a closing bracket, `;`, `,` or quote, an
// indented string (opened by two single quotes, returned as written) and
// `toString` of one of those are extracted. Prose such as "`builtins.getFlake`
// cannot" or "builtins.getFlake is slow", an argument that is none of these
// and a double-quoted string that does not close on its line are left out,
// and nothing records that they were: a call that yields no argument is
// invisible to the caller. An indented string that does not close is still
// returned, up to the next blank, `)` or `;`.
package getflake

import "strings"

const callName = "builtins.getFlake"

// Args returns the raw argument of every builtins.getFlake call in text, in
// order. A call preceded on its line by a comment is skipped.
//
// A string argument is returned with double quotes, normalized to `"…"`
// whether it is written `"…"` or, inside a shell string, `\"…\"`, so one
// spelling reaches the caller. `toString X` is returned as written, with X the
// next token. A path, a bare identifier or an indented string is returned
// verbatim.
func Args(text string) []string {
	// CRLF and bare CR fold into LF, so a CRLF file's carriage return never
	// lands inside an argument.
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	lines := strings.Split(text, "\n")
	args := make([]string, 0, strings.Count(text, callName))
	for i := range lines {
		args = append(args, lineArgs(lines, i)...)
	}
	if len(args) == 0 {
		return nil
	}
	return args
}

func lineArgs(lines []string, n int) []string {
	line := lines[n]
	var args []string
	for from := 0; ; {
		i := strings.Index(line[from:], callName)
		if i < 0 {
			return args
		}
		start := from + i
		end := start + len(callName)
		from = end
		if startsComment(line[:start]) {
			return args
		}
		if end < len(line) && isIdent(line[end]) {
			continue
		}
		rest := line[end:]
		if isContinued(rest) {
			// A formatter breaks the line after the name or after its
			// opening parenthesis, so the argument is on the next line that
			// has any text.
			rest = nextText(lines[n+1:])
		}
		if arg, ok := argument(rest); ok {
			args = append(args, arg)
		}
	}
}

// startsComment reports whether prefix holds a comment-opening `#`: one at
// the start of the line or after a blank. A `#` glued to a word is part of a
// string or a flake reference.
func startsComment(prefix string) bool {
	for i := range len(prefix) {
		if prefix[i] == '#' && (i == 0 || prefix[i-1] == ' ' || prefix[i-1] == '\t') {
			return true
		}
	}
	return false
}

// isContinued reports whether nothing but blanks and at most one opening
// parenthesis follows the call name on its line.
func isContinued(rest string) bool {
	rest = skipBlanks(rest)
	rest = strings.TrimPrefix(rest, "(")
	return skipBlanks(rest) == ""
}

// nextText returns the first line with text, its leading blanks removed, or
// "" when none follows.
func nextText(lines []string) string {
	for _, l := range lines {
		if l = skipBlanks(l); l != "" {
			return l
		}
	}
	return ""
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
		if isIdentStart(arg[0]) && !endsArgument(rest[len(arg):]) {
			return "", false
		}
		return arg, true
	}
	// toString applies to the next token; one that cannot be read leaves the
	// bare identifier, which the caller sees as an unknown argument.
	after := skipBlanks(strings.TrimPrefix(rest, "toString"))
	operand, ok := token(after)
	if !ok {
		return arg, true
	}
	if isIdentStart(operand[0]) && !endsArgument(after[len(operand):]) {
		return "", false
	}
	return "toString " + operand, true
}

// endsArgument reports whether an identifier argument ends where Nix code
// would end it: at the end of the line or before a closing bracket, a `;`,
// a `,` or the quote that closes a shell expression. A word followed by
// another word is prose that happens to mention the call, as in
// `echo "builtins.getFlake is slow"`, and reading it as an argument would
// fail the check on a repository that calls nothing.
func endsArgument(after string) bool {
	after = skipBlanks(after)
	return after == "" || strings.ContainsRune(`)]};,"'`+"`\\", rune(after[0]))
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
	case strings.HasPrefix(s, "''"):
		return indented(s), true
	case s[0] == '(':
		return parenthesized(s)
	case isPath(s):
		return s[:pathLen(s)], true
	case isIdentStart(s[0]):
		return s[:identLen(s)], true
	}
	return "", false
}

// indented reads an indented string, which starts with two single quotes,
// through the closing pair on this line. The caller never classifies it as a
// flake reference, so an unterminated one is returned up to the next
// delimiter rather than dropped: a call must not vanish because its argument
// is odd.
func indented(s string) string {
	if j := strings.Index(s[2:], "''"); j >= 0 {
		return s[:j+4]
	}
	if n := strings.IndexAny(s, " \t);"); n > 0 {
		return s[:n]
	}
	return s
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
