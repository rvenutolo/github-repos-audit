package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// storageBase is the repository path fetchStorage is handed in these tests.
const storageBase = "/repos/gh-owner/shell-scripts"

// storagePage is one canned answer of the fake storage endpoints. A link,
// when set, is sent as the Link header the way GitHub sends it: absolute, on
// api.github.com, and naming the repository by numeric id rather than by
// owner and name, which is what the client has to re-base onto its own URL.
type storagePage struct {
	status int
	body   string
	link   string
}

// emptyStorage is what each endpoint answers for a repository with nothing
// stored, so a test overrides only the endpoint it is about.
func emptyStorage() map[string]storagePage {
	return map[string]storagePage{
		storageBase + "/actions/caches":              {body: `{"total_count":0,"actions_caches":[]}`},
		storageBase + "/actions/cache/usage":         {body: `{"active_caches_size_in_bytes":0,"active_caches_count":0}`},
		storageBase + "/actions/cache/storage-limit": {body: `{"max_cache_size_gb":10}`},
		storageBase + "/actions/artifacts":           {body: `{"total_count":0,"artifacts":[]}`},
	}
}

// newStorageClient serves pages keyed by request path, with the query's
// branch filter appended as "?branch=<branch>" (decoded, so a branch the
// client failed to escape arrives cut short and finds no page) and its page
// number as "#<page>" for any page after the first, and fails the test on
// any request it has no answer for or on a list read without the API's
// largest page size. It counts requests by key so a test can say what was
// asked for.
func newStorageClient(t *testing.T, pages map[string]storagePage) (*Client, func(string) int) {
	t.Helper()

	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if b := r.URL.Query().Get("branch"); b != "" {
			key += "?branch=" + b
		}
		if p := r.URL.Query().Get("page"); p != "" && p != "1" {
			key += "#" + p
		}
		mu.Lock()
		counts[key]++
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/caches") || strings.HasSuffix(r.URL.Path, "/artifacts") {
			if got := r.URL.Query().Get("per_page"); got != "100" {
				t.Errorf("%s asked with per_page=%q, want 100", r.URL.Path, got)
			}
		}
		page, ok := pages[key]
		if !ok {
			t.Errorf("unexpected request for %s", r.URL.RequestURI())
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		if page.link != "" {
			w.Header().Set("Link", page.link)
		}
		status := page.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		if _, err := w.Write([]byte(page.body)); err != nil {
			t.Errorf("write %s: %v", key, err)
		}
	}))
	t.Cleanup(server.Close)

	c, err := New(Options{Owner: "gh-owner", Token: "token", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	seen := func(key string) int {
		mu.Lock()
		defer mu.Unlock()
		return counts[key]
	}
	return c, seen
}

// TestFetchStorage_pagesCaches follows the caches list across two pages by
// its Link header, which names the repository by id the way GitHub's does,
// and records every entry sorted by key then ref, without last_accessed_at.
func TestFetchStorage_pagesCaches(t *testing.T) {
	t.Parallel()

	pages := emptyStorage()
	pages[storageBase+"/actions/caches"] = storagePage{
		link: `<https://api.github.com/repositories/1000001/actions/caches?per_page=100&page=2>; rel="next", ` +
			`<https://api.github.com/repositories/1000001/actions/caches?per_page=100&page=2>; rel="last"`,
		body: `{"total_count":3,"actions_caches":[
			{"id":1,"ref":"refs/pull/12/merge","key":"go-Linux-abc","version":"v1",
			 "last_accessed_at":"2026-10-21T04:36:20.668438Z","created_at":"2026-10-19T02:20:33.392904Z","size_in_bytes":2000},
			{"id":2,"ref":"refs/heads/main","key":"nix-Linux-abc","version":"v1",
			 "last_accessed_at":"2026-10-21T04:36:20Z","created_at":"2026-10-18T00:00:00Z","size_in_bytes":812250354}
		]}`,
	}
	pages["/repositories/1000001/actions/caches#2"] = storagePage{
		link: `<https://api.github.com/repositories/1000001/actions/caches?per_page=100&page=1>; rel="prev", ` +
			`<https://api.github.com/repositories/1000001/actions/caches?per_page=100&page=1>; rel="first"`,
		body: `{"total_count":3,"actions_caches":[
			{"id":3,"ref":"refs/heads/main","key":"go-Linux-abc","version":"v1",
			 "last_accessed_at":"2026-10-21T04:36:20Z","created_at":"2026-10-17T00:00:00Z","size_in_bytes":206000000}
		]}`,
	}
	c, seen := newStorageClient(t, pages)

	var got audit.ActionsStorage
	if err := c.fetchStorage(t.Context(), storageBase, &got); err != nil {
		t.Fatalf("fetchStorage() error = %v, want nil", err)
	}

	want := []audit.Cache{
		{
			Key: "go-Linux-abc", Ref: "refs/heads/main", SizeBytes: 206000000,
			CreatedAt: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC),
		},
		{
			Key: "go-Linux-abc", Ref: "refs/pull/12/merge", SizeBytes: 2000,
			CreatedAt: time.Date(2026, 10, 19, 2, 20, 33, 392904000, time.UTC),
		},
		{
			Key: "nix-Linux-abc", Ref: "refs/heads/main", SizeBytes: 812250354,
			CreatedAt: time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC),
		},
	}
	if diff := cmp.Diff(want, got.Caches); diff != "" {
		t.Errorf("caches mismatch (-want +got):\n%s", diff)
	}
	if n := seen("/repositories/1000001/actions/caches#2"); n != 1 {
		t.Errorf("second caches page asked for %d times, want 1", n)
	}
}

// TestFetchStorage_sortsSameKeyAndRefByCreation: two entries can share a key
// and a ref (they differ in version), and GitHub's listing order is not a
// fact about either, so creation time decides between them.
func TestFetchStorage_sortsSameKeyAndRefByCreation(t *testing.T) {
	t.Parallel()

	pages := emptyStorage()
	pages[storageBase+"/actions/caches"] = storagePage{body: `{"total_count":2,"actions_caches":[
		{"ref":"refs/heads/main","key":"k","version":"b","created_at":"2026-10-19T00:00:00Z","size_in_bytes":2},
		{"ref":"refs/heads/main","key":"k","version":"a","created_at":"2026-10-18T00:00:00Z","size_in_bytes":1}
	]}`}
	c, _ := newStorageClient(t, pages)

	var got audit.ActionsStorage
	if err := c.fetchStorage(t.Context(), storageBase, &got); err != nil {
		t.Fatalf("fetchStorage() error = %v, want nil", err)
	}
	if len(got.Caches) != 2 || got.Caches[0].SizeBytes != 1 || got.Caches[1].SizeBytes != 2 {
		t.Errorf("caches = %+v, want the older entry first", got.Caches)
	}
}

// TestFetchStorage_skipsExpiredArtifacts: an expired artifact is still listed
// but holds no storage, so it is neither counted nor summed. The list is
// paged like the caches list.
func TestFetchStorage_skipsExpiredArtifacts(t *testing.T) {
	t.Parallel()

	pages := emptyStorage()
	pages[storageBase+"/actions/artifacts"] = storagePage{
		link: `<https://api.github.com/repositories/1000001/actions/artifacts?per_page=100&page=2>; rel="next"`,
		body: `{"total_count":3,"artifacts":[
			{"id":1,"name":"a","size_in_bytes":24939,"expired":false},
			{"id":2,"name":"b","size_in_bytes":5000000,"expired":true}
		]}`,
	}
	pages["/repositories/1000001/actions/artifacts#2"] = storagePage{body: `{"total_count":3,"artifacts":[
		{"id":3,"name":"c","size_in_bytes":61,"expired":false}
	]}`}
	c, _ := newStorageClient(t, pages)

	var got audit.ActionsStorage
	if err := c.fetchStorage(t.Context(), storageBase, &got); err != nil {
		t.Fatalf("fetchStorage() error = %v, want nil", err)
	}
	if got.ArtifactCount != 2 || got.ArtifactBytes != 25000 {
		t.Errorf("artifacts = %d · %d B, want 2 · 25000 B", got.ArtifactCount, got.ArtifactBytes)
	}
}

// TestFetchStorage_limitInBytes: the limit is given in gigabytes and recorded
// in decimal bytes, and the usage is GitHub's own figure.
func TestFetchStorage_limitInBytes(t *testing.T) {
	t.Parallel()

	pages := emptyStorage()
	pages[storageBase+"/actions/cache/usage"] = storagePage{
		body: `{"active_caches_size_in_bytes":8235856992,"active_caches_count":21}`,
	}
	c, _ := newStorageClient(t, pages)

	var got audit.ActionsStorage
	if err := c.fetchStorage(t.Context(), storageBase, &got); err != nil {
		t.Fatalf("fetchStorage() error = %v, want nil", err)
	}
	want := audit.ActionsStorage{CacheUsageBytes: 8235856992, CacheLimitBytes: 10_000_000_000}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("storage mismatch (-want +got):\n%s", diff)
	}
}

// TestFetchStorage_unexpectedStatusAborts: no storage endpoint has a
// non-200 answer that means something, so a status that survives the retries
// is an error naming the endpoint, whichever of the four it was.
func TestFetchStorage_unexpectedStatusAborts(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{
		storageBase + "/actions/caches",
		storageBase + "/actions/cache/usage",
		storageBase + "/actions/cache/storage-limit",
		storageBase + "/actions/artifacts",
	} {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()

			pages := emptyStorage()
			pages[endpoint] = storagePage{status: http.StatusInternalServerError, body: `{"message":"Server Error"}`}
			c, seen := newStorageClient(t, pages)

			var got audit.ActionsStorage
			err := c.fetchStorage(t.Context(), storageBase, &got)
			if !errors.Is(err, ErrUnexpectedStatus) {
				t.Fatalf("fetchStorage() error = %v, want ErrUnexpectedStatus", err)
			}
			if !strings.Contains(err.Error(), endpoint) {
				t.Errorf("fetchStorage() error = %q, want it to name %s", err, endpoint)
			}
			if n := seen(endpoint); n != maxAttempts {
				t.Errorf("%s asked for %d times, want %d: a 500 is retried before it aborts", endpoint, n, maxAttempts)
			}
		})
	}
}

// TestFetchStorage_stopsRunawayPaging: a Link header that never runs out is
// a paging fault, not a repository with endless storage, and it is an error
// rather than a loop or a silently partial sum.
func TestFetchStorage_stopsRunawayPaging(t *testing.T) {
	t.Parallel()

	pages := emptyStorage()
	pages[storageBase+"/actions/artifacts"] = storagePage{
		link: `<https://api.github.com` + storageBase + `/actions/artifacts?per_page=100>; rel="next"`,
		body: `{"total_count":1,"artifacts":[{"size_in_bytes":1,"expired":false}]}`,
	}
	c, seen := newStorageClient(t, pages)

	var got audit.ActionsStorage
	err := c.fetchStorage(t.Context(), storageBase, &got)
	if err == nil {
		t.Fatal("fetchStorage() error = nil, want a paging error")
	}
	if n := seen(storageBase + "/actions/artifacts"); n != maxStoragePages {
		t.Errorf("artifacts asked for %d times, want %d", n, maxStoragePages)
	}
}
