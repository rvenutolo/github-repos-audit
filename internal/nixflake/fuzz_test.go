package nixflake_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/nixflake"
)

// FuzzParseLock holds the invariants every input must keep, since the text is
// whatever a repository happens to contain: ParseLock does not panic or
// recurse without end, and a lock it reports a problem with yields no facts.
func FuzzParseLock(f *testing.F) {
	for _, seed := range []string{
		"", "{}", "[", `{"nodes": {}}`, `{"nodes": {"root": {}}, "root": "root"}`,
		`{"nodes": {"root": {"inputs": {"nixpkgs": "n"}}, "n": {"locked": {"lastModified": 1}, "original": {"type": "github", "owner": "O", "repo": "r", "rev": "x"}}}, "root": "root"}`,
		`{"nodes": {"root": {"inputs": {"nixpkgs": ["a", "nixpkgs"], "a": "a"}}, "a": {"inputs": {"nixpkgs": ["a", "nixpkgs"]}}}}`,
		`{"nodes": {"root": {"inputs": {"nixpkgs": ["root", "nixpkgs"]}}}}`,
		`{"nodes": {"root": {"inputs": {"nixpkgs": 1}}}}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		lock, problem := nixflake.ParseLock(text)
		if problem == "" {
			return
		}
		if diff := cmp.Diff(nixflake.LockFacts{}, lock); diff != "" {
			t.Errorf("ParseLock() with problem %q carries facts (-want +got):\n%s", problem, diff)
		}
	})
}
