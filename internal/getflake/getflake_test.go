package getflake_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/getflake"
)

func TestArgs_forms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want []string
	}{
		{"toString of identifier in parens", `builtins.getFlake (toString REPO_PLACEHOLDER);`, []string{"toString REPO_PLACEHOLDER"}},
		{"toString of path", `builtins.getFlake (toString ./.)`, []string{"toString ./."}},
		{"toString of parenthesized expression", `builtins.getFlake (toString (a + b))`, []string{"toString (a + b)"}},
		{"toString of nested parentheses", `builtins.getFlake toString (f (g x)) tail`, []string{"toString (f (g x))"}},
		{"toString of string", `builtins.getFlake (toString "x/y")`, []string{`toString "x/y"`}},
		{"toString of unterminated parenthesis", `builtins.getFlake (toString (a + b`, []string{"toString"}},
		{"CRLF line", "builtins.getFlake ./x\r\n", []string{"./x"}},
		{"toString of nothing", `builtins.getFlake (toString)`, []string{"toString"}},
		{"string", `builtins.getFlake "git+file://REPO_PLACEHOLDER";`, []string{`"git+file://REPO_PLACEHOLDER"`}},
		{"string in parens", `builtins.getFlake ("git+file://x")`, []string{`"git+file://x"`}},
		{"string with escaped quote", `builtins.getFlake "a\"b" rest`, []string{`"a\"b"`}},
		{"unterminated string", `builtins.getFlake "git+file://x`, nil},
		{"relative path", `builtins.getFlake ./.`, []string{"./."}},
		{"parent path", `builtins.getFlake ../x;`, []string{"../x"}},
		{"home path", `builtins.getFlake ~/x)`, []string{"~/x"}},
		{"absolute path", `builtins.getFlake /abs/x`, []string{"/abs/x"}},
		{"identifier", `builtins.getFlake src`, []string{"src"}},
		{"identifier with quote and dash", `builtins.getFlake my-src'`, []string{"my-src'"}},
		{"interpolated string", `builtins.getFlake "${REPO_DIR}"`, []string{`"${REPO_DIR}"`}},
		{"tab separated", "builtins.getFlake\t./.", []string{"./."}},
		{"two calls in order", "builtins.getFlake ./.\nbuiltins.getFlake src\n", []string{"./.", "src"}},
		{"two calls on one line", `builtins.getFlake ./. + builtins.getFlake src`, []string{"./.", "src"}},
		{"no call", `nothing here`, nil},
		{"parenthesis then argument on next lines", "flake = builtins.getFlake (\n  toString ./.\n);", []string{"toString ./."}},
		{"argument on next line", "builtins.getFlake\n  ./.", []string{"./."}},
		{"argument after blank lines", "builtins.getFlake\n\n  ./.", []string{"./."}},
		{"string argument on next line", "builtins.getFlake (\n  \"git+file://x\"\n)", []string{`"git+file://x"`}},
		{"indented string", "builtins.getFlake ''${x}''", []string{"''${x}''"}},
		{"unterminated indented string", "builtins.getFlake ''${x}", []string{"''${x}"}},
		{"unterminated indented string before delimiter", "builtins.getFlake (''${x});", []string{"''${x}"}},
		{"hash inside string before call", `x = "a#b"; y = builtins.getFlake ./.;`, []string{"./."}},
		{"nix eval expression", `nix eval .#foo --expr 'builtins.getFlake ./.'`, []string{"./."}},
		{"identifier on next line before semicolon", "builtins.getFlake\n  src;", []string{"src"}},
		{"identifier on next line before parenthesis", "(builtins.getFlake\n  src).outputs", []string{"src"}},
		{"identifier at end of shell expression", `--expr "builtins.getFlake src"`, []string{"src"}},
		{"identifier before comma", `[ (builtins.getFlake a), b ]`, []string{"a"}},
		{"toString of identifier before closing parenthesis", "builtins.getFlake (\n  toString x\n)", []string{"toString x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, getflake.Args(tt.text)); diff != "" {
				t.Errorf("Args() (-want +got):\n%s", diff)
			}
		})
	}
}

func TestArgs_shellEscapedString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want []string
	}{
		{"backslash-escaped quotes", `--expr "builtins.getFlake \"$DIR\""`, []string{`"$DIR"`}},
		{"single-quoted expression", `'builtins.getFlake "'"$DIR"'"'`, []string{`"'"`}},
		{"unterminated escaped string", `"builtins.getFlake \"$DIR`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, getflake.Args(tt.text)); diff != "" {
				t.Errorf("Args() (-want +got):\n%s", diff)
			}
		})
	}
}

func TestArgs_ignoresComments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want []string
	}{
		{"whole-line comment", `# builtins.getFlake (toString x)`, nil},
		{"trailing comment", `foo # see builtins.getFlake ./.`, nil},
		{"same call without comment", `foo builtins.getFlake ./.`, []string{"./."}},
		{"comment on another line", "# note\nbuiltins.getFlake ./.", []string{"./."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, getflake.Args(tt.text)); diff != "" {
				t.Errorf("Args() (-want +got):\n%s", diff)
			}
		})
	}
}

func TestArgs_ignoresNonArgument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
	}{
		{"mentioned in prose", "`builtins.getFlake` cannot reach"},
		{"followed by semicolon", `builtins.getFlake;`},
		{"longer name", `builtins.getFlakeX ./.`},
		{"nothing after", `builtins.getFlake`},
		{"only parenthesis", `builtins.getFlake (`},
		{"prose punctuation after name", "`builtins.getFlake` cannot reach"},
		{"comment on next line", "builtins.getFlake\n# ./."},
		{"nothing on next line", "builtins.getFlake (\n"},
		{"unsupported token", `builtins.getFlake {}`},
		{"prose word on next line", "echo \"never call builtins.getFlake\nwith a path\""},
		{"prose word on same line", `echo "builtins.getFlake is slow"`},
		{"prose word after parenthesis", "see builtins.getFlake (\nthe manual)"},
		{"prose word after toString", `echo "builtins.getFlake toString some path"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := getflake.Args(tt.text); len(got) != 0 {
				t.Errorf("Args() = %q, want none", got)
			}
		})
	}
}
