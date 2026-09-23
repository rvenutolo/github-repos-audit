package renovate_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/renovate"
)

// files is a fake preset store keyed by "repo/path@ref". A missing key is a 404.
type files map[string]string

func (f files) fetch(_ context.Context, p renovate.Preset) (string, string, error) {
	text, ok := f[p.Repo+"/"+p.Path+"@"+p.Ref]
	if !ok {
		return "", renovate.ProblemNotFound, nil
	}
	return text, "", nil
}

func resolve(t *testing.T, root string, store files) renovate.Result {
	t.Helper()
	res, err := renovate.Resolve(context.Background(), "gh-owner", "renovate.json", root, store.fetch)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	return res
}

func TestResolve(t *testing.T) {
	t.Parallel()

	store := files{
		"preset-store/default.json@":  `{"minimumReleaseAge": "7 days"}`,
		"preset-store/three.json@":    `{"minimumReleaseAge": "3 days"}`,
		"preset-store/nulls.json@":    `{"minimumReleaseAge": null}`,
		"preset-store/chain.json@":    `{"extends": ["github>gh-owner/preset-store:three"]}`,
		"preset-store/tagged.json@v1": `{"minimumReleaseAge": "14 days"}`,
		"preset-store/json5.json@":    "{\n  // a comment\n  minimumReleaseAge: '5 days',\n}",
		"other/renovate.json@":        `{"minimumReleaseAge": "2 days"}`, // default.json fallback
		"other/dir/renovate.json@":    `{"minimumReleaseAge": "4 days"}`, // dir/default.json fallback
	}
	tests := []struct {
		name string
		root string
		want renovate.Result
	}{
		{
			"own value", `{"minimumReleaseAge": "7 days"}`,
			renovate.Result{MinReleaseAge: "7 days", Source: "renovate.json"},
		},
		{"unset", `{"extends": ["config:recommended"]}`, renovate.Result{}},
		{"empty object", `{}`, renovate.Result{}},
		{
			"inherited from an account preset", `{"extends": ["github>gh-owner/preset-store"]}`,
			renovate.Result{MinReleaseAge: "7 days", Source: "github>gh-owner/preset-store"},
		},
		{
			"later extends wins", `{"extends": ["github>gh-owner/preset-store", "github>gh-owner/preset-store:three"]}`,
			renovate.Result{MinReleaseAge: "3 days", Source: "github>gh-owner/preset-store:three"},
		},
		{
			"own keys beat presets", `{"extends": ["github>gh-owner/preset-store"], "minimumReleaseAge": "1 day"}`,
			renovate.Result{MinReleaseAge: "1 day", Source: "renovate.json"},
		},
		{
			"null clears an earlier value", `{"extends": ["github>gh-owner/preset-store", "github>gh-owner/preset-store:nulls"]}`,
			renovate.Result{},
		},
		{
			"own null clears a preset", `{"extends": ["github>gh-owner/preset-store"], "minimumReleaseAge": null}`,
			renovate.Result{},
		},
		{
			"nested presets", `{"extends": ["github>gh-owner/preset-store:chain"]}`,
			renovate.Result{MinReleaseAge: "3 days", Source: "github>gh-owner/preset-store:three"},
		},
		{
			"tagged preset", `{"extends": ["github>gh-owner/preset-store:tagged#v1"]}`,
			renovate.Result{MinReleaseAge: "14 days", Source: "github>gh-owner/preset-store:tagged#v1"},
		},
		{
			"json5 preset", `{"extends": ["github>gh-owner/preset-store:json5"]}`,
			renovate.Result{MinReleaseAge: "5 days", Source: "github>gh-owner/preset-store:json5"},
		},
		{
			"default.json falls back to renovate.json", `{"extends": ["github>gh-owner/other"]}`,
			renovate.Result{MinReleaseAge: "2 days", Source: "github>gh-owner/other"},
		},
		{
			"explicit :default falls back to renovate.json", `{"extends": ["github>gh-owner/other:default"]}`,
			renovate.Result{MinReleaseAge: "2 days", Source: "github>gh-owner/other:default"},
		},
		{
			"//dir/default falls back to dir/renovate.json", `{"extends": ["github>gh-owner/other//dir/default"]}`,
			renovate.Result{MinReleaseAge: "4 days", Source: "github>gh-owner/other//dir/default"},
		},
		{
			"other owners and built-ins contribute nothing", `{"extends": ["github>someone-else/x", "config:best-practices"]}`,
			renovate.Result{},
		},
		{
			"root JSONC with comments", "{\n  // why\n  \"minimumReleaseAge\": \"7 days\" /* block */\n}",
			renovate.Result{MinReleaseAge: "7 days", Source: "renovate.json"},
		},
		{
			"root JSON5", "{minimumReleaseAge: '7 days', extends: [],}",
			renovate.Result{MinReleaseAge: "7 days", Source: "renovate.json"},
		},
		{
			"stabilityDays migrates to days", `{"stabilityDays": 5}`,
			renovate.Result{MinReleaseAge: "5 days", Source: "renovate.json"},
		},
		{
			"stabilityDays 1 is singular", `{"stabilityDays": 1}`,
			renovate.Result{MinReleaseAge: "1 day", Source: "renovate.json"},
		},
		{
			"stabilityDays 0 is unset", `{"extends": ["github>gh-owner/preset-store"], "stabilityDays": 0}`,
			renovate.Result{},
		},
		{
			"stabilityDays migrates past an explicit null", `{"minimumReleaseAge": null, "stabilityDays": 3}`,
			renovate.Result{MinReleaseAge: "3 days", Source: "renovate.json"},
		},
		{
			"minimumReleaseAge beats stabilityDays in one layer", `{"stabilityDays": 5, "minimumReleaseAge": "2 days"}`,
			renovate.Result{MinReleaseAge: "2 days", Source: "renovate.json"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resolve(t, tt.root, store); got != tt.want {
				t.Errorf("Resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolve_unresolved(t *testing.T) {
	t.Parallel()

	store := files{
		"preset-store/loop-a.json@": `{"extends": ["github>gh-owner/preset-store:loop-b"]}`,
		"preset-store/loop-b.json@": `{"extends": ["github>gh-owner/preset-store:loop-a"]}`,
		"preset-store/broken.json@": `{"minimumReleaseAge": `,
	}
	// A chain eleven presets deep: depth 10 is the limit.
	for i := range 11 {
		store[fmt.Sprintf("preset-store/d%d.json@", i)] = fmt.Sprintf(`{"extends": ["github>gh-owner/preset-store:d%d"]}`, i+1)
	}
	store["preset-store/d11.json@"] = `{"minimumReleaseAge": "7 days"}`

	tests := []struct {
		name     string
		root     string
		contains string
	}{
		{"root does not parse", `{"minimumReleaseAge": `, "renovate.json"},
		{"root is not an object", `["a"]`, "renovate.json"},
		{"root is null", `null`, "not an object"},
		{"value is not a string", `{"minimumReleaseAge": 7}`, "minimumReleaseAge"},
		{"value is a template", `{"minimumReleaseAge": "{{arg0}}"}`, "{{"},
		{"extends is not an array", `{"extends": "github>gh-owner/preset-store"}`, "extends"},
		{"extends entry is not a string", `{"extends": [7]}`, "extends"},
		{"stabilityDays is not a whole number", `{"stabilityDays": 1.5}`, "stabilityDays"},
		{"preset not found", `{"extends": ["github>gh-owner/missing"]}`, "not found"},
		{"preset does not parse", `{"extends": ["github>gh-owner/preset-store:broken"]}`, "preset-store:broken"},
		{"cycle", `{"extends": ["github>gh-owner/preset-store:loop-a"]}`, "cycle"},
		{"too deep", `{"extends": ["github>gh-owner/preset-store:d0"]}`, "deeper than 10"},
		{"nested sub-preset", `{"extends": ["github>gh-owner/preset-store:go/sub"]}`, "preset-store:go/sub"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolve(t, tt.root, store)
			if got.Unresolved == "" || !strings.Contains(got.Unresolved, tt.contains) {
				t.Errorf("Resolve().Unresolved = %q, want it to mention %q", got.Unresolved, tt.contains)
			}
			if got.MinReleaseAge != "" || got.Source != "" {
				t.Errorf("Resolve() = %+v, want only Unresolved set", got)
			}
		})
	}
}

func TestResolve_problemFromFetchIsUnresolved(t *testing.T) {
	t.Parallel()

	fetch := func(context.Context, renovate.Preset) (string, string, error) { return "", "not a file", nil }
	res, err := renovate.Resolve(context.Background(), "gh-owner", "renovate.json",
		`{"extends": ["github>gh-owner/preset-store:go"]}`, fetch)
	if err != nil || !strings.Contains(res.Unresolved, "not a file") {
		t.Errorf("Resolve() = %+v, %v; want Unresolved mentioning %q and no error", res, err, "not a file")
	}
}

func TestResolve_fetchErrorAborts(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	fetch := func(context.Context, renovate.Preset) (string, string, error) { return "", "", boom }
	_, err := renovate.Resolve(context.Background(), "gh-owner", "renovate.json",
		`{"extends": ["github>gh-owner/preset-store"]}`, fetch)
	if !errors.Is(err, boom) {
		t.Errorf("Resolve() error = %v, want it to wrap %v", err, boom)
	}
}

// TestResolve_skippedPresetsAreNeverFetched: a built-in or another owner's
// preset must not cost a request.
func TestResolve_skippedPresetsAreNeverFetched(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var fetched []renovate.Preset
	fetch := func(_ context.Context, p renovate.Preset) (string, string, error) {
		mu.Lock()
		defer mu.Unlock()
		fetched = append(fetched, p)
		return "", renovate.ProblemNotFound, nil
	}
	_, err := renovate.Resolve(context.Background(), "gh-owner", "renovate.json",
		`{"extends": ["config:best-practices", "github>someone-else/x", "npm>y"]}`, fetch)
	if err != nil || len(fetched) != 0 {
		t.Errorf("Resolve() fetched %v, err %v; want no fetches and no error", fetched, err)
	}
}
