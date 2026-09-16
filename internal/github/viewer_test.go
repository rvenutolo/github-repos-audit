package github_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/github"
)

func TestViewer_returnsTheTokensLogin(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	got, err := github.Viewer(t.Context(), github.Options{Token: theToken, BaseURL: api.url(), Logger: discardLogger()})
	if err != nil {
		t.Fatalf("Viewer() error = %v, want nil", err)
	}
	if got != testOwner {
		t.Errorf("Viewer() = %q, want %q", got, testOwner)
	}
}

func TestViewer_ignoresOptionsOwner(t *testing.T) {
	t.Parallel()

	api := newFakeAPI(t)
	got, err := github.Viewer(t.Context(),
		github.Options{Owner: "someone-else", Token: theToken, BaseURL: api.url(), Logger: discardLogger()})
	if err != nil {
		t.Fatalf("Viewer() error = %v, want nil", err)
	}
	if got != testOwner {
		t.Errorf("Viewer() = %q, want the token's login %q", got, testOwner)
	}
}

func TestViewer_failures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "unauthorised", status: http.StatusUnauthorized, body: `{"message":"Bad credentials"}`, want: github.ErrUnexpectedStatus},
		{name: "empty login", status: http.StatusOK, body: `{"login":""}`},
		{name: "not json", status: http.StatusOK, body: `<html>`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			t.Cleanup(server.Close)

			got, err := github.Viewer(t.Context(), github.Options{Token: theToken, BaseURL: server.URL, Logger: discardLogger()})
			if err == nil {
				t.Fatalf("Viewer() = %q, want an error", got)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("Viewer() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestViewer_rejectsMissingToken(t *testing.T) {
	t.Parallel()

	if _, err := github.Viewer(t.Context(), github.Options{}); !errors.Is(err, github.ErrConfig) {
		t.Errorf("Viewer() error = %v, want ErrConfig", err)
	}
}

func TestClient_Owner(t *testing.T) {
	t.Parallel()

	client, err := github.New(github.Options{Owner: testOwner, Token: theToken})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := client.Owner(); got != testOwner {
		t.Errorf("Owner() = %q, want %q", got, testOwner)
	}
}
