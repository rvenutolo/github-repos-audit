// Package github reads live GitHub and returns audit.Repo values. It decides
// nothing: every field it fills is an answer GitHub gave, and internal/rules
// is what turns those answers into verdicts.
//
// The client is read-only by construction. Every call is a GET except the
// single POST to /graphql, which GitHub's GraphQL endpoint requires because it
// accepts no other verb, and every document sent there is a query rather than
// a mutation. Everything that writes to GitHub lives in the refresh workflow,
// which keeps the collector provably read-only.
//
// Several responses that look like failures are ordinary answers and are
// treated as such: a private repository omits security_and_analysis, answers
// 404 from private-vulnerability-reporting, and a public one answers 422 from
// the Actions access endpoint. Any status outside that documented set aborts
// the whole run, because a field GitHub declined to answer is not a field that
// is missing and rendering it as a gap would invent one.
//
// Two conditions are neither answers nor failures and are waited out instead:
// a rate limit, which a six-wide fan-out issuing nine calls per repository is
// built to trigger, and a 5xx. Both are resent a few times with backoff, so
// the unattended nightly refresh comes back for the answer rather than going
// red for a reason that has nothing to do with the code. A rate limit on
// /graphql arrives as a 200 whose body carries a RATE_LIMITED error rather
// than as a status, so that one endpoint hands the retry loop a way to read
// its body; the wait it earns is the same one a 429 earns.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the root of GitHub's public API. Both the REST endpoints
// and /graphql hang off it, so one base URL is enough to point the whole
// client at an httptest server.
const DefaultBaseURL = "https://api.github.com"

// DefaultTimeout bounds one request, not the whole run: the run's deadline is
// the caller's context.
const DefaultTimeout = 30 * time.Second

// fetchLimit is how many repositories are collected at once.
const fetchLimit = 6

// apiVersion pins the REST schema, so a future default cannot silently reshape
// a response this package parses.
const apiVersion = "2022-11-28"

// userAgent identifies this tool in GitHub's logs.
const userAgent = "github-repos-audit"

// maxBodyBytes bounds a single response. The largest answer here is a few
// kilobytes of GraphQL, so this only exists to keep a misbehaving endpoint
// from being read into memory without limit.
const maxBodyBytes = 8 << 20

// maxSnippetRunes is how much of an unexpected body an error quotes.
const maxSnippetRunes = 200

// redacted is what a Secret renders as, everywhere.
const redacted = "[redacted]"

// ErrUnexpectedStatus reports a status this package has no reading for. Every
// such response aborts the run and leaves the report untouched.
var ErrUnexpectedStatus = errors.New("unexpected status")

// errGraphQL reports that GitHub answered 200 with a non-empty errors array,
// which is how the GraphQL endpoint reports a failure.
var errGraphQL = errors.New("graphql")

// ErrConfig reports a Client that cannot be built from the given Options.
var ErrConfig = errors.New("invalid client options")

// ErrRateLimited reports a rate limit whose reset is further out than this run
// is willing to wait for.
var ErrRateLimited = errors.New("rate limited")

// maxAttempts is how many times one request is sent before the run fails: the
// first send plus three retries.
const maxAttempts = 4

// retryBaseDelay is the first exponential backoff wait; each further attempt
// doubles it before jitter.
const retryBaseDelay = time.Second

// maxRetryWait caps one backoff. A rate limit that resets further out than
// this is not waited for: that is a primary limit tens of minutes away, and
// sleeping through it inside an unattended nightly run is worse than failing
// and letting the next run refresh the report.
const maxRetryWait = 2 * time.Minute

// transientBody reports whether a 200 body is a transient answer rather than a
// usable one. It is the seam GitHub's GraphQL rate limit needs: that limit is
// reported as a 200 carrying a RATE_LIMITED error, so the status the rest of
// the policy keys on cannot see it. A nil transientBody means every 200 is an
// answer, which is what every REST call passes.
type transientBody func([]byte) bool

// retryVerdict is what the retry policy made of one answer.
type retryVerdict int

const (
	// retryNone is an answer to hand to the caller as it stands, whether it is
	// a 200 or a status the caller will abort on.
	retryNone retryVerdict = iota
	// retryBackoff is a transient answer to resend after a wait.
	retryBackoff
	// retryTooLong is a rate limit whose reset is beyond maxRetryWait.
	retryTooLong
)

// Secret is an API token that refuses to print itself. It implements both
// fmt.Stringer and slog.LogValuer, so neither a log line nor an error message
// can carry the token by accident; Reveal is the one way to the real value and
// is called only when building the Authorization header.
type Secret string

// Both interfaces are what keep the token out of logs and error messages, so
// the compiler checks they are still implemented.
var (
	_ slog.LogValuer = Secret("")
	_ fmt.Stringer   = Secret("")
)

// String renders the redaction rather than the token.
func (s Secret) String() string { return redacted }

// LogValue renders the redaction rather than the token.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// Reveal returns the token itself. Every call site is a place the token has to
// leave the process, which is exactly one: the Authorization header.
func (s Secret) Reveal() string { return string(s) }

// Options configures a Client. Token is required, and so is Owner for New;
// Viewer ignores Owner. The rest have defaults that suit the real API.
type Options struct {
	// Owner is the account whose repositories are read.
	Owner string
	// Token authenticates every request.
	Token Secret
	// BaseURL is the API root. Empty means DefaultBaseURL; a test points it at
	// an httptest server.
	BaseURL string
	// HTTPClient is the client every request goes through. Empty means one
	// constructed with DefaultTimeout. http.DefaultClient is never used: it
	// has no timeout, and a hung GitHub would hang the daily refresh forever.
	HTTPClient *http.Client
	// Logger receives diagnostics. Empty means slog.Default().
	Logger *slog.Logger
}

// Client reads repositories from GitHub. It is safe for concurrent use, which
// is what lets Collect fan out.
type Client struct {
	owner   string
	token   Secret
	baseURL string
	http    *http.Client
	log     *slog.Logger
	// now and sleep are the retry policy's two seams. They are fields rather
	// than direct calls to time.Now and waitFor so a test can pin the clock a
	// Retry-After date is read against, and assert a sixty-second backoff
	// without spending sixty seconds.
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

// response is one answer from GitHub, already fully read. Holding the body as
// bytes rather than a stream is what lets do drain and close it before
// returning, so the connection goes back to the pool on every path.
type response struct {
	status int
	header http.Header
	body   []byte
}

// New builds a Client from opts.
func New(opts Options) (*Client, error) {
	if opts.Owner == "" {
		return nil, fmt.Errorf("%w: no owner", ErrConfig)
	}
	return newClient(opts)
}

// newClient builds a Client without requiring an owner. Only Viewer uses it
// directly, because naming the owner is what Viewer is for.
func newClient(opts Options) (*Client, error) {
	if opts.Token == "" {
		return nil, fmt.Errorf("%w: no token", ErrConfig)
	}

	base := opts.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("%w: base url %q: %w", ErrConfig, base, err)
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Client{
		owner:   opts.Owner,
		token:   opts.Token,
		baseURL: base,
		http:    httpClient,
		log:     logger,
		now:     time.Now,
		sleep:   waitFor,
	}, nil
}

// Owner is the account this client reads.
func (c *Client) Owner() string { return c.owner }

// do sends one request and returns the whole answer, resending it while GitHub
// answers with something transient: a rate limit carrying the headers that say
// so, or a 5xx. A bare 403 is not transient — it is a token without a scope,
// and resending it is a hang that looks like a stall — so it comes straight
// back to the caller. Retrying is safe on every call this client makes,
// including the one POST, because the document that POST carries is a query.
func (c *Client) do(ctx context.Context, method, rawURL string, body []byte) (*response, error) {
	return c.doRetrying(ctx, method, rawURL, body, nil)
}

// doRetrying is do with one addition: transient, when it is not nil, decides
// whether a 200 is an answer or a limit to wait out. It is one loop rather
// than a second one at the caller because the budget and the backoff are the
// policy, and a caller looping over a call that already retries would quietly
// multiply both.
func (c *Client) doRetrying(
	ctx context.Context, method, rawURL string, body []byte, transient transientBody,
) (*response, error) {
	req, err := c.newRequest(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}

	for attempt := 1; ; attempt++ {
		resp, sendErr := c.send(ctx, req, body)

		var wait time.Duration
		if sendErr != nil {
			if !retryableSendErr(ctx, sendErr) {
				return nil, sendErr
			}
			wait = c.backoff(attempt)
		} else {
			var verdict retryVerdict
			wait, verdict = c.retryWait(resp, attempt, transient)
			switch verdict {
			case retryNone:
				return resp, nil
			case retryTooLong:
				return nil, fmt.Errorf("%s %s: %w: GitHub asks for %s, beyond the %s this run will wait: %s",
					method, rawURL, ErrRateLimited, wait.Round(time.Second), maxRetryWait, snippet(resp.body))
			case retryBackoff:
			}
		}

		if attempt >= maxAttempts {
			if sendErr != nil {
				return nil, fmt.Errorf("%s %s: after %d attempts: %w", method, rawURL, attempt, sendErr)
			}
			// The only 200 that reaches the budget is one transient called a
			// limit, and reporting that as an unexpected status 200 would send
			// a reader of a failed nightly run looking for the wrong thing.
			if resp.status == http.StatusOK {
				return nil, fmt.Errorf("%s %s: %w after %d attempts: %s",
					method, rawURL, ErrRateLimited, attempt, snippet(resp.body))
			}
			return nil, fmt.Errorf("%s %s: %w %d after %d attempts: %s",
				method, rawURL, ErrUnexpectedStatus, resp.status, attempt, snippet(resp.body))
		}

		if sendErr != nil {
			c.log.InfoContext(ctx, "retrying after a transport failure",
				"method", method, "url", rawURL, "attempt", attempt, "wait", wait, "err", sendErr)
		} else {
			c.log.InfoContext(ctx, "retrying after a transient answer",
				"method", method, "url", rawURL, "status", resp.status, "attempt", attempt, "wait", wait)
		}
		if err := c.sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

// newRequest builds the request every attempt is a clone of. It is built once
// because nothing about it changes between attempts except the body reader,
// and a header set per attempt is a header that can differ between them.
func (c *Client) newRequest(ctx context.Context, method, rawURL string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", method, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token.Reveal())
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// send makes one attempt and returns the whole answer. The body is read under
// a limit, drained and closed here rather than by the caller, because a caller
// that returns early on a status check is exactly how a connection leaks. The
// request is cloned and its body reader rebuilt, so a retried POST carries its
// document again rather than an empty reader the first attempt consumed.
func (c *Client) send(ctx context.Context, req *http.Request, body []byte) (*response, error) {
	attempt := req.Clone(ctx)
	if body != nil {
		attempt.Body = io.NopCloser(bytes.NewReader(body))
	}

	resp, err := c.http.Do(attempt)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	read, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	// ReadAll stopped at the limit rather than at EOF if the body was larger,
	// so drain the remainder to keep the connection reusable.
	if _, derr := io.Copy(io.Discard, resp.Body); derr != nil {
		c.log.DebugContext(ctx, "drain response body", "err", derr)
	}

	return &response{status: resp.StatusCode, header: resp.Header, body: read}, nil
}

// retryWait reports how long to wait before resending resp's request, and
// whether to resend it at all.
func (c *Client) retryWait(resp *response, attempt int, transient transientBody) (time.Duration, retryVerdict) {
	switch resp.status {
	case http.StatusOK:
		// A 200 is an answer unless the caller can read one that is not. The
		// GraphQL rate limit is the only one there is, and it carries the same
		// headers a 429 does, so it earns the same wait.
		if transient == nil || !transient(resp.body) {
			return 0, retryNone
		}
		return c.rateLimitWait(resp.header, attempt)
	case http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return c.backoff(attempt), retryBackoff
	case http.StatusForbidden, http.StatusTooManyRequests:
	default:
		return 0, retryNone
	}

	// The discriminator: GitHub reports both a secondary rate limit and a
	// missing scope as 403, and only the rate limit says when to come back.
	if resp.header.Get("Retry-After") == "" && resp.header.Get("X-RateLimit-Remaining") != "0" {
		return 0, retryNone
	}
	return c.rateLimitWait(resp.header, attempt)
}

// rateLimitWait is how a known rate limit is waited out, whichever way GitHub
// reported it: the time it named, backoff when it named none, and retryTooLong
// when the time it named is further out than this run will sit through.
func (c *Client) rateLimitWait(h http.Header, attempt int) (time.Duration, retryVerdict) {
	wait, ok := c.indicatedWait(h)
	if !ok {
		return c.backoff(attempt), retryBackoff
	}
	if wait > maxRetryWait {
		return wait, retryTooLong
	}
	return max(wait, 0), retryBackoff
}

// indicatedWait reads how long GitHub asked to be left alone for: Retry-After
// as delta-seconds or an HTTP date, else the epoch in X-RateLimit-Reset. A
// wait already in the past is returned as it is and clamped by the caller.
func (c *Client) indicatedWait(h http.Header) (time.Duration, bool) {
	if v := h.Get("Retry-After"); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil {
			return time.Duration(seconds) * time.Second, true
		}
		if at, err := http.ParseTime(v); err == nil {
			return at.Sub(c.now()), true
		}
	}
	if v := h.Get("X-RateLimit-Reset"); v != "" {
		if epoch, err := strconv.ParseInt(v, 10, 64); err == nil {
			return time.Unix(epoch, 0).Sub(c.now()), true
		}
	}
	return 0, false
}

// backoff is the wait for an answer that named no time of its own: exponential
// in the attempt, with full jitter so six concurrent fetches that hit the same
// limit do not come back in step and trigger it again.
func (c *Client) backoff(attempt int) time.Duration {
	delay := retryBaseDelay << (attempt - 1)
	//nolint:gosec // G404: jitter spreading retries, not a value anything trusts
	return rand.N(min(delay, maxRetryWait))
}

// retryableSendErr reports whether an error from the transport is worth
// another attempt. A cancelled or expired context is the caller giving up and
// never is; anything else is a connection this run may get a second time.
func retryableSendErr(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// waitFor blocks for d, or until ctx is done, whichever comes first. It is a
// select on a timer rather than a sleep so a SIGINT during a minute-long
// backoff exits now rather than in a minute.
func waitFor(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// getJSON issues a GET against path, which is relative to the base URL, and
// decodes a 200 body into out. Statuses listed in allowed are returned to the
// caller without decoding, because several of them are ordinary answers; every
// other non-200 is ErrUnexpectedStatus and aborts the run.
func (c *Client) getJSON(ctx context.Context, path string, out any, allowed ...int) (*response, error) {
	resp, err := c.do(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if resp.status != http.StatusOK {
		if !slices.Contains(allowed, resp.status) {
			return nil, fmt.Errorf("%s: %w %d: %s", path, ErrUnexpectedStatus, resp.status, snippet(resp.body))
		}
		return resp, nil
	}
	if out != nil {
		if err := json.Unmarshal(resp.body, out); err != nil {
			return nil, fmt.Errorf("%s: decode response: %w", path, err)
		}
	}
	return resp, nil
}

// snippet renders an unexpected body compactly enough to belong in an error
// message. GitHub's error bodies are short JSON objects and carry no
// credential, so quoting one is safe and is usually the whole diagnosis.
func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	r := []rune(s)
	if len(r) > maxSnippetRunes {
		return string(r[:maxSnippetRunes]) + "…"
	}
	return s
}

// compareFold orders names the way the report does: case-insensitively, with
// the case-sensitive comparison as a tiebreak so that two names differing only
// in case still have one fixed order rather than whichever the sort happened
// to produce.
func compareFold(a, b string) int {
	if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// sortedUnique returns in sorted with duplicates removed, leaving nil alone so
// an absent list stays absent in audit.json rather than becoming an empty one.
func sortedUnique(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}
