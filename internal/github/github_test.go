package github_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/github"
)

// theToken is the value every test authenticates with. It must never appear in
// any output the tests inspect.
const theToken = "ghp_fixture_token_value"

// discardLogger keeps test output clean; no test asserts on log lines.
func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// newTestClient points a client at the fake API.
func newTestClient(t *testing.T, baseURL string) *github.Client {
	t.Helper()
	client, err := github.New(github.Options{
		Owner:      testOwner,
		Token:      theToken,
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		Logger:     discardLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Every test here drives a fake that answers at once. Keeping the backoff
	// would make the tests of the abort paths — which now retry a 5xx four
	// times before giving up — spend seconds waiting for an answer that a
	// fixture already gave.
	github.DisableRetryWaits(client)
	return client
}

func TestSecret_redactsItself(t *testing.T) {
	t.Parallel()

	secret := github.Secret(theToken)

	if got := secret.String(); got != "[redacted]" {
		t.Errorf("String() = %q, want %q", got, "[redacted]")
	}
	if got := secret.LogValue().String(); got != "[redacted]" {
		t.Errorf("LogValue() = %q, want %q", got, "[redacted]")
	}
	if got := secret.Reveal(); got != theToken {
		t.Errorf("Reveal() = %q, want %q", got, theToken)
	}
}

// TestSecret_neverReachesOutput drives the two paths a token would escape
// through if Secret did not redact: a formatted string and a log line.
func TestSecret_neverReachesOutput(t *testing.T) {
	t.Parallel()

	secret := github.Secret(theToken)

	// The log line is itself the redaction under test — LogValue is what keeps
	// the token out of it — which is why this test reads log output.
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	logger.Info("authenticating", "token", secret)

	outputs := map[string]string{
		"log line":  logged.String(),
		"formatted": fmtSprint(secret),
		"sprintf":   fmt.Sprintf("%v", secret),
	}
	for name, out := range outputs {
		if strings.Contains(out, theToken) {
			t.Errorf("%s carries the token: %s", name, out)
		}
		if !strings.Contains(out, "[redacted]") {
			t.Errorf("%s is missing the redaction: %s", name, out)
		}
	}
}

// fmtSprint routes through the Stringer, which is the escape path being tested.
func fmtSprint(s github.Secret) string { return fmt.Sprint(s) }

// TestResolveToken_envWins covers the path the refresh workflow takes: with
// GITHUB_TOKEN set, the token is taken from the environment and gh is never
// consulted, so no gh binary is needed here.
func TestResolveToken_envWins(t *testing.T) {
	t.Parallel()

	env := map[string]string{"GITHUB_TOKEN": theToken}
	getenv := func(key string) string { return env[key] }

	got, err := github.ResolveToken(t.Context(), getenv)
	if err != nil {
		t.Fatalf("ResolveToken() error = %v, want nil", err)
	}
	if got.Reveal() != theToken {
		t.Errorf("ResolveToken().Reveal() = %q, want %q", got.Reveal(), theToken)
	}
	if got.String() != "[redacted]" {
		t.Errorf("ResolveToken().String() = %q, want %q", got.String(), "[redacted]")
	}
}

func TestNew_rejectsIncompleteOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts github.Options
	}{
		{name: "no owner", opts: github.Options{Token: theToken}},
		{name: "no token", opts: github.Options{Owner: testOwner}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := github.New(tc.opts); !errors.Is(err, github.ErrConfig) {
				t.Errorf("New error = %v, want ErrConfig", err)
			}
		})
	}
}

// TestNew_defaultsAreUsable checks that a client built with only the required
// options works, and in particular that the default HTTP client has a timeout
// rather than being http.DefaultClient, which has none.
func TestNew_defaultsAreUsable(t *testing.T) {
	t.Parallel()

	client, err := github.New(github.Options{Owner: testOwner, Token: theToken})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client == nil {
		t.Fatal("New returned a nil client")
	}
}

// TestClient_sendsAuthAndVersionHeaders pins the request shape: the token
// travels as a bearer credential and the REST schema is pinned, so a future
// default cannot silently reshape a response this package parses.
func TestClient_sendsAuthAndVersionHeaders(t *testing.T) {
	t.Parallel()

	type headers struct {
		auth       string
		accept     string
		apiVersion string
		agent      string
	}
	got := make(chan headers, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- headers{
			auth:       r.Header.Get("Authorization"),
			accept:     r.Header.Get("Accept"),
			apiVersion: r.Header.Get("X-GitHub-Api-Version"),
			agent:      r.Header.Get("User-Agent"),
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte("[]")); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL)
	if _, err := client.Discover(t.Context()); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	h := <-got
	if want := "Bearer " + theToken; h.auth != want {
		t.Errorf("Authorization = %q, want %q", h.auth, want)
	}
	if want := "application/vnd.github+json"; h.accept != want {
		t.Errorf("Accept = %q, want %q", h.accept, want)
	}
	if want := "2022-11-28"; h.apiVersion != want {
		t.Errorf("X-GitHub-Api-Version = %q, want %q", h.apiVersion, want)
	}
	if want := "github-repos-audit"; h.agent != want {
		t.Errorf("User-Agent = %q, want %q", h.agent, want)
	}
}

// TestClient_unexpectedStatusAborts covers the rule that any status this
// package has no reading for aborts the run rather than being folded into a
// missing field.
func TestClient_unexpectedStatusAborts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "server error", status: http.StatusInternalServerError, body: `{"message":"Server Error"}`},
		{name: "forbidden", status: http.StatusForbidden, body: `{"message":"Resource not accessible"}`},
		{name: "unauthorised", status: http.StatusUnauthorized, body: `{"message":"Bad credentials"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, tc.body, tc.status)
			}))
			t.Cleanup(server.Close)

			_, err := newTestClient(t, server.URL).Discover(t.Context())
			if !errors.Is(err, github.ErrUnexpectedStatus) {
				t.Fatalf("Discover error = %v, want ErrUnexpectedStatus", err)
			}
			if strings.Contains(err.Error(), theToken) {
				t.Errorf("error carries the token: %v", err)
			}
		})
	}
}

// TestClient_unexpectedStatusQuotesTheBody pins the user-facing shape of the
// abort message: the body GitHub answered with is quoted, collapsed onto one
// line so it reads inside an error, and cut short with an ellipsis when it is
// long. GitHub's error bodies are short JSON objects, so the quote is usually
// the whole diagnosis.
func TestClient_unexpectedStatusQuotesTheBody(t *testing.T) {
	t.Parallel()

	longBody := strings.Repeat("x", 1000)
	tests := []struct {
		name string
		body string
		// want is text the error must carry; absent is text it must not.
		want   string
		absent string
	}{
		{
			name:   "whitespace collapses",
			body:   "{\n  \"message\": \"Not Found\"\n}",
			want:   `{ "message": "Not Found" }`,
			absent: "\n",
		},
		{
			name:   "long bodies are truncated",
			body:   longBody,
			want:   "x…",
			absent: longBody,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, tc.body, http.StatusInternalServerError)
			}))
			t.Cleanup(server.Close)

			_, err := newTestClient(t, server.URL).Discover(t.Context())
			if !errors.Is(err, github.ErrUnexpectedStatus) {
				t.Fatalf("Discover error = %v, want ErrUnexpectedStatus", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.want) {
				t.Errorf("error = %q, want it to contain %q", msg, tc.want)
			}
			if strings.Contains(msg, tc.absent) {
				t.Errorf("error = %q, want it not to contain %q", msg, tc.absent)
			}
		})
	}
}

// TestClient_transportFailureIsAnError covers the path where Do itself fails,
// which is distinct from a non-2xx status.
func TestClient_transportFailureIsAnError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close() // nothing is listening now, so the connection is refused

	if _, err := newTestClient(t, server.URL).Discover(t.Context()); err == nil {
		t.Fatal("Discover succeeded against a closed server")
	}
}

// TestClient_contextCancellationStopsTheRun asserts the run gives up promptly
// when its caller does, which is what makes SIGINT exit 130 rather than hang.
func TestClient_contextCancellationStopsTheRun(t *testing.T) {
	t.Parallel()

	blocked := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(blocked)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(t.Context())
	client := newTestClient(t, server.URL)

	errs := make(chan error, 1) // buffered so the goroutine cannot block if the test returns first
	go func() {
		_, err := client.Discover(ctx)
		errs <- err
	}()

	<-blocked
	cancel()

	err := <-errs
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Discover error = %v, want context.Canceled", err)
	}
}
