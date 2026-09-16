package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests live in the package because the policy they hold to its contract
// is deliberately unexported: retryWait is a decision, not an API, and the two
// seams that make it testable — the injected clock and the injected wait — are
// there so a test can assert a sixty-second backoff without spending sixty
// seconds. What a caller can see of all this is asserted through Client in the
// exported tests.

// theInstant is the wall clock every test that reads a header date pins to.
var theInstant = time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)

// waitRecorder stands in for the backoff wait: it records what the policy
// asked for and returns at once, so a test spends assertions rather than
// seconds. It honours a cancelled context because the real wait does, and the
// retry loop's behaviour on cancellation is part of what is under test.
type waitRecorder struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (w *waitRecorder) wait(ctx context.Context, d time.Duration) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.waits = append(w.waits, d)
	return ctx.Err()
}

func (w *waitRecorder) recorded() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration(nil), w.waits...)
}

// newRetryTestClient builds a client whose clock is fixed and whose backoff is
// recorded rather than taken.
func newRetryTestClient(t *testing.T, baseURL string) (*Client, *waitRecorder) {
	t.Helper()

	c, err := New(Options{Owner: "someone", Token: "token", BaseURL: baseURL})
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	rec := &waitRecorder{}
	c.now = func() time.Time { return theInstant }
	c.sleep = rec.wait
	return c, rec
}

// countingServer answers with the scripted statuses in order, repeating the
// last one once the script runs out, and counts what it was handed.
type countingServer struct {
	// answered is closed when the first request has been served, so a test can
	// wait for the client to be inside its backoff rather than poll for it.
	answered  chan struct{}
	closeOnce sync.Once

	mu     sync.Mutex
	calls  int
	bodies []string
}

func newCountingServer() *countingServer {
	return &countingServer{answered: make(chan struct{})}
}

func (s *countingServer) handle(script []scriptedResponse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			body = nil
		}

		s.mu.Lock()
		n := s.calls
		s.calls++
		s.bodies = append(s.bodies, string(body))
		s.mu.Unlock()

		step := script[min(n, len(script)-1)]
		for k, v := range step.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(step.status)
		if _, err := w.Write([]byte(step.body)); err != nil {
			return
		}
		s.closeOnce.Do(func() { close(s.answered) })
	}
}

func (s *countingServer) seen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *countingServer) sentBodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}

// scriptedResponse is one answer the fake endpoint gives.
type scriptedResponse struct {
	status int
	header map[string]string
	body   string
}

// TestClient_retryWait holds the policy to its discriminator: a 403 is
// retryable only when it carries evidence of a rate limit, because a bare 403
// is a token without a scope and resending it is a hang that looks like a
// stall.
func TestClient_retryWait(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		status      int
		header      map[string]string
		body        string
		attempt     int
		wantVerdict retryVerdict
		wantWait    time.Duration
		// wantMaxWait bounds a jittered wait instead of pinning it.
		wantMaxWait time.Duration
		// transient is the body check the GraphQL call site supplies; the REST
		// call sites supply none, so it is nil unless a case says otherwise.
		transient transientBody
	}{
		{
			name:        "bare forbidden is fatal",
			status:      http.StatusForbidden,
			attempt:     1,
			wantVerdict: retryNone,
		},
		{
			name:        "bare too many requests is fatal",
			status:      http.StatusTooManyRequests,
			attempt:     1,
			wantVerdict: retryNone,
		},
		{
			name:        "not found is fatal",
			status:      http.StatusNotFound,
			attempt:     1,
			wantVerdict: retryNone,
		},
		{
			name:        "ok is not a retry",
			status:      http.StatusOK,
			attempt:     1,
			wantVerdict: retryNone,
		},
		{
			name:        "forbidden with retry-after waits the delta",
			status:      http.StatusForbidden,
			header:      map[string]string{"Retry-After": "17"},
			attempt:     1,
			wantVerdict: retryBackoff,
			wantWait:    17 * time.Second,
		},
		{
			name:        "retry-after may be an http date",
			status:      http.StatusForbidden,
			header:      map[string]string{"Retry-After": theInstant.Add(30 * time.Second).Format(http.TimeFormat)},
			attempt:     1,
			wantVerdict: retryBackoff,
			wantWait:    30 * time.Second,
		},
		{
			name:   "an exhausted budget waits for the reset",
			status: http.StatusTooManyRequests,
			header: map[string]string{
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Reset":     resetIn(45 * time.Second),
			},
			attempt:     1,
			wantVerdict: retryBackoff,
			wantWait:    45 * time.Second,
		},
		{
			name:   "retry-after wins over the reset",
			status: http.StatusForbidden,
			header: map[string]string{
				"Retry-After":           "5",
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Reset":     resetIn(45 * time.Second),
			},
			attempt:     1,
			wantVerdict: retryBackoff,
			wantWait:    5 * time.Second,
		},
		{
			name:   "a reset already past waits not at all",
			status: http.StatusForbidden,
			header: map[string]string{
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Reset":     resetIn(-time.Minute),
			},
			attempt:     1,
			wantVerdict: retryBackoff,
			wantWait:    0,
		},
		{
			name:   "a reset beyond the cap is fatal",
			status: http.StatusForbidden,
			header: map[string]string{
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Reset":     resetIn(40 * time.Minute),
			},
			attempt:     1,
			wantVerdict: retryTooLong,
		},
		{
			name:        "a retry-after beyond the cap is fatal",
			status:      http.StatusTooManyRequests,
			header:      map[string]string{"Retry-After": "3600"},
			attempt:     1,
			wantVerdict: retryTooLong,
		},
		{
			name:        "an unparsable retry-after still marks a rate limit",
			status:      http.StatusForbidden,
			header:      map[string]string{"Retry-After": "soon"},
			attempt:     1,
			wantVerdict: retryBackoff,
			wantMaxWait: retryBaseDelay,
		},
		{
			name:        "a gateway error backs off exponentially",
			status:      http.StatusBadGateway,
			attempt:     3,
			wantVerdict: retryBackoff,
			wantMaxWait: 4 * retryBaseDelay,
		},
		{
			name:        "a server error is retryable",
			status:      http.StatusInternalServerError,
			attempt:     1,
			wantVerdict: retryBackoff,
			wantMaxWait: retryBaseDelay,
		},
		{
			name:        "service unavailable is retryable",
			status:      http.StatusServiceUnavailable,
			attempt:     1,
			wantVerdict: retryBackoff,
			wantMaxWait: retryBaseDelay,
		},
		{
			name:        "gateway timeout is retryable",
			status:      http.StatusGatewayTimeout,
			attempt:     1,
			wantVerdict: retryBackoff,
			wantMaxWait: retryBaseDelay,
		},
		{
			name:        "a graphql rate limit is a retryable 200",
			status:      http.StatusOK,
			header:      map[string]string{"X-RateLimit-Reset": resetIn(20 * time.Second)},
			body:        `{"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`,
			attempt:     1,
			transient:   graphQLRateLimited,
			wantVerdict: retryBackoff,
			wantWait:    20 * time.Second,
		},
		{
			name:        "a graphql rate limit with no reset backs off",
			status:      http.StatusOK,
			body:        `{"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`,
			attempt:     1,
			transient:   graphQLRateLimited,
			wantVerdict: retryBackoff,
			wantMaxWait: retryBaseDelay,
		},
		{
			name:        "a graphql reset beyond the cap is fatal",
			status:      http.StatusOK,
			header:      map[string]string{"X-RateLimit-Reset": resetIn(50 * time.Minute)},
			body:        `{"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`,
			attempt:     1,
			transient:   graphQLRateLimited,
			wantVerdict: retryTooLong,
		},
		{
			name:        "another graphql error type is fatal",
			status:      http.StatusOK,
			header:      map[string]string{"X-RateLimit-Reset": resetIn(20 * time.Second)},
			body:        `{"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a Repository"}]}`,
			attempt:     1,
			transient:   graphQLRateLimited,
			wantVerdict: retryNone,
		},
		{
			name:        "a rate limit mixed with another error is fatal",
			status:      http.StatusOK,
			body:        `{"errors":[{"type":"RATE_LIMITED"},{"type":"FORBIDDEN"}]}`,
			attempt:     1,
			transient:   graphQLRateLimited,
			wantVerdict: retryNone,
		},
		{
			name:        "a graphql answer is not a retry",
			status:      http.StatusOK,
			body:        `{"data":{"repository":{}}}`,
			attempt:     1,
			transient:   graphQLRateLimited,
			wantVerdict: retryNone,
		},
		{
			name:        "a rate limit on a call with no body check is an answer",
			status:      http.StatusOK,
			body:        `{"errors":[{"type":"RATE_LIMITED"}]}`,
			attempt:     1,
			wantVerdict: retryNone,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := newRetryTestClient(t, "https://example.invalid")
			header := make(http.Header, len(tc.header))
			for k, v := range tc.header {
				header.Set(k, v)
			}

			resp := &response{status: tc.status, header: header, body: []byte(tc.body)}
			gotWait, gotVerdict := c.retryWait(resp, tc.attempt, tc.transient)

			if gotVerdict != tc.wantVerdict {
				t.Fatalf("retryWait verdict = %v, want %v", gotVerdict, tc.wantVerdict)
			}
			switch {
			case tc.wantVerdict != retryBackoff:
				return
			case tc.wantMaxWait > 0:
				if gotWait < 0 || gotWait > tc.wantMaxWait {
					t.Errorf("retryWait = %v, want within [0, %v]", gotWait, tc.wantMaxWait)
				}
			default:
				if gotWait != tc.wantWait {
					t.Errorf("retryWait = %v, want %v", gotWait, tc.wantWait)
				}
			}
		})
	}
}

// TestClient_do_retriesUntilItSucceeds is the whole point of the policy: a
// transient answer costs a wait, not the run.
func TestClient_do_retriesUntilItSucceeds(t *testing.T) {
	t.Parallel()

	srv := newCountingServer()
	server := httptest.NewServer(srv.handle([]scriptedResponse{
		{status: http.StatusBadGateway, body: `{"message":"Bad gateway"}`},
		{status: http.StatusForbidden, header: map[string]string{"Retry-After": "2"}, body: `{"message":"slow down"}`},
		{status: http.StatusOK, body: `{"ok":true}`},
	}))
	t.Cleanup(server.Close)

	c, rec := newRetryTestClient(t, server.URL)

	resp, err := c.do(t.Context(), http.MethodGet, server.URL+"/anything", nil)
	if err != nil {
		t.Fatalf("do() error = %v, want nil", err)
	}
	if resp.status != http.StatusOK {
		t.Errorf("do() status = %d, want %d", resp.status, http.StatusOK)
	}
	if got := srv.seen(); got != 3 {
		t.Errorf("server saw %d requests, want 3", got)
	}
	waits := rec.recorded()
	if len(waits) != 2 {
		t.Fatalf("waited %d times, want 2", len(waits))
	}
	if waits[1] != 2*time.Second {
		t.Errorf("second wait = %v, want the 2s Retry-After asked for", waits[1])
	}
}

// TestClient_do_doesNotRetryABareForbidden is the discriminator seen from the
// outside: a token that lacks a scope fails once, immediately.
func TestClient_do_doesNotRetryABareForbidden(t *testing.T) {
	t.Parallel()

	srv := newCountingServer()
	server := httptest.NewServer(srv.handle([]scriptedResponse{
		{status: http.StatusForbidden, body: `{"message":"Resource not accessible"}`},
	}))
	t.Cleanup(server.Close)

	c, rec := newRetryTestClient(t, server.URL)

	resp, err := c.do(t.Context(), http.MethodGet, server.URL+"/anything", nil)
	if err != nil {
		t.Fatalf("do() error = %v, want the response", err)
	}
	if resp.status != http.StatusForbidden {
		t.Errorf("do() status = %d, want %d", resp.status, http.StatusForbidden)
	}
	if got := srv.seen(); got != 1 {
		t.Errorf("server saw %d requests, want 1", got)
	}
	if got := len(rec.recorded()); got != 0 {
		t.Errorf("waited %d times, want 0", got)
	}
}

// TestClient_do_givesUpAfterTheBudget pins the shape of the give-up: the
// attempt count is in the message, because a reader of a failed nightly run
// needs to know the retries happened and did not help.
func TestClient_do_givesUpAfterTheBudget(t *testing.T) {
	t.Parallel()

	srv := newCountingServer()
	server := httptest.NewServer(srv.handle([]scriptedResponse{
		{status: http.StatusServiceUnavailable, body: `{"message":"Server Error"}`},
	}))
	t.Cleanup(server.Close)

	c, _ := newRetryTestClient(t, server.URL)

	_, err := c.do(t.Context(), http.MethodGet, server.URL+"/anything", nil)
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("do() error = %v, want ErrUnexpectedStatus", err)
	}
	if got := srv.seen(); got != maxAttempts {
		t.Errorf("server saw %d requests, want %d", got, maxAttempts)
	}
	for _, want := range []string{strconv.Itoa(maxAttempts), "Server Error"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// TestClient_do_failsWhenTheResetIsTooFarOut covers the deliberate refusal to
// wait out a primary rate limit: sleeping forty minutes inside an unattended
// nightly run is worse than failing and letting the next one refresh.
func TestClient_do_failsWhenTheResetIsTooFarOut(t *testing.T) {
	t.Parallel()

	srv := newCountingServer()
	server := httptest.NewServer(srv.handle([]scriptedResponse{{
		status: http.StatusForbidden,
		header: map[string]string{"Retry-After": "3600"},
		body:   `{"message":"API rate limit exceeded"}`,
	}}))
	t.Cleanup(server.Close)

	c, rec := newRetryTestClient(t, server.URL)

	_, err := c.do(t.Context(), http.MethodGet, server.URL+"/anything", nil)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("do() error = %v, want ErrRateLimited", err)
	}
	if got := srv.seen(); got != 1 {
		t.Errorf("server saw %d requests, want 1", got)
	}
	if got := len(rec.recorded()); got != 0 {
		t.Errorf("waited %d times, want 0", got)
	}
}

// TestClient_do_retriesATransportFailure covers the failure that never reaches
// a status: a connection dropped mid-answer is as transient as a 502 and is
// the other way a nightly run dies for a reason that is not the code's.
func TestClient_do_retriesATransportFailure(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			// Aborts the connection without a status and without logging it.
			panic(http.ErrAbortHandler)
		}
		if _, err := w.Write([]byte(`{"ok":true}`)); err != nil {
			return
		}
	}))
	t.Cleanup(server.Close)

	c, _ := newRetryTestClient(t, server.URL)

	resp, err := c.do(t.Context(), http.MethodGet, server.URL+"/anything", nil)
	if err != nil {
		t.Fatalf("do() error = %v, want nil", err)
	}
	if resp.status != http.StatusOK {
		t.Errorf("do() status = %d, want %d", resp.status, http.StatusOK)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("server saw %d requests, want 2", calls)
	}
}

// TestClient_do_resendsTheBody covers the one POST this client makes: a retried
// GraphQL query has to carry its document again, and a reader consumed by the
// first attempt would send an empty one.
func TestClient_do_resendsTheBody(t *testing.T) {
	t.Parallel()

	srv := newCountingServer()
	server := httptest.NewServer(srv.handle([]scriptedResponse{
		{status: http.StatusBadGateway, body: `{"message":"Bad gateway"}`},
		{status: http.StatusOK, body: `{"data":{}}`},
	}))
	t.Cleanup(server.Close)

	c, _ := newRetryTestClient(t, server.URL)
	document := `{"query":"query { viewer { login } }"}`

	if _, err := c.do(t.Context(), http.MethodPost, server.URL+graphQLPath, []byte(document)); err != nil {
		t.Fatalf("do() error = %v, want nil", err)
	}

	bodies := srv.sentBodies()
	if len(bodies) != 2 {
		t.Fatalf("server saw %d requests, want 2", len(bodies))
	}
	for i, got := range bodies {
		if got != document {
			t.Errorf("attempt %d sent %q, want %q", i+1, got, document)
		}
	}
}

// rateLimitedBody is what GitHub's GraphQL endpoint answers a rate limit with:
// a 200, and the news in the body.
const rateLimitedBody = `{"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`

// resetIn renders an X-RateLimit-Reset d from the pinned clock.
func resetIn(d time.Duration) string {
	return strconv.FormatInt(theInstant.Add(d).Unix(), 10)
}

// TestClient_fetchRepository_retriesAGraphQLRateLimit is the failure the status
// code cannot see: /graphql reports its rate limit as a 200, so the answer that
// would otherwise fail the nightly refresh has to be read out of the body.
func TestClient_fetchRepository_retriesAGraphQLRateLimit(t *testing.T) {
	t.Parallel()

	srv := newCountingServer()
	server := httptest.NewServer(srv.handle([]scriptedResponse{
		{
			status: http.StatusOK,
			header: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": resetIn(30 * time.Second)},
			body:   rateLimitedBody,
		},
		{status: http.StatusOK, body: `{"data":{"repository":{"visibility":"PUBLIC"}}}`},
	}))
	t.Cleanup(server.Close)

	c, rec := newRetryTestClient(t, server.URL)

	repo, err := c.fetchRepository(t.Context(), "shell-scripts")
	if err != nil {
		t.Fatalf("fetchRepository() error = %v, want nil", err)
	}
	if repo.Visibility != "PUBLIC" {
		t.Errorf("Visibility = %q, want the second answer's PUBLIC", repo.Visibility)
	}
	if got := srv.seen(); got != 2 {
		t.Errorf("server saw %d requests, want 2", got)
	}
	waits := rec.recorded()
	if len(waits) != 1 {
		t.Fatalf("waited %d times, want 1", len(waits))
	}
	if waits[0] != 30*time.Second {
		t.Errorf("wait = %v, want the 30s X-RateLimit-Reset asked for", waits[0])
	}
}

// TestClient_fetchRepository_doesNotRetryAnotherGraphQLError is the body-level
// discriminator: only a rate limit is worth asking again for, because a
// NOT_FOUND answers the same however many times it is asked.
func TestClient_fetchRepository_doesNotRetryAnotherGraphQLError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{
			name: "a lone not-found",
			body: `{"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a Repository"}]}`,
		},
		{
			// Waiting could clear the limit but never the FORBIDDEN, so the
			// whole budget would be spent to fail anyway.
			name: "a rate limit alongside another error",
			body: `{"errors":[{"type":"RATE_LIMITED","message":"slow down"},{"type":"FORBIDDEN","message":"no"}]}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := newCountingServer()
			server := httptest.NewServer(srv.handle([]scriptedResponse{{status: http.StatusOK, body: tc.body}}))
			t.Cleanup(server.Close)

			c, rec := newRetryTestClient(t, server.URL)

			_, err := c.fetchRepository(t.Context(), "shell-scripts")
			if !errors.Is(err, errGraphQL) {
				t.Fatalf("fetchRepository() error = %v, want errGraphQL", err)
			}
			if got := srv.seen(); got != 1 {
				t.Errorf("server saw %d requests, want 1", got)
			}
			if got := len(rec.recorded()); got != 0 {
				t.Errorf("waited %d times, want 0", got)
			}
		})
	}
}

// TestClient_fetchRepository_givesUpOnAPersistentRateLimit pins what a limit
// that outlasts the budget is reported as. It is not an unexpected status: the
// status was 200 every time, and saying so would send the reader of a failed
// nightly run looking for the wrong thing.
func TestClient_fetchRepository_givesUpOnAPersistentRateLimit(t *testing.T) {
	t.Parallel()

	srv := newCountingServer()
	server := httptest.NewServer(srv.handle([]scriptedResponse{{status: http.StatusOK, body: rateLimitedBody}}))
	t.Cleanup(server.Close)

	c, _ := newRetryTestClient(t, server.URL)

	_, err := c.fetchRepository(t.Context(), "shell-scripts")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("fetchRepository() error = %v, want ErrRateLimited", err)
	}
	if errors.Is(err, ErrUnexpectedStatus) {
		t.Errorf("error = %q, want it not to blame the 200 it was given", err)
	}
	if got := srv.seen(); got != maxAttempts {
		t.Errorf("server saw %d requests, want %d", got, maxAttempts)
	}
}

// TestClient_fetchRepository_doesNotWaitOutAPrimaryLimit is the same refusal a
// REST rate limit gets: an hour is longer than an unattended nightly run
// should sit, and the next run refreshes the report anyway.
func TestClient_fetchRepository_doesNotWaitOutAPrimaryLimit(t *testing.T) {
	t.Parallel()

	srv := newCountingServer()
	server := httptest.NewServer(srv.handle([]scriptedResponse{{
		status: http.StatusOK,
		header: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": resetIn(time.Hour)},
		body:   rateLimitedBody,
	}}))
	t.Cleanup(server.Close)

	c, rec := newRetryTestClient(t, server.URL)

	_, err := c.fetchRepository(t.Context(), "shell-scripts")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("fetchRepository() error = %v, want ErrRateLimited", err)
	}
	if got := srv.seen(); got != 1 {
		t.Errorf("server saw %d requests, want 1", got)
	}
	if got := len(rec.recorded()); got != 0 {
		t.Errorf("waited %d times, want 0", got)
	}
}

// TestClient_do_stopsWaitingWhenTheContextIsCancelled is why the wait is a
// select and not a sleep: a SIGINT during a sixty-second backoff has to exit
// now, not in sixty seconds.
func TestClient_do_stopsWaitingWhenTheContextIsCancelled(t *testing.T) {
	t.Parallel()

	srv := newCountingServer()
	server := httptest.NewServer(srv.handle([]scriptedResponse{{
		status: http.StatusForbidden,
		header: map[string]string{"Retry-After": "60"},
		body:   `{"message":"slow down"}`,
	}}))
	t.Cleanup(server.Close)

	// The real wait, not the recorder: the timer is what is under test.
	c, _ := newRetryTestClient(t, server.URL)
	c.sleep = waitFor

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := c.do(ctx, http.MethodGet, server.URL+"/anything", nil); done <- err }()

	// One answer means the client is inside its backoff.
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	select {
	case <-srv.answered:
	case <-deadline.C:
		t.Fatal("the client never sent its first request")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("do() error = %v, want context.Canceled", err)
		}
	case <-deadline.C:
		t.Fatal("do() waited out its backoff instead of giving up with the context")
	}
}

// TestClient_do_retriesATruncatedBody covers the answer that arrives with a
// good status and then stops: GitHub sets a Content-Length and the connection
// drops before the body is complete. Reading it fails, and a half-read body
// must never be handed on as an answer — the status was 200, so nothing later
// in the pipeline would know to distrust it.
func TestClient_do_retriesATruncatedBody(t *testing.T) {
	t.Parallel()

	srv := newCountingServer()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		srv.mu.Lock()
		srv.calls++
		srv.mu.Unlock()
		w.Header().Set("Content-Length", "64")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"partial":`)); err != nil {
			return
		}
		// Flush first, so the client has the status and the start of the body
		// before the connection goes: without this the whole response is lost
		// and the failure is a transport error rather than a failed read.
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		flusher.Flush()
		// Abandon the response mid-body, which is what a dropped connection
		// looks like to the client: an unexpected EOF short of Content-Length.
		panic(http.ErrAbortHandler)
	}))
	t.Cleanup(server.Close)

	c, rec := newRetryTestClient(t, server.URL)

	_, err := c.do(t.Context(), http.MethodGet, server.URL+"/repos/someone/shell-scripts", nil)
	if err == nil {
		t.Fatal("do() error = nil, want the truncated read reported")
	}
	if !strings.Contains(err.Error(), "read response") {
		t.Errorf("do() error = %q, want it to name the failed read", err)
	}
	// A dropped connection is transient, so the budget is spent before giving
	// up rather than failing the whole run on one bad socket.
	if got := srv.seen(); got != maxAttempts {
		t.Errorf("server saw %d requests, want %d", got, maxAttempts)
	}
	if got := len(rec.recorded()); got != maxAttempts-1 {
		t.Errorf("waited %d times, want %d", got, maxAttempts-1)
	}
}
