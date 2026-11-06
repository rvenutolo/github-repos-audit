package github

import (
	"path"
	"regexp"
	"strings"
)

// devshellExemptMarker is the comment text that opts a script out of the
// devShell self-wrap, followed by the reason it must run without Nix.
const devshellExemptMarker = "devshell-exempt:"

// devshellTestLine matches a line that tests IN_DEVSHELL: a `[[`, `[` or
// `test` expression that names it. The name must stand alone, so
// MY_IN_DEVSHELL is not counted, and must not be followed by `=`, so an
// assignment inside a conditional line is not read as a test. Lines with no
// test word, such as `export IN_DEVSHELL=1` or a bare KEEP_VARS array entry,
// do not match: a wrapper can set the variable or list it without ever
// reading it.
var devshellTestLine = regexp.MustCompile(`(\[\[|\[|(^|[^A-Za-z0-9_-])test\s).*(^|[^A-Za-z0-9_])IN_DEVSHELL([^A-Za-z0-9_=]|$)`)

// devshellExecLine matches a line that starts with `exec`. Only an exec
// replaces the script with the wrapper; a plain call to the wrapper returns
// to a script that has already run unwrapped.
var devshellExecLine = regexp.MustCompile(`^exec\s`)

// wrapperWaitLine matches a `wait` as the first word of a line, or after
// `||`, `&&` or `;`. The fixed wrapper's `wait "${child_pid}" || rc="$?"`
// starts with it. Requiring a command position keeps `echo waiting` and
// `await` out.
var wrapperWaitLine = regexp.MustCompile(`(^|\|\||&&|;)\s*wait(\s|$)`)

// wrapperMktempLine matches a mktemp that makes a directory, spelled
// `--directory` or as `d` among short flags. A bare `--tmpdir` option has no
// single-dash form, so it is not mistaken for `-d`.
var wrapperMktempLine = regexp.MustCompile(`(^|[^A-Za-z0-9_-])mktemp\b.*(--directory|\s-[A-Za-z]*d[A-Za-z]*(\s|\)|"|$))`)

// wrapperKeepTmpdirLine matches a line that is only TMPDIR, optionally
// quoted (a KEEP_VARS entry), or that passes `--keep TMPDIR`. An export or a
// `${TMPDIR:-/tmp}` default is not a keep: the wrapper can set TMPDIR for
// itself while `nix develop` still replaces it.
var wrapperKeepTmpdirLine = regexp.MustCompile(`^(TMPDIR|"TMPDIR"|'TMPDIR')$|--keep\s+TMPDIR(\s|$)`)

// wrapperExitTrapLine matches a trap whose last word is EXIT, the cleanup
// trap. `trap - INT QUIT` resets and `trap x INT` handles a signal; neither
// runs when the wrapper exits normally.
var wrapperExitTrapLine = regexp.MustCompile(`^trap\s.*\sEXIT$`)

// shellShebang reports whether line 1 of text is a shebang whose interpreter
// is sh or bash by base name: directly, through env, or through `env -S`,
// with flags allowed. Only line 1 is read, so a shebang later in the file
// does not count, and the base name is compared whole, so `bashful` does not.
func shellShebang(text string) bool {
	first, _, _ := strings.Cut(text, "\n")
	rest, ok := strings.CutPrefix(strings.TrimSpace(first), "#!")
	if !ok {
		return false
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return false
	}
	interpreter := path.Base(fields[0])
	if interpreter == "env" {
		interpreter = ""
		for _, f := range fields[1:] {
			// env's own flags (-S, -i) and NAME=value settings come before
			// the program it runs.
			if strings.HasPrefix(f, "-") || strings.Contains(f, "=") {
				continue
			}
			interpreter = path.Base(f)
			break
		}
	}
	return interpreter == "sh" || interpreter == "bash"
}

// codeLines returns the lines of text trimmed of surrounding whitespace
// (carriage returns included), without blank lines and without lines whose
// first character is `#`. That includes the shebang on line 1. A trailing
// comment stays on its line: the tests that read these lines look at what a
// line does, and a comment after code does not change it.
func codeLines(text string) []string {
	var out []string
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// exemptReasons returns, for each comment line containing the devshell-exempt
// marker, the trimmed text after it. The reason may be empty, which the
// caller treats as an exemption with no reason. A marker on a code line is
// not an exemption, so an `echo` of the word cannot opt a script out.
func exemptReasons(text string) []string {
	var out []string
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		if _, reason, found := strings.Cut(line, devshellExemptMarker); found {
			out = append(out, strings.TrimSpace(reason))
		}
	}
	return out
}

// scriptLines returns the code lines of text that make up a devShell
// self-wrap: lines that test IN_DEVSHELL, and lines that start with `exec`
// and name in-devshell. Comments are ignored, so prose that names either
// cannot satisfy the check. It decides nothing; a script self-wraps when
// both results are non-empty, which the rules package judges.
func scriptLines(text string) (inDevshellTest, execInDevshell []string) {
	for _, line := range codeLines(text) {
		if devshellTestLine.MatchString(line) {
			inDevshellTest = append(inDevshellTest, line)
		}
		if devshellExecLine.MatchString(line) && strings.Contains(line, "in-devshell") {
			execInDevshell = append(execInDevshell, line)
		}
	}
	return inDevshellTest, execInDevshell
}

// wrapperLines returns the code lines of text matching each of the four
// markers of a wrapper that cleans up its temp directory: a `wait` for the
// child, a mktemp of a directory, TMPDIR kept, and an EXIT trap. Comments are
// ignored: the reference wrapper's header discusses all four, and a leaking
// wrapper's header would otherwise satisfy them. It decides nothing.
func wrapperLines(text string) (wait, mktemp, keepTmpdir, exitTrap []string) {
	for _, line := range codeLines(text) {
		if wrapperWaitLine.MatchString(line) {
			wait = append(wait, line)
		}
		if wrapperMktempLine.MatchString(line) {
			mktemp = append(mktemp, line)
		}
		if wrapperKeepTmpdirLine.MatchString(line) {
			keepTmpdir = append(keepTmpdir, line)
		}
		if wrapperExitTrapLine.MatchString(line) {
			exitTrap = append(exitTrap, line)
		}
	}
	return wait, mktemp, keepTmpdir, exitTrap
}
