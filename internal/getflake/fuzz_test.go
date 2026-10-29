package getflake_test

import (
	"strings"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/getflake"
)

// FuzzArgs holds the invariants every input must keep, since the text is
// whatever a repository happens to contain: Args does not panic, and every
// argument it returns is non-empty and has no line break.
func FuzzArgs(f *testing.F) {
	for _, seed := range []string{
		"", "builtins.getFlake", "builtins.getFlake (", "builtins.getFlake \"",
		"builtins.getFlake (toString (a (b", "builtins.getFlake (toString REPO_PLACEHOLDER);",
		`--expr "builtins.getFlake \"$DIR\""`, `'builtins.getFlake "'"$DIR"'"'`,
		"# builtins.getFlake ./.", "builtins.getFlake ./.\nbuiltins.getFlake src",
		`builtins.getFlake "a\"`, `builtins.getFlake \"`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		for _, arg := range getflake.Args(text) {
			if arg == "" || strings.ContainsAny(arg, "\r\n") {
				t.Errorf("Args() returned %q: want a non-empty single-line argument", arg)
			}
		}
	})
}
