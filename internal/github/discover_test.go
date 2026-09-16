package github_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/github"
)

// TestClient_Discover drives the real pagination path: the first page's Link
// header names api.github.com, so the client has to re-base it onto the server
// it was configured with rather than following the host GitHub printed.
//
// The two discovery fixtures carry only the fields this tool reads. The live
// payload has a hundred more, none of which can be used: security_and_analysis
// is absent from the list even for a public repository, so folding any setting
// into discovery would read a value that is not there.
func TestClient_Discover(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	got, err := newTestClient(t, api.url()).Discover(t.Context())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	// compiler-config is archived, updater is a fork, and java-demo is both:
	// none of the three is in scope.
	want := []string{"cipher-lib", "hook-guard", "shell-scripts", "web-app"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Discover mismatch (-want +got):\n%s", diff)
	}
}

// TestClient_Discover_sortsCaseInsensitively pins the ordering the report
// depends on: a mixed-case name sorts by its lowercase form, not by where its
// capital letters would naively land.
func TestClient_Discover_sortsCaseInsensitively(t *testing.T) {
	t.Parallel()

	const page = `[
	  {"name": "media-server", "fork": false, "archived": false},
	  {"name": "mixedCase-flake", "fork": false, "archived": false},
	  {"name": "Cipher-lib", "fork": false, "archived": false},
	  {"name": "private-notes", "fork": false, "archived": false}
	]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(page)); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	got, err := newTestClient(t, server.URL).Discover(t.Context())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []string{"Cipher-lib", "media-server", "mixedCase-flake", "private-notes"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Discover mismatch (-want +got):\n%s", diff)
	}
}

// TestClient_Discover_emptyAccount checks the no-repositories case answers an
// empty list rather than an error, because an account with nothing in it is a
// valid state and repos.toml coverage is what reports the mismatch.
func TestClient_Discover_emptyAccount(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`[]`)); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	got, err := newTestClient(t, server.URL).Discover(t.Context())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Discover returned %v, want nothing", got)
	}
}

// TestClient_Discover_malformedPayload asserts a body that is not the expected
// shape is a runtime failure, not an empty account.
func TestClient_Discover_malformedPayload(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"message": "not a list"}`)); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	if _, err := newTestClient(t, server.URL).Discover(t.Context()); err == nil {
		t.Fatal("Discover accepted a payload that is not a list")
	}
}

// TestClient_Discover_followsOnlyAWellFormedNextLink pins what the paging
// loop does with each shape of Link header GitHub can send. A rel="next"
// entry is followed, re-based onto the server the client was given; anything
// else — no header, no next entry, an entry with no rel, an unparsable URL —
// ends the listing cleanly rather than looping or leaving that server.
func TestClient_Discover_followsOnlyAWellFormedNextLink(t *testing.T) {
	t.Parallel()

	const firstPage = "/user/repos?affiliation=owner&per_page=100"
	tests := []struct {
		name string
		// link is the Link header on the first page; later pages carry none.
		link string
		// wantRequests is every request the server should see, in order.
		wantRequests []string
	}{
		{name: "no header", link: "", wantRequests: []string{firstPage}},
		{
			name: "next and last",
			link: `<https://api.github.com/user/repos?per_page=100&page=2>; rel="next", ` +
				`<https://api.github.com/user/repos?per_page=100&page=7>; rel="last"`,
			wantRequests: []string{firstPage, "/user/repos?per_page=100&page=2"},
		},
		{
			name: "last page has no next",
			link: `<https://api.github.com/user/repos?per_page=100&page=1>; rel="prev", ` +
				`<https://api.github.com/user/repos?per_page=100&page=1>; rel="first"`,
			wantRequests: []string{firstPage},
		},
		{
			name:         "next without a query",
			link:         `<https://api.github.com/user/repos>; rel="next"`,
			wantRequests: []string{firstPage, "/user/repos"},
		},
		{
			name:         "malformed entry is ignored",
			link:         `<https://api.github.com/user/repos>`,
			wantRequests: []string{firstPage},
		},
		{
			name:         "unparsable url yields nothing",
			link:         "<://nope>; rel=\"next\"",
			wantRequests: []string{firstPage},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var (
				mu       sync.Mutex
				requests []string
			)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, r.URL.RequestURI())
				page := len(requests)
				mu.Unlock()

				if page == 1 && tc.link != "" {
					w.Header().Set("Link", tc.link)
				}
				w.Header().Set("Content-Type", "application/json")
				body := `[{"name": "page` + strconv.Itoa(page) + `", "fork": false, "archived": false}]`
				if _, err := w.Write([]byte(body)); err != nil {
					t.Errorf("write: %v", err)
				}
			}))
			t.Cleanup(server.Close)

			got, err := newTestClient(t, server.URL).Discover(t.Context())
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}

			mu.Lock()
			seen := slices.Clone(requests)
			mu.Unlock()
			if diff := cmp.Diff(tc.wantRequests, seen); diff != "" {
				t.Errorf("requests mismatch (-want +got):\n%s", diff)
			}
			// One repository per page fetched, so a page that was requested
			// but not folded in would show here.
			wantNames := make([]string, 0, len(tc.wantRequests))
			for i := range tc.wantRequests {
				wantNames = append(wantNames, "page"+strconv.Itoa(i+1))
			}
			if diff := cmp.Diff(wantNames, got); diff != "" {
				t.Errorf("Discover mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestClient_Discover_runawayPaginationStops guards the paging loop: a server
// that always advertises a next page must end the run with an error rather than
// looping until the process is killed.
func TestClient_Discover_runawayPaginationStops(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", `<https://api.github.com/user/repos?page=9>; rel="next"`)
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`[{"name": "loop", "fork": false, "archived": false}]`)); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	_, err := newTestClient(t, server.URL).Discover(t.Context())
	if err == nil {
		t.Fatal("Discover followed an endless Link chain")
	}
	if errors.Is(err, github.ErrUnexpectedStatus) {
		t.Errorf("error = %v, want a pagination failure rather than a status failure", err)
	}
}
