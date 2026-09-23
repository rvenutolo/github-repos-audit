package github_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/github"
)

// presetDefault is the contents path the collector reads for a bare
// github>gh-owner/preset-store reference.
const presetDefault = "/repos/gh-owner/preset-store/contents/default.json"

// scriptRenovateProbe queues, for the next GraphQL request, mixedCase-flake's
// recorded answer with its renovate_json probe replaced by probe. Rewriting the
// recorded answer rather than adding a fixture keeps every other field real
// and needs no new repository name in the fake vocabulary.
func scriptRenovateProbe(t *testing.T, api *fakeAPI, probe map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureRoot, "repos", "mixedCase-flake", "graphql.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc struct {
		Data struct {
			Repository map[string]any `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if _, ok := doc.Data.Repository["renovate_json"]; !ok {
		t.Fatal("fixture has no renovate_json probe to replace")
	}
	doc.Data.Repository["renovate_json"] = probe
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	api.failNext("/graphql", scriptedFailure{status: http.StatusOK, body: string(body)})
}

// collectFlake collects mixedCase-flake alone and returns its Renovate facts.
func collectFlake(t *testing.T, api *fakeAPI) audit.Renovate {
	t.Helper()
	repos, err := newTestClient(t, api.url()).Collect(t.Context(), []string{"mixedCase-flake"})
	if err != nil {
		t.Fatalf("Collect() error = %v, want nil: an unusable config is a fact, not an abort", err)
	}
	return repos[0].Renovate
}

// TestCollect_presetFetchedOncePerRun: several repositories extending the
// same preset cost one request, however many collectors run at once.
func TestCollect_presetFetchedOncePerRun(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t)
	c := newTestClient(t, api.url())
	// mixedCase-flake and web-app both extend github>gh-owner/preset-store.
	if _, err := c.Collect(t.Context(), []string{"mixedCase-flake", "web-app"}); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if n := api.callsTo(presetDefault); n != 1 {
		t.Errorf("preset fetched %d times, want 1", n)
	}
}

// TestCollect_presetCacheIsPerRun: the cache lives for one Collect call, so a
// second run on the same Client reads the preset afresh rather than trusting
// an answer from an earlier run.
func TestCollect_presetCacheIsPerRun(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t)
	c := newTestClient(t, api.url())
	for range 2 {
		if _, err := c.Collect(t.Context(), []string{"mixedCase-flake"}); err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
	}
	if n := api.callsTo(presetDefault); n != 2 {
		t.Errorf("preset fetched %d times over two runs, want 2", n)
	}
}

// TestCollect_presetNotFoundIsAFactNotAnAbort: a 404 on the default file, then
// on its deprecated renovate.json fallback, is an ordinary answer.
func TestCollect_presetNotFoundIsAFactNotAnAbort(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t)
	api.failNext(presetDefault,
		scriptedFailure{status: http.StatusNotFound, body: `{"message":"Not Found"}`})
	got := collectFlake(t, api)
	if got.MinReleaseAge != "" || !strings.Contains(got.MinReleaseAgeError, "not found") {
		t.Errorf("Renovate = %+v, want an error mentioning not found", got)
	}
	if n := api.callsTo("/repos/gh-owner/preset-store/contents/renovate.json"); n != 1 {
		t.Errorf("renovate.json fallback fetched %d times, want 1", n)
	}
}

// TestCollect_presetFallbackIsRead: a preset repository with no default.json
// but a renovate.json is read from the latter, as Renovate reads it.
func TestCollect_presetFallbackIsRead(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t)
	api.failNext(presetDefault,
		scriptedFailure{status: http.StatusNotFound, body: `{"message":"Not Found"}`})
	// base64 of {"minimumReleaseAge": "5 days"}
	api.failNext("/repos/gh-owner/preset-store/contents/renovate.json", scriptedFailure{
		status: http.StatusOK,
		body:   `{"type":"file","encoding":"base64","content":"eyJtaW5pbXVtUmVsZWFzZUFnZSI6ICI1IGRheXMifQo=\n"}`,
	})
	want := audit.Renovate{MinReleaseAge: "5 days", MinReleaseAgeSource: "github>gh-owner/preset-store"}
	if diff := cmp.Diff(want, collectFlake(t, api)); diff != "" {
		t.Errorf("Renovate mismatch (-want +got):\n%s", diff)
	}
}

// TestCollect_presetServerErrorAborts: any other status is not an answer.
func TestCollect_presetServerErrorAborts(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t)
	api.failNext(presetDefault,
		scriptedFailure{status: http.StatusForbidden, body: `{"message":"Forbidden"}`})
	c := newTestClient(t, api.url())
	if _, err := c.Collect(t.Context(), []string{"mixedCase-flake"}); !errors.Is(err, github.ErrUnexpectedStatus) {
		t.Errorf("Collect() error = %v, want ErrUnexpectedStatus", err)
	}
}

// TestCollect_presetContentsAnswersThatAreNotAPreset: the contents API answers
// 200 for several things that are not a readable preset file. Each is recorded
// as unresolved, never read as an empty config and never an abort.
func TestCollect_presetContentsAnswersThatAreNotAPreset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "a directory", body: `[{"type":"file"}]`, want: "not a file"},
		{name: "a symlink", body: `{"type":"symlink"}`, want: "not a file"},
		{name: "a submodule", body: `{"type":"submodule"}`, want: "not a file"},
		{name: "a file over 1 MB", body: `{"type":"file","encoding":"none","content":""}`, want: "too large"},
		{name: "content that is not base64", body: `{"type":"file","encoding":"base64","content":"not base64!"}`, want: "base64"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			api := newFakeAPI(t)
			api.failNext(presetDefault, scriptedFailure{status: http.StatusOK, body: tc.body})
			got := collectFlake(t, api)
			if got.MinReleaseAge != "" || !strings.Contains(got.MinReleaseAgeError, tc.want) {
				t.Errorf("Renovate = %+v, want an error mentioning %q", got, tc.want)
			}
		})
	}
}

// TestCollect_ownConfigBlobThatCannotBeRead: GitHub answers text: null for a
// binary blob and truncates a large one. Neither is an empty config.
func TestCollect_ownConfigBlobThatCannotBeRead(t *testing.T) {
	t.Parallel()

	oid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa000a"
	tests := []struct {
		name  string
		probe map[string]any
		want  string
	}{
		{
			name:  "binary",
			probe: map[string]any{"oid": oid, "text": nil, "is_truncated": false, "is_binary": true},
			want:  "not a text file",
		},
		{
			name:  "text null without the binary flag",
			probe: map[string]any{"oid": oid, "text": nil, "is_truncated": false, "is_binary": false},
			want:  "not a text file",
		},
		{
			name: "truncated",
			probe: map[string]any{
				"oid": oid, "text": `{"minimumReleaseAge": "3 days"`, "is_truncated": true, "is_binary": false,
			},
			want: "too large",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			api := newFakeAPI(t)
			scriptRenovateProbe(t, api, tc.probe)
			got := collectFlake(t, api)
			if got.MinReleaseAge != "" || !strings.Contains(got.MinReleaseAgeError, tc.want) {
				t.Errorf("Renovate = %+v, want an error mentioning %q", got, tc.want)
			}
			if !strings.Contains(got.MinReleaseAgeError, "renovate.json") {
				t.Errorf("error %q does not name the config file", got.MinReleaseAgeError)
			}
			if n := api.callsTo(presetDefault); n != 0 {
				t.Errorf("preset fetched %d times for an unreadable config, want 0", n)
			}
		})
	}
}

// TestCollect_namedPresetIsRead: a :name preset is read from name.json.
func TestCollect_namedPresetIsRead(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t)
	scriptRenovateProbe(t, api, map[string]any{
		"oid":          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa000a",
		"text":         `{"extends": ["github>gh-owner/preset-store:go"]}`,
		"is_truncated": false,
		"is_binary":    false,
	})
	want := audit.Renovate{MinReleaseAge: "3 days", MinReleaseAgeSource: "github>gh-owner/preset-store:go"}
	if diff := cmp.Diff(want, collectFlake(t, api)); diff != "" {
		t.Errorf("Renovate mismatch (-want +got):\n%s", diff)
	}
}

// TestCollect_presetPathAndRefAreEscaped: a preset path and ref come from a
// repository's own file, so each path segment and the ref are escaped rather
// than concatenated into the URL, while the path's slashes stay separators.
func TestCollect_presetPathAndRefAreEscaped(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t)
	scriptRenovateProbe(t, api, map[string]any{
		"oid":          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa000a",
		"text":         `{"extends":["github>gh-owner/preset-store//dir with space/go#v1.0"]}`,
		"is_truncated": false,
		"is_binary":    false,
	})
	collectFlake(t, api)

	const wantPath = "/repos/gh-owner/preset-store/contents/dir%20with%20space/go.json"
	var got []call
	for _, c := range api.seen() {
		if strings.Contains(c.path, "/contents/") {
			got = append(got, c)
		}
	}
	if len(got) != 1 {
		t.Fatalf("contents requests = %+v, want exactly one", got)
	}
	if got[0].method != http.MethodGet || got[0].escapedPath != wantPath || got[0].rawQuery != "ref=v1.0" {
		t.Errorf("request = %s %s?%s, want GET %s?ref=v1.0",
			got[0].method, got[0].escapedPath, got[0].rawQuery, wantPath)
	}
}
