package renovate_test

import (
	"context"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/renovate"
)

// FuzzResolve holds the one invariant every input must keep: Resolve never
// panics, and an unresolved result carries nothing else.
func FuzzResolve(f *testing.F) {
	for _, seed := range []string{
		`{}`, `{"minimumReleaseAge": "7 days"}`, `{"extends": ["github>gh-owner/x"]}`,
		`{minimumReleaseAge: '3 days',}`, `{"stabilityDays": 2}`, `[`,
	} {
		f.Add(seed)
	}
	fetch := func(context.Context, renovate.Preset) (string, string, error) {
		return "", renovate.ProblemNotFound, nil
	}
	f.Fuzz(func(t *testing.T, root string) {
		res, err := renovate.Resolve(context.Background(), "gh-owner", "renovate.json", root, fetch)
		if err != nil {
			t.Fatalf("Resolve() error = %v; a fetch that never errors must not produce one", err)
		}
		if res.Unresolved != "" && (res.MinReleaseAge != "" || res.Source != "") {
			t.Errorf("Resolve() = %+v, want nothing but Unresolved", res)
		}
	})
}
