package github

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestShellShebang: only a first-line shebang whose interpreter, directly or
// through env, has the base name sh or bash makes a shell script.
func TestShellShebang(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		text string
		want bool
	}{
		{"bash", "#!/bin/bash\necho hi\n", true},
		{"sh", "#!/bin/sh\n", true},
		{"env bash", "#!/usr/bin/env bash\n", true},
		{"env sh", "#!/usr/bin/env sh\n", true},
		{"env -S", "#!/usr/bin/env -S bash -e\n", true},
		{"env -u operand is not the interpreter", "#!/usr/bin/env -u bash python3\n", false},
		{"env --unset operand is not the interpreter", "#!/usr/bin/env --unset sh python3\n", false},
		{"env -u then bash", "#!/usr/bin/env -u NAME bash\n", true},
		{"env --unset then sh", "#!/usr/bin/env --unset NAME sh\n", true},
		{"env -C then bash", "#!/usr/bin/env -C /tmp bash\n", true},
		{"env --chdir operand is not the interpreter", "#!/usr/bin/env --chdir sh python3\n", false},
		{"flags", "#!/bin/sh -eu\n", true},
		{"local bash", "#!/usr/local/bin/bash\n", true},
		{"store bash", "#!/nix/store/abc-bash-5.2/bin/bash\n", true},
		{"crlf", "#!/bin/bash\r\necho hi\r\n", true},
		{"space after bang", "#! /bin/bash\n", true},
		{"no newline", "#!/bin/bash", true},
		{"trailing space", "#!/bin/bash  \n", true},
		{"python", "#!/usr/bin/env python3\n", false},
		{"zsh", "#!/usr/bin/env zsh\n", false},
		{"bashful", "#!/bin/bashful\n", false},
		{"no shebang", "echo hi\n", false},
		{"empty", "", false},
		{"shebang on line 2", "# comment\n#!/bin/bash\n", false},
		{"bare env", "#!/usr/bin/env\n", false},
		{"bare bang", "#!\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := shellShebang(tc.text); got != tc.want {
				t.Errorf("shellShebang(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// TestCodeLines: blank lines and comments, indented or not, are dropped, a
// shebang is dropped as line 1, and a trailing comment stays on its line.
func TestCodeLines(t *testing.T) {
	t.Parallel()

	text := "#!/bin/bash\r\n" +
		"set -e # strict\r\n" +
		"\r\n" +
		"   \t\n" +
		"# comment\n" +
		"   # indented comment\n" +
		"  echo hi  \n"
	want := []string{"set -e # strict", "echo hi"}
	if diff := cmp.Diff(want, codeLines(text)); diff != "" {
		t.Errorf("codeLines() mismatch (-want +got):\n%s", diff)
	}
	if got := codeLines(""); len(got) != 0 {
		t.Errorf("codeLines(\"\") = %q, want none", got)
	}
}

// TestExemptReasons: only comment lines carry the marker, and the reason may
// be empty.
func TestExemptReasons(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		text string
		want []string
	}{
		{"reason", "# devshell-exempt: runs without Nix\n", []string{"runs without Nix"}},
		{"empty reason", "# devshell-exempt:\n", []string{""}},
		{"blank reason", "# devshell-exempt:   \r\n", []string{""}},
		{"indented", "  #devshell-exempt: a\n", []string{"a"}},
		{"code line", "echo devshell-exempt: x\n", nil},
		{"none", "echo hi\n", nil},
		{"two", "# devshell-exempt: a\necho\n# devshell-exempt: b\n", []string{"a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, exemptReasons(tc.text)); diff != "" {
				t.Errorf("exemptReasons() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// selfWrapScript is the self-wrap shape: a test of IN_DEVSHELL and an exec of
// the wrapper.
const selfWrapScript = `#!/usr/bin/env bash
set -Eeuo pipefail
# IN_DEVSHELL is set by the wrapper
if [[ -z "${IN_DEVSHELL:-}" ]]; then
  exec "${REPO_DIR}/.ci/in-devshell" "${REPO_DIR}/run-all-checks" "$@"
fi
`

// TestScriptLines: the self-wrap lines are the non-comment test of
// IN_DEVSHELL and the non-comment exec naming the wrapper.
func TestScriptLines(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		text     string
		wantTest []string
		wantExec []string
	}{
		{
			"self-wrap",
			selfWrapScript,
			[]string{`if [[ -z "${IN_DEVSHELL:-}" ]]; then`},
			[]string{`exec "${REPO_DIR}/.ci/in-devshell" "${REPO_DIR}/run-all-checks" "$@"`},
		},
		{"no if block", "#!/bin/bash\nset -e\necho hi\n", nil, nil},
		{"export only", "#!/bin/bash\nexport IN_DEVSHELL=1\n", nil, nil},
		{"assignment only", "IN_DEVSHELL=1 ./run\n", nil, nil},
		{"array entry", "KEEP_VARS=(\n  IN_DEVSHELL\n  HOME\n)\n", nil, nil},
		{"commented exec", "# exec .ci/in-devshell\n", nil, nil},
		{"call without exec", "\"$REPO/.ci/in-devshell\" x\n", nil, nil},
		{
			"single bracket and test",
			"[ -n \"$IN_DEVSHELL\" ] || x\ntest -z \"$IN_DEVSHELL\" && y\n",
			[]string{`[ -n "$IN_DEVSHELL" ] || x`, `test -z "$IN_DEVSHELL" && y`},
			nil,
		},
		{"longer name", "[[ -z \"$MY_IN_DEVSHELL\" ]]\n", nil, nil},
		{
			"exec after and",
			"[[ -z \"${IN_DEVSHELL:-}\" ]] && exec ./.ci/in-devshell \"$0\" \"$@\"\n",
			[]string{`[[ -z "${IN_DEVSHELL:-}" ]] && exec ./.ci/in-devshell "$0" "$@"`},
			[]string{`[[ -z "${IN_DEVSHELL:-}" ]] && exec ./.ci/in-devshell "$0" "$@"`},
		},
		{
			"exec after then on one line",
			"if [[ -z \"${IN_DEVSHELL:-}\" ]]; then exec ./.ci/in-devshell \"$0\" \"$@\"; fi\n",
			[]string{`if [[ -z "${IN_DEVSHELL:-}" ]]; then exec ./.ci/in-devshell "$0" "$@"; fi`},
			[]string{`if [[ -z "${IN_DEVSHELL:-}" ]]; then exec ./.ci/in-devshell "$0" "$@"; fi`},
		},
		{"exec after or", "false || exec ./.ci/in-devshell x\n", nil, []string{"false || exec ./.ci/in-devshell x"}},
		{"exec after semicolon", "true; exec ./.ci/in-devshell x\n", nil, []string{"true; exec ./.ci/in-devshell x"}},
		{"exec after else", "else exec ./.ci/in-devshell x\n", nil, []string{"else exec ./.ci/in-devshell x"}},
		{"exec after do", "do exec ./.ci/in-devshell x\n", nil, []string{"do exec ./.ci/in-devshell x"}},
		{"exec as an argument is not a command", "echo exec ./.ci/in-devshell\n", nil, nil},
		{"then exec at line start", "then exec ./.ci/in-devshell x\n", nil, []string{"then exec ./.ci/in-devshell x"}},
		{
			"do exec after semicolon",
			"for x in y; do exec ./.ci/in-devshell x; done\n",
			nil,
			[]string{"for x in y; do exec ./.ci/in-devshell x; done"},
		},
		{
			"else exec after semicolon",
			"if a; then b; else exec ./.ci/in-devshell x; fi\n",
			nil,
			[]string{"if a; then b; else exec ./.ci/in-devshell x; fi"},
		},
		{"exec with a quoted separator in its arguments", "exec ./.ci/in-devshell 'a; b'\n", nil, []string{"exec ./.ci/in-devshell 'a; b'"}},
		{"then as an argument is not a command position", "echo then exec ./.ci/in-devshell\n", nil, nil},
		{"do as an argument is not a command position", "echo do exec ./.ci/in-devshell\n", nil, nil},
		{"else as an argument is not a command position", "echo else exec ./.ci/in-devshell\n", nil, nil},
		{"semicolon in a single-quoted string", "log 'a; exec in-devshell'\n", nil, nil},
		{"and in a double-quoted string", "log \"a && exec in-devshell\"\n", nil, nil},
		{"then in a double-quoted string", "log \"then exec in-devshell\"\n", nil, nil},
		{"escaped quote does not end a string", "log \"a \\\" ; exec in-devshell\"\n", nil, nil},
		{"execute is not exec", "execute ./.ci/in-devshell\n", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotTest, gotExec := scriptLines(tc.text)
			if diff := cmp.Diff(tc.wantTest, gotTest); diff != "" {
				t.Errorf("test lines mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantExec, gotExec); diff != "" {
				t.Errorf("exec lines mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestUnquoted: quoted text is removed with its quotes kept, and a backslash
// escapes a quote outside single quotes only.
func TestUnquoted(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"echo plain", "echo plain"},
		{"log 'a; b' x", "log '' x"},
		{`log "a && b" x`, `log "" x`},
		{`log "a \" b" x`, `log "" x`},
		{`log 'a \' x`, `log '' x`},
		{`echo \' ; x`, `echo \' ; x`},
		{"log 'unterminated; x", "log '"},
		{`log "it's" x`, `log "" x`},
	} {
		if got := unquoted(tc.in); got != tc.want {
			t.Errorf("unquoted(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// leakingWrapper is the wrapper shape that leaks its temp directory: prose
// about the markers in the header, a KEEP_VARS list without TMPDIR, and a
// top-level exec of nix.
const leakingWrapper = `#!/usr/bin/env bash
# Runs a command inside the dev shell.
#
# The shell creates a temp dir with mktemp -d and must wait for the child,
# then run a trap ... EXIT to remove it; keep TMPDIR so it survives.
set -Eeuo pipefail

KEEP_VARS=(
  HOME
  IN_DEVSHELL
)

if [[ -n "${IN_DEVSHELL:-}" ]]; then
  exec "$@"
fi

keep_args=()
for v in "${KEEP_VARS[@]}"; do
  keep_args+=(--keep "$v")
done

exec nix "${nix_args[@]}" "$@"
`

// fixedWrapper is the wrapper shape that cleans up: TMPDIR kept, a temp dir
// made with mktemp, an EXIT trap, and nix run in a waited-for child.
const fixedWrapper = `#!/usr/bin/env bash
set -Eeuo pipefail

KEEP_VARS=(
  HOME
  IN_DEVSHELL
  TMPDIR
)

if [[ -n "${IN_DEVSHELL:-}" ]]; then
  exec "$@"
fi

remove_tmp_dir() { rm --recursive --force "${tmp_dir}"; }
trap remove_tmp_dir EXIT
tmp_dir="$(mktemp --directory --tmpdir="${base}" in-devshell.XXXXXX)"
export TMPDIR="${tmp_dir}"

(
  trap - INT QUIT
  exec nix "${nix_args[@]}" "$@"
) <&0 &
child_pid="$!"
rc=0
wait "${child_pid}" || rc="$?"
exit "${rc}"
`

// TestWrapperLines_shapes: the leaking shape matches no marker, and the fixed
// shape matches each marker exactly once.
func TestWrapperLines_shapes(t *testing.T) {
	t.Parallel()

	w, m, k, e := wrapperLines(leakingWrapper)
	for name, got := range map[string][]string{"wait": w, "mktemp": m, "keepTmpdir": k, "exitTrap": e} {
		if len(got) != 0 {
			t.Errorf("leaking wrapper %s = %q, want none", name, got)
		}
	}

	w, m, k, e = wrapperLines(fixedWrapper)
	if diff := cmp.Diff([]string{`wait "${child_pid}" || rc="$?"`}, w); diff != "" {
		t.Errorf("fixed wait mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{`tmp_dir="$(mktemp --directory --tmpdir="${base}" in-devshell.XXXXXX)"`}, m); diff != "" {
		t.Errorf("fixed mktemp mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"TMPDIR"}, k); diff != "" {
		t.Errorf("fixed keepTmpdir mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"trap remove_tmp_dir EXIT"}, e); diff != "" {
		t.Errorf("fixed exitTrap mismatch (-want +got):\n%s", diff)
	}
}

// TestWrapperLines_markerForms: each marker's accepted and rejected spellings.
func TestWrapperLines_markerForms(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		text   string
		marker string
		want   int
	}{
		{"wait first word", "wait\n", "wait", 1},
		{"wait after semicolon", "sleep 1; wait \"$p\"\n", "wait", 1},
		{"wait after and", "true && wait \"$p\"\n", "wait", 1},
		{"wait after or", "false || wait \"$p\"\n", "wait", 1},
		{"await is not wait", "await x\n", "wait", 0},
		{"wait in a word", "echo waiting\n", "wait", 0},
		{"mktemp -d", "d=$(mktemp -d)\n", "mktemp", 1},
		{"mktemp combined short flags", "d=$(mktemp -dt x.XXXX)\n", "mktemp", 1},
		{"mktemp file", "f=$(mktemp)\n", "mktemp", 0},
		{"mktemp tmpdir only", "f=$(mktemp --tmpdir=/x)\n", "mktemp", 0},
		{"keep bare", "TMPDIR\n", "keepTmpdir", 1},
		{"keep double quoted", "\"TMPDIR\"\n", "keepTmpdir", 1},
		{"keep single quoted", "'TMPDIR'\n", "keepTmpdir", 1},
		{"keep argument", "nix develop --keep TMPDIR x\n", "keepTmpdir", 1},
		{"export TMPDIR", "export TMPDIR=\"${tmp_dir}\"\n", "keepTmpdir", 0},
		{"default TMPDIR", "base=\"${TMPDIR:-/tmp}\"\n", "keepTmpdir", 0},
		{"trap EXIT", "trap cleanup EXIT\n", "exitTrap", 1},
		{"trap quoted EXIT", "trap 'rm -rf x' EXIT\n", "exitTrap", 1},
		{"trap INT", "trap 'x' INT\n", "exitTrap", 0},
		{"trap reset", "trap - INT QUIT\n", "exitTrap", 0},
		{"trap EXIT with trailing comment", "trap cleanup EXIT # remove tmp\n", "exitTrap", 1},
		{"trap EXIT with more signals", "trap cleanup EXIT INT TERM\n", "exitTrap", 1},
		{"trap EXIT with semicolon", "trap cleanup EXIT;\n", "exitTrap", 1},
		{"trap EXIT in the middle", "trap cleanup INT EXIT TERM\n", "exitTrap", 1},
		{"trap reset EXIT", "trap - EXIT\n", "exitTrap", 0},
		{"trap empty single-quoted handler", "trap '' EXIT\n", "exitTrap", 0},
		{"trap empty double-quoted handler", "trap \"\" EXIT\n", "exitTrap", 0},
		{"trap end of options then empty handler", "trap -- '' EXIT\n", "exitTrap", 0},
		{"trap end of options then empty double-quoted handler", "trap -- \"\" EXIT\n", "exitTrap", 0},
		{"trap end of options then reset", "trap -- - EXIT\n", "exitTrap", 0},
		{"trap end of options then handler", "trap -- cleanup EXIT\n", "exitTrap", 1},
		{"trap EXITING is not EXIT", "trap cleanup EXITING\n", "exitTrap", 0},
		{"keep bare with trailing comment", "TMPDIR # keep nix's tmp\n", "keepTmpdir", 1},
		{"keep quoted with trailing comment", "\"TMPDIR\" # keep\n", "keepTmpdir", 1},
		{"keep argument at line end", "nix develop --keep TMPDIR\n", "keepTmpdir", 1},
		{"TMPDIR_X is not TMPDIR", "TMPDIR_X\n", "keepTmpdir", 0},
		{"commented", "# wait x\n# trap y EXIT\n# mktemp -d\n# TMPDIR\n", "wait", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w, m, k, e := wrapperLines(tc.text)
			got := map[string][]string{"wait": w, "mktemp": m, "keepTmpdir": k, "exitTrap": e}[tc.marker]
			if len(got) != tc.want {
				t.Errorf("wrapperLines(%q) %s = %q, want %d line(s)", tc.text, tc.marker, got, tc.want)
			}
		})
	}
}
