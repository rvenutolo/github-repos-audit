package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// TestRepository_releases is why the query asks for five releases rather than
// one. GitHub returns them newest first and counts drafts in the total, so the
// summary has to subtract what a stranger cannot see and then look past it:
// a draft published later than the newest real release must not become the
// repository's last release date, and neither must an unpublished node.
func TestRepository_releases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		nodes           string
		totalCount      int
		wantDrafts      int
		wantLastPublish time.Time
	}{
		{
			name:            "no releases at all",
			nodes:           `[]`,
			totalCount:      0,
			wantDrafts:      0,
			wantLastPublish: time.Time{},
		},
		{
			name: "drafts are counted and never dated",
			nodes: `[
				{"is_draft": true, "published_at": "2026-09-01T12:00:00Z"},
				{"is_draft": true, "published_at": null},
				{"is_draft": false, "published_at": "2026-06-01T12:00:00Z"}
			]`,
			totalCount:      3,
			wantDrafts:      2,
			wantLastPublish: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			// A release created but never published carries no date. It is not
			// a draft, so it is not subtracted; it simply has nothing to say
			// about when the repository last shipped.
			name: "an unpublished release contributes no date",
			nodes: `[
				{"is_draft": false, "published_at": null},
				{"is_draft": false, "published_at": "2026-06-01T12:00:00Z"}
			]`,
			totalCount:      2,
			wantDrafts:      0,
			wantLastPublish: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			name: "the newest published release wins whatever the order",
			nodes: `[
				{"is_draft": false, "published_at": "2026-01-02T00:00:00Z"},
				{"is_draft": false, "published_at": "2026-07-04T00:00:00Z"},
				{"is_draft": false, "published_at": "2026-03-03T00:00:00Z"}
			]`,
			totalCount:      3,
			wantDrafts:      0,
			wantLastPublish: time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC),
		},
		{
			// The one shape the five-node window exists for: nothing but
			// drafts in view, so there is a total but no date to show.
			name: "a run of drafts hides nothing because there is nothing behind it",
			nodes: `[
				{"is_draft": true, "published_at": null},
				{"is_draft": true, "published_at": null}
			]`,
			totalCount:      2,
			wantDrafts:      2,
			wantLastPublish: time.Time{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := `{"releases":{"total_count":` + strconv.Itoa(tc.totalCount) + `,"nodes":` + tc.nodes + `}}`
			var r repository
			if err := json.Unmarshal([]byte(body), &r); err != nil {
				t.Fatalf("unmarshal the fixture: %v", err)
			}

			got := r.releases()
			if got.Total != tc.totalCount {
				t.Errorf("Total = %d, want %d", got.Total, tc.totalCount)
			}
			if got.Drafts != tc.wantDrafts {
				t.Errorf("Drafts = %d, want %d", got.Drafts, tc.wantDrafts)
			}
			if !got.LastPublishedAt.Equal(tc.wantLastPublish) {
				t.Errorf("LastPublishedAt = %v, want %v", got.LastPublishedAt, tc.wantLastPublish)
			}
		})
	}
}

// TestRepository_releases_normalisesToUTC keeps the snapshot comparable with
// itself: audit.json is diffed between runs, and a date that moved only
// because GitHub answered in another offset would read as a release.
func TestRepository_releases_normalisesToUTC(t *testing.T) {
	t.Parallel()

	var r repository
	body := `{"releases":{"total_count":1,"nodes":[{"is_draft":false,"published_at":"2026-06-01T14:00:00+02:00"}]}}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("unmarshal the fixture: %v", err)
	}

	got := r.releases().LastPublishedAt
	want := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("LastPublishedAt = %v (%v), want %v (UTC)", got, got.Location(), want)
	}
}

// TestWaitFor is the backoff's one job: hold for the time asked, and come back
// at once when the run is cancelled. A non-positive wait is not a hold at all,
// so it reports whether the context is still live rather than pausing.
func TestWaitFor(t *testing.T) {
	t.Parallel()

	t.Run("a positive wait elapses", func(t *testing.T) {
		t.Parallel()

		start := time.Now()
		if err := waitFor(t.Context(), 10*time.Millisecond); err != nil {
			t.Errorf("waitFor() error = %v, want nil", err)
		}
		if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
			t.Errorf("waitFor returned after %v, want at least 10ms", elapsed)
		}
	})

	t.Run("a non-positive wait does not block", func(t *testing.T) {
		t.Parallel()

		for _, d := range []time.Duration{0, -time.Second} {
			if err := waitFor(t.Context(), d); err != nil {
				t.Errorf("waitFor(ctx, %v) error = %v, want nil on a live context", d, err)
			}
		}
	})

	t.Run("a cancelled context is reported without waiting", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		// Long enough that a wait rather than a cancellation would hang the
		// test out to its own deadline.
		if err := waitFor(ctx, time.Hour); !errors.Is(err, context.Canceled) {
			t.Errorf("waitFor() error = %v, want context.Canceled", err)
		}
		if err := waitFor(ctx, 0); !errors.Is(err, context.Canceled) {
			t.Errorf("waitFor(ctx, 0) error = %v, want context.Canceled", err)
		}
	})
}

// TestNew_rejectsAnUnparsableBaseURL keeps a bad --api-url from becoming a
// request to somewhere else. A control character is the case url.Parse rejects
// and string concatenation would otherwise carry all the way to the wire.
func TestNew_rejectsAnUnparsableBaseURL(t *testing.T) {
	t.Parallel()

	_, err := New(Options{
		Owner:   "gh-owner",
		Token:   "token",
		BaseURL: "https://api.github.com\x7f/",
	})
	if !errors.Is(err, ErrConfig) {
		t.Errorf("New() error = %v, want it to wrap ErrConfig", err)
	}
}

// TestJoinGraphQLErrors covers the shape GitHub is not obliged to fill in: an
// error object with a message and no type. Rendering it as ": message" would
// read like a missing field rather than like the error it is.
func TestJoinGraphQLErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		errs []graphQLError
		want string
	}{
		{"none", nil, ""},
		{"a typed error", []graphQLError{{Type: "NOT_FOUND", Message: "no repo"}}, "NOT_FOUND: no repo"},
		{"an untyped error", []graphQLError{{Message: "something went wrong"}}, "something went wrong"},
		{
			name: "both, in the order given",
			errs: []graphQLError{{Message: "first"}, {Type: "FORBIDDEN", Message: "second"}},
			want: "first; FORBIDDEN: second",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := joinGraphQLErrors(tc.errs); got != tc.want {
				t.Errorf("joinGraphQLErrors() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWalkHistory_guards covers the walk's failure modes. They are internal
// because the fake API serves one fixture per cursor and cannot script a
// malformed page without a new fixture repository.
func TestWalkHistory_guards(t *testing.T) {
	t.Parallel()

	pageOne := func(cursor *string) *history {
		return &history{
			PageInfo: pageInfo{HasNextPage: true, EndCursor: cursor},
			Nodes: []historyNode{{
				Author: &gitActor{Name: new("Pat Example"), Email: new("pat@example.com")},
			}},
		}
	}
	errStub := errors.New("stub failure")

	tests := []struct {
		name      string
		first     *history
		stubErr   error
		wantCalls int
		wantErr   func(error) bool
		wantText  string
	}{
		{
			// Asking again without a cursor would return page one, which
			// promises a next page again: a walk that never ends.
			name:      "a next page promised with a null cursor",
			first:     pageOne(nil),
			wantCalls: 0,
			wantErr:   func(err error) bool { return errors.Is(err, errGraphQL) },
			wantText:  "no end cursor",
		},
		{
			name:      "a next page promised with an empty cursor",
			first:     pageOne(new("")),
			wantCalls: 0,
			wantErr:   func(err error) bool { return errors.Is(err, errGraphQL) },
			wantText:  "no end cursor",
		},
		{
			// A failed page must fail the walk: identities from the pages
			// before it are half a history, not a smaller whole one.
			name:      "a page that fails",
			first:     pageOne(new("cursor-02")),
			stubErr:   errStub,
			wantCalls: 1,
			wantErr:   func(err error) bool { return errors.Is(err, errStub) },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			got, err := walkHistory(t.Context(), tc.first, func(context.Context, string) (*history, error) {
				calls++
				if tc.stubErr != nil {
					return nil, tc.stubErr
				}
				return &history{}, nil
			})
			if err == nil || !tc.wantErr(err) {
				t.Fatalf("walkHistory() error = %v, want the guard's error", err)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("walkHistory() error = %q, want it to contain %q", err, tc.wantText)
			}
			if got != nil {
				t.Errorf("walkHistory() = %v alongside an error, want nil", got)
			}
			if calls != tc.wantCalls {
				t.Errorf("next page fetched %d times, want %d", calls, tc.wantCalls)
			}
		})
	}
}

// TestWalkHistory_recordsExactStrings pins what the walk records: every
// actor on every page, null halves as "", null actors not at all, no case
// folding, and one entry per distinct pair in byte order.
func TestWalkHistory_recordsExactStrings(t *testing.T) {
	t.Parallel()

	first := &history{
		PageInfo: pageInfo{HasNextPage: true, EndCursor: new("cursor-02")},
		Nodes: []historyNode{
			{
				Author:    &gitActor{Name: new("Pat Example"), Email: new("pat@example.com")},
				Committer: &gitActor{Name: new("Pat Example"), Email: new("Pat@Example.com")},
			},
			{Author: nil, Committer: &gitActor{Name: new("Robin Other"), Email: nil}},
		},
	}
	second := &history{
		PageInfo: pageInfo{HasNextPage: false},
		Nodes: []historyNode{
			{
				Author:    &gitActor{Name: new("Pat Example"), Email: new("pat@example.com")},
				Committer: &gitActor{Name: nil, Email: new("robin@example.org")},
			},
		},
	}
	var asked []string
	got, err := walkHistory(t.Context(), first, func(_ context.Context, cursor string) (*history, error) {
		asked = append(asked, cursor)
		return second, nil
	})
	if err != nil {
		t.Fatalf("walkHistory() error = %v, want nil", err)
	}
	want := []audit.Identity{
		{Name: "", Email: "robin@example.org"},
		{Name: "Pat Example", Email: "Pat@Example.com"},
		{Name: "Pat Example", Email: "pat@example.com"},
		{Name: "Robin Other", Email: ""},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("walkHistory() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"cursor-02"}, asked); diff != "" {
		t.Errorf("cursors asked mismatch (-want +got):\n%s", diff)
	}
}

// graphQLServer answers every POST with body and hands back a Client pointed
// at it. Its sleep returns at once: these tests are about what an answer
// means, not about waiting on one.
func graphQLServer(t *testing.T, body string) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	c, err := New(Options{Owner: "gh-owner", Token: "token", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return c
}

// TestClient_identities_abortsWhenTheCommitIsGone covers a later page that
// finds no commit at the oid the first page described. The commit vanished
// mid-walk, and what was walked so far is not the history, so the run aborts
// rather than recording it.
func TestClient_identities_abortsWhenTheCommitIsGone(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		`{"data":{"repository":{"object":null}}}`,
		`{"data":{"repository":null}}`,
		`{"data":{"repository":{"object":{}}}}`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()

			c := graphQLServer(t, body)
			r := &repository{DefaultBranchRef: &branchRef{
				Name: "main",
				Target: &commit{
					OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0014",
					History: &history{
						PageInfo: pageInfo{HasNextPage: true, EndCursor: new("cursor-02")},
					},
				},
			}}
			got, err := c.identities(t.Context(), "web-app", r)
			if !errors.Is(err, errGraphQL) || !strings.Contains(err.Error(), "commit not found") {
				t.Fatalf("identities() error = %v, want a commit-not-found graphql error", err)
			}
			if got != nil {
				t.Errorf("identities() = %v alongside an error, want nil", got)
			}
		})
	}
}

// TestClient_identities_noHistoryIsNoIdentities covers the answers that carry
// no history to walk: an empty repository, a ref with no target, and a target
// without the history selection. None of them is an error.
func TestClient_identities_noHistoryIsNoIdentities(t *testing.T) {
	t.Parallel()

	c := graphQLServer(t, `{"errors":[{"message":"no request was expected"}]}`)
	for name, r := range map[string]*repository{
		"empty repository": {},
		"no target":        {DefaultBranchRef: &branchRef{Name: "main"}},
		"no history on it": {DefaultBranchRef: &branchRef{Name: "main", Target: &commit{OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0014"}}},
	} {
		got, err := c.identities(t.Context(), "web-app", r)
		if err != nil || got != nil {
			t.Errorf("%s: identities() = %v, %v, want nil, nil", name, got, err)
		}
	}
}

// TestClient_postGraphQL_undecodableData covers data that parses as JSON but
// not as the shape asked for: an error, never a zero value read as an answer.
func TestClient_postGraphQL_undecodableData(t *testing.T) {
	t.Parallel()

	c := graphQLServer(t, `{"data":{"repository":"not an object"}}`)
	if _, err := c.fetchRepository(t.Context(), "web-app"); err == nil || !strings.Contains(err.Error(), "decode data") {
		t.Errorf("fetchRepository() error = %v, want a decode-data error", err)
	}
}
