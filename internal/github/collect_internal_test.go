package github

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"
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
