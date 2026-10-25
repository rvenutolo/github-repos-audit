package github_test

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

const (
	marketplacePath = "/repos/gh-owner/plugin-market/contents/.claude-plugin/marketplace.json"
	kitRelease      = "/repos/gh-owner/plugin-kit/releases/tags/v1.2.0"
	kitRef          = "/repos/gh-owner/plugin-kit/git/ref/tags/v1.2.0"
	kitTagObject    = "/repos/gh-owner/plugin-kit/git/tags/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0001"
	kitManifest     = "/repos/gh-owner/plugin-kit/contents/.claude-plugin/plugin.json"
	kitTag          = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0001"
	kitCommit       = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0002"
	packCommit      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0003"
)

// marketAudited is the audited set the marketplace tests run against: the
// marketplace itself and the two plugins it lists that the account has.
func marketAudited() []audit.Repo {
	return []audit.Repo{{Name: "plugin-market"}, {Name: "plugin-kit"}, {Name: "skill-pack"}}
}

// contentsBody is a contents-API answer for a file holding text.
func contentsBody(text string) string {
	return `{"type": "file", "encoding": "base64", "content": "` +
		base64.StdEncoding.EncodeToString([]byte(text)) + `"}`
}

// TestClient_CollectMarketplace reads the recorded marketplace: plugin-kit is
// pinned to a release whose annotated tag resolves to the pinned commit, and
// whose manifest at that commit names it; skill-pack is pinned to a tag the
// repository has neither as a release nor as a ref, at a commit with no
// manifest, and carries a version; web-app is not audited; hook-guard has a
// relative-path source. None of that is an error.
func TestClient_CollectMarketplace(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t)

	got, err := newTestClient(t, api.url()).CollectMarketplace(t.Context(), "plugin-market", marketAudited())
	if err != nil {
		t.Fatalf("CollectMarketplace() error = %v, want nil", err)
	}
	want := audit.ClaudeMarketplace{
		Repo: "plugin-market",
		Entries: []audit.MarketplaceEntry{
			{
				Name: "plugin-kit", Repo: "gh-owner/plugin-kit", Ref: "v1.2.0", SHA: kitCommit,
				ReleaseTag: true, TagCommit: kitCommit, PluginName: "plugin-kit",
			},
			{
				Name: "skill-pack", Repo: "gh-owner/skill-pack", Ref: "v0.1.0", SHA: packCommit, HasVersion: true,
				Problem: "no .claude-plugin/plugin.json at " + packCommit,
			},
			{Name: "web-app", Repo: "gh-owner/web-app", Ref: "v1.0.0", Problem: "not an audited repository"},
			{Name: "hook-guard", Problem: "not an audited repository"},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("CollectMarketplace() mismatch (-want +got):\n%s", diff)
	}

	for _, c := range api.seen() {
		if c.method != http.MethodGet {
			t.Errorf("%s %s: the marketplace is read with GETs only", c.method, c.path)
		}
		if c.path == kitManifest && c.rawQuery != "ref="+kitCommit {
			t.Errorf("manifest read with query %q, want ref=%s: the name is the one at the pinned commit", c.rawQuery, kitCommit)
		}
		if strings.Contains(c.path, "/web-app/") {
			t.Errorf("GET %s: an entry outside the audited set costs no request", c.path)
		}
	}
	for _, path := range []string{kitRelease, kitRef, kitTagObject, kitManifest} {
		if api.callsTo(path) != 1 {
			t.Errorf("GET %s made %d times, want once", path, api.callsTo(path))
		}
	}
}

// TestClient_CollectMarketplace_lightweightTag: a ref that points straight at
// a commit is that commit, with no tag object to read.
func TestClient_CollectMarketplace_lightweightTag(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t)
	api.failNext(kitRef, scriptedFailure{
		status: http.StatusOK,
		body:   `{"ref": "refs/tags/v1.2.0", "object": {"sha": "` + kitCommit + `", "type": "commit"}}`,
	})

	got, err := newTestClient(t, api.url()).CollectMarketplace(t.Context(), "plugin-market", marketAudited())
	if err != nil {
		t.Fatalf("CollectMarketplace() error = %v, want nil", err)
	}
	if got.Entries[0].TagCommit != kitCommit || got.Entries[0].Problem != "" {
		t.Errorf("entry = %+v, want TagCommit %s and no problem", got.Entries[0], kitCommit)
	}
	if n := api.callsTo(kitTagObject); n != 0 {
		t.Errorf("tag object read %d times, want none for a lightweight tag", n)
	}
}

// TestClient_CollectMarketplace_tagThatIsNoCommit: a tag that resolves to
// something other than a commit, or keeps resolving to tags, has no commit to
// compare with the pin, and says why.
func TestClient_CollectMarketplace_tagThatIsNoCommit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "a ref to a tree",
			path: kitRef,
			body: `{"ref": "refs/tags/v1.2.0", "object": {"sha": "` + kitCommit + `", "type": "tree"}}`,
		},
		{
			name: "an annotated tag of a blob",
			path: kitTagObject,
			body: `{"sha": "` + kitTag + `", "object": {"sha": "` + kitCommit + `", "type": "blob"}}`,
		},
		{
			name: "a ref to a commit with no sha",
			path: kitRef,
			body: `{"ref": "refs/tags/v1.2.0", "object": {"sha": "", "type": "commit"}}`,
		},
		{
			name: "an annotated tag of a commit with no sha",
			path: kitTagObject,
			body: `{"sha": "` + kitTag + `", "object": {"sha": "", "type": "commit"}}`,
		},
		{
			name: "an annotated tag of a tag with no sha",
			path: kitTagObject,
			body: `{"sha": "` + kitTag + `", "object": {"sha": "", "type": "tag"}}`,
		},
		{
			name: "an annotated tag of itself",
			path: kitTagObject,
			body: `{"sha": "` + kitTag + `", "object": {"sha": "` + kitTag + `", "type": "tag"}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			api := newFakeAPI(t)
			answers := make([]scriptedFailure, 10)
			for i := range answers {
				answers[i] = scriptedFailure{status: http.StatusOK, body: tc.body}
			}
			api.failNext(tc.path, answers...)

			got, err := newTestClient(t, api.url()).CollectMarketplace(t.Context(), "plugin-market", marketAudited())
			if err != nil {
				t.Fatalf("CollectMarketplace() error = %v, want nil", err)
			}
			entry := got.Entries[0]
			if entry.TagCommit != "" {
				t.Errorf("TagCommit = %q, want none", entry.TagCommit)
			}
			if want := "tag v1.2.0 does not resolve to a commit"; entry.Problem != want {
				t.Errorf("Problem = %q, want %q", entry.Problem, want)
			}
			// The other lookups are independent of the tag's and still made.
			if !entry.ReleaseTag || entry.PluginName != "plugin-kit" {
				t.Errorf("entry = %+v, want the release and the manifest still read", entry)
			}
		})
	}
}

// TestClient_CollectMarketplace_unreadable: a marketplace that cannot be read
// is a fact on the snapshot, never an abort, so the rest of the report is
// still written and every marketplace check can say unreadable.
func TestClient_CollectMarketplace_unreadable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		repo    string
		audited []audit.Repo
		script  string
		want    string
	}{
		{
			name:    "invalid JSON",
			repo:    "plugin-market",
			audited: marketAudited(),
			script:  contentsBody(`{"plugins": [`),
			want:    ".claude-plugin/marketplace.json: not valid JSON",
		},
		{
			name:    "the wrong shape",
			repo:    "plugin-market",
			audited: marketAudited(),
			script:  contentsBody(`{"plugins": 3}`),
			want:    ".claude-plugin/marketplace.json: plugins is not a list",
		},
		{
			name:    "not a file",
			repo:    "plugin-market",
			audited: marketAudited(),
			script:  `[]`,
			want:    ".claude-plugin/marketplace.json: not a file",
		},
		{
			name:    "no marketplace.json",
			repo:    "skill-pack",
			audited: marketAudited(),
			want:    "no .claude-plugin/marketplace.json",
		},
		{
			name:    "a repository the account does not have",
			repo:    "plugin-market",
			audited: []audit.Repo{{Name: "plugin-kit"}},
			want:    "not among the audited repositories",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			api := newFakeAPI(t)
			if tc.script != "" {
				api.failNext(marketplacePath, scriptedFailure{status: http.StatusOK, body: tc.script})
			}

			got, err := newTestClient(t, api.url()).CollectMarketplace(t.Context(), tc.repo, tc.audited)
			if err != nil {
				t.Fatalf("CollectMarketplace() error = %v, want nil: an unreadable marketplace is a fact", err)
			}
			if got.Repo != tc.repo {
				t.Errorf("Repo = %q, want %q", got.Repo, tc.repo)
			}
			if !strings.HasPrefix(got.Unreadable, tc.want) {
				t.Errorf("Unreadable = %q, want it to start with %q", got.Unreadable, tc.want)
			}
			if got.Entries != nil {
				t.Errorf("Entries = %+v, want none alongside Unreadable", got.Entries)
			}
		})
	}
}

// TestClient_CollectMarketplace_missingRepositoryCostsNothing: a marketplace
// repository the account does not have is known from the audited set alone.
func TestClient_CollectMarketplace_missingRepositoryCostsNothing(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t)
	if _, err := newTestClient(t, api.url()).CollectMarketplace(t.Context(), "plugin-market", nil); err != nil {
		t.Fatalf("CollectMarketplace() error = %v, want nil", err)
	}
	if calls := api.seen(); len(calls) != 0 {
		t.Errorf("requests = %+v, want none", calls)
	}
}

// TestClient_CollectMarketplace_unexpectedStatusAborts: only a 404 is an
// answer on these endpoints. Anything else GitHub declines to answer aborts
// the run, as every other read does, after the 5xx retries.
func TestClient_CollectMarketplace_unexpectedStatusAborts(t *testing.T) {
	t.Parallel()

	for _, path := range []string{marketplacePath, kitRelease, kitRef, kitTagObject, kitManifest} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			api := newFakeAPI(t)
			answers := make([]scriptedFailure, 10)
			for i := range answers {
				answers[i] = scriptedFailure{status: http.StatusInternalServerError, body: `{"message":"Server Error"}`}
			}
			api.failNext(path, answers...)

			if _, err := newTestClient(t, api.url()).CollectMarketplace(t.Context(), "plugin-market", marketAudited()); err == nil {
				t.Fatalf("CollectMarketplace() error = nil, want a 500 on %s to abort", path)
			}
		})
	}
}

// TestClient_CollectMarketplace_manifestProblems: a manifest at the pinned
// commit that names nothing leaves PluginName empty and says why.
func TestClient_CollectMarketplace_manifestProblems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, body, want string
	}{
		{name: "not a file", body: `[]`, want: ".claude-plugin/plugin.json at " + kitCommit + ": not a file"},
		{name: "invalid JSON", body: contentsBody(`{`), want: ".claude-plugin/plugin.json at " + kitCommit + ": not valid JSON"},
		{name: "no name", body: contentsBody(`{"version": "1.2.0"}`), want: ".claude-plugin/plugin.json at " + kitCommit + ": no name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			api := newFakeAPI(t)
			api.failNext(kitManifest, scriptedFailure{status: http.StatusOK, body: tc.body})

			got, err := newTestClient(t, api.url()).CollectMarketplace(t.Context(), "plugin-market", marketAudited())
			if err != nil {
				t.Fatalf("CollectMarketplace() error = %v, want nil", err)
			}
			entry := got.Entries[0]
			if entry.PluginName != "" || !strings.HasPrefix(entry.Problem, tc.want) {
				t.Errorf("entry = %+v, want no plugin name and a problem starting %q", entry, tc.want)
			}
		})
	}
}

// TestClient_CollectMarketplace_unpinnedEntry: an entry with no ref and no sha
// has nothing to look up and says so. The repository is matched the way
// GitHub matches it, ignoring case, and recorded as written.
func TestClient_CollectMarketplace_unpinnedEntry(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t)
	api.failNext(marketplacePath, scriptedFailure{
		status: http.StatusOK,
		body:   contentsBody(`{"plugins": [{"name": "plugin-kit", "source": {"source": "github", "repo": "GH-Owner/Plugin-Kit"}}]}`),
	})

	got, err := newTestClient(t, api.url()).CollectMarketplace(t.Context(), "plugin-market", marketAudited())
	if err != nil {
		t.Fatalf("CollectMarketplace() error = %v, want nil", err)
	}
	want := []audit.MarketplaceEntry{{Name: "plugin-kit", Repo: "GH-Owner/Plugin-Kit", Problem: "no ref; no sha"}}
	if diff := cmp.Diff(want, got.Entries); diff != "" {
		t.Errorf("entries mismatch (-want +got):\n%s", diff)
	}
	if calls := len(api.seen()); calls != 1 {
		t.Errorf("requests = %d, want only the marketplace read", calls)
	}
}

// marketplaceOf is a contents answer for a marketplace listing gh-owner/
// plugin-kit pinned to ref, with no sha so that the only lookups made are
// the ref's.
func marketplaceOf(ref string) string {
	return contentsBody(`{"plugins": [{"name": "plugin-kit", "source": ` +
		`{"source": "github", "repo": "gh-owner/plugin-kit", "ref": "` + ref + `"}}]}`)
}

// TestClient_CollectMarketplace_emptyRepository: GitHub answers 409 for a
// ref in a repository with no commits, a status that is no documented
// answer. An entry naming an empty audited repository is known to have no
// pin from the audited set alone, so nothing is asked of it.
func TestClient_CollectMarketplace_emptyRepository(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t)
	audited := []audit.Repo{{Name: "plugin-market"}, {Name: "plugin-kit", Empty: true}, {Name: "skill-pack"}}

	got, err := newTestClient(t, api.url()).CollectMarketplace(t.Context(), "plugin-market", audited)
	if err != nil {
		t.Fatalf("CollectMarketplace() error = %v, want nil", err)
	}
	want := audit.MarketplaceEntry{
		Name: "plugin-kit", Repo: "gh-owner/plugin-kit", Ref: "v1.2.0", SHA: kitCommit, Problem: "empty repository",
	}
	if diff := cmp.Diff(want, got.Entries[0]); diff != "" {
		t.Errorf("entry mismatch (-want +got):\n%s", diff)
	}
	for _, c := range api.seen() {
		if strings.Contains(c.path, "/plugin-kit/") {
			t.Errorf("GET %s: an empty repository is asked nothing", c.path)
		}
	}
}

// TestClient_CollectMarketplace_unsafeRef: a ref comes from another
// repository's file, and one with a ".", ".." or empty segment would, once
// joined into the URL, name another endpoint than the tag lookups. It is
// recorded as unsafe and never requested.
func TestClient_CollectMarketplace_unsafeRef(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{"..", ".", "a/../b", "./x", "a//b", "v1/"} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()
			api := newFakeAPI(t)
			api.failNext(marketplacePath, scriptedFailure{status: http.StatusOK, body: marketplaceOf(ref)})

			got, err := newTestClient(t, api.url()).CollectMarketplace(t.Context(), "plugin-market", marketAudited())
			if err != nil {
				t.Fatalf("CollectMarketplace() error = %v, want nil", err)
			}
			if want := "unsafe ref; no sha"; len(got.Entries) != 1 || got.Entries[0].Problem != want {
				t.Errorf("entries = %+v, want one whose problem is %q", got.Entries, want)
			}
			if calls := api.seen(); len(calls) != 1 {
				t.Errorf("requests = %+v, want only the marketplace read", calls)
			}
		})
	}
}

// TestClient_CollectMarketplace_slashedTag: a tag may contain slashes, which
// stay separators in both lookups, so the release and the ref are asked for
// the same tag.
func TestClient_CollectMarketplace_slashedTag(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t)
	api.failNext(marketplacePath, scriptedFailure{status: http.StatusOK, body: marketplaceOf("release/v1")})

	if _, err := newTestClient(t, api.url()).CollectMarketplace(t.Context(), "plugin-market", marketAudited()); err != nil {
		t.Fatalf("CollectMarketplace() error = %v, want nil", err)
	}
	for _, path := range []string{
		"/repos/gh-owner/plugin-kit/releases/tags/release/v1",
		"/repos/gh-owner/plugin-kit/git/ref/tags/release/v1",
	} {
		n := 0
		for _, c := range api.seen() {
			if c.escapedPath == path {
				n++
			}
		}
		if n != 1 {
			t.Errorf("GET %s made %d times, want once; requests = %+v", path, n, api.seen())
		}
	}
}
