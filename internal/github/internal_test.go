package github

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests live in the package rather than beside the exported tests
// because the seams they need — the injected gh fallback, the Link-header
// parser the fuzzer drives directly and the one helper every REST read goes
// through — are deliberately unexported: none belongs in the API, and
// reaching the gh branch any other way would need a gh binary on PATH and a
// process-global environment, which no parallel test may have. Everything
// the parser and the helper do that a caller can see is asserted through
// Client in the exported tests.

func TestResolveToken(t *testing.T) {
	t.Parallel()

	boom := errors.New("gh: not logged in")

	tests := []struct {
		name    string
		env     map[string]string
		gh      func(context.Context) (string, error)
		want    Secret
		wantErr error
	}{
		{
			name: "environment wins",
			env:  map[string]string{"GITHUB_TOKEN": "from-env"},
			gh:   func(context.Context) (string, error) { return "from-gh", nil },
			want: "from-env",
		},
		{
			name: "environment is trimmed",
			env:  map[string]string{"GITHUB_TOKEN": "  from-env\n"},
			gh:   func(context.Context) (string, error) { return "from-gh", nil },
			want: "from-env",
		},
		{
			name: "falls back to gh when unset",
			env:  map[string]string{},
			gh:   func(context.Context) (string, error) { return "from-gh\n", nil },
			want: "from-gh",
		},
		{
			name: "falls back to gh when blank",
			env:  map[string]string{"GITHUB_TOKEN": "   "},
			gh:   func(context.Context) (string, error) { return "from-gh", nil },
			want: "from-gh",
		},
		{
			name:    "gh failure is reported",
			env:     map[string]string{},
			gh:      func(context.Context) (string, error) { return "", boom },
			wantErr: boom,
		},
		{
			name:    "gh printing nothing is reported",
			env:     map[string]string{},
			gh:      func(context.Context) (string, error) { return "\n", nil },
			wantErr: errNoToken,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			getenv := func(key string) string { return tc.env[key] }
			got, err := resolveToken(t.Context(), getenv, tc.gh)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("resolveToken error = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("resolveToken = %q, want %q", got.Reveal(), tc.want.Reveal())
			}
		})
	}
}

// TestResolveToken_reportsAnInterruptedGhAsCancellation covers Ctrl-C landing
// while gh is running. exec kills the child and reports "signal: killed" as
// an ExitError with no context error in its chain, which would exit 1 rather
// than 130 unless the cancellation is put back.
func TestResolveToken_reportsAnInterruptedGhAsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	gh := func(ctx context.Context) (string, error) {
		// The interrupt arrives while gh is blocked; what comes back is what
		// exec reports for a child killed by its context.
		cancel()
		<-ctx.Done()
		return "", errors.New("signal: killed")
	}

	_, err := resolveToken(ctx, func(string) string { return "" }, gh)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("resolveToken error = %v, want it to wrap context.Canceled", err)
	}
}

// The roles TestGhHelperProcess can take. The mode is a flag rather than
// an environment variable so the test that spawns the child stays parallel:
// setting an environment variable would mean mutating process-global state.
const (
	ghHelperHolder  = "holder"
	ghHelperSleeper = "sleeper"
	ghHelperPrinter = "printer"
	ghHelperFailure = "failure"
)

// ghHelperLifetime bounds how long a helper process lives if nothing kills
// it. Both long-lived roles are killed long before this: the holder by its
// context, the sleeper by the parent test's cleanup.
const ghHelperLifetime = time.Minute

// What the printer and failure roles write. The token is a fixture, not a
// credential: no service has ever issued it.
const (
	ghHelperToken  = "gho_notarealtoken"
	ghHelperStderr = "gh: To get started with GitHub CLI, please run: gh auth login"
)

var (
	ghHelperMode = flag.String("gh-helper-mode", "",
		"internal: the role TestGhHelperProcess takes when run as a child process")
	ghHelperPIDFile = flag.String("gh-helper-pid-file", "",
		"internal: where the holder records its grandchild's pid")
)

// TestRunCapture_returnsAfterWaitDelayWhenAChildHoldsThePipe covers the reason
// runCapture sets WaitDelay. exec kills the child on cancellation but Wait
// blocks on the pipes, not the process, so a descendant that inherited stdout
// and outlived the kill would hang the tool forever. gh itself never does
// this on demand, so the child is this test binary: the holder starts a
// grandchild that keeps stdout open, then is killed with the grandchild still
// holding it. Only WaitDelay gets runCapture back.
func TestRunCapture_returnsAfterWaitDelayWhenAChildHoldsThePipe(t *testing.T) {
	t.Parallel()

	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() {
		_, err := runCapture(ctx, os.Args[0],
			"-test.run=^TestGhHelperProcess$",
			"-gh-helper-mode="+ghHelperHolder,
			"-gh-helper-pid-file="+pidFile,
		)
		done <- err
	}()

	// Wait for the grandchild to exist before interrupting: killing the
	// holder first would leave nothing holding the pipe and prove nothing.
	grandchild := awaitGrandchild(t, pidFile)
	t.Cleanup(func() {
		if p, err := os.FindProcess(grandchild); err == nil {
			_ = p.Kill() //nolint:errcheck // the sleeper may have exited already
		}
	})
	cancel()

	select {
	case err := <-done:
		// The child is killed, so a token never comes back; that the call
		// returns at all is the whole assertion.
		if err == nil {
			t.Error("runCapture() error = nil, want the killed child's error")
		}
	case <-time.After(4 * ghWaitDelay):
		t.Fatal("runCapture() has not returned well past WaitDelay; a descendant holding stdout can hang the tool")
	}
}

// awaitGrandchild blocks until the holder has recorded its grandchild's pid,
// which it does only after the grandchild has inherited stdout.
func awaitGrandchild(t *testing.T, pidFile string) int {
	t.Helper()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()

	for {
		// A partially written file parses as nothing, so a short read simply
		// means waiting for the next tick.
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return pid
			}
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("the holder never recorded its grandchild's pid")
			return 0
		}
	}
}

// TestGhHelperProcess is not a test of anything: it is the child half of
// TestRunCapture_returnsAfterWaitDelayWhenAChildHoldsThePipe, which needs a
// process that keeps stdout open after being killed and cannot get one from
// the devshell. Running the test binary itself keeps that hermetic. An
// ordinary run passes no mode and skips.
func TestGhHelperProcess(t *testing.T) {
	t.Parallel()

	switch *ghHelperMode {
	case ghHelperHolder:
		// The grandchild inherits this process's stdout, which is the pipe
		// runCapture reads, and outlives the kill that ends the holder.
		// WithoutCancel because the grandchild has to outlive this process:
		// the kill that ends the holder is what the parent is testing, and a
		// grandchild that went with it would hold nothing.
		grandchild := exec.CommandContext(context.WithoutCancel(t.Context()), os.Args[0],
			"-test.run=^TestGhHelperProcess$",
			"-gh-helper-mode="+ghHelperSleeper,
		)
		grandchild.Stdout = os.Stdout
		if err := grandchild.Start(); err != nil {
			t.Fatalf("start the grandchild: %v", err)
		}
		pid := strconv.Itoa(grandchild.Process.Pid)
		if err := os.WriteFile(*ghHelperPIDFile, []byte(pid), 0o600); err != nil {
			t.Fatalf("record the grandchild pid: %v", err)
		}
		time.Sleep(ghHelperLifetime)
	case ghHelperSleeper:
		time.Sleep(ghHelperLifetime)
	case ghHelperPrinter:
		// A token as gh prints one: the value, then a newline the caller has
		// to trim.
		fmt.Fprintln(os.Stdout, ghHelperToken)
	case ghHelperFailure:
		// gh explains an expired login on stderr and exits non-zero, which is
		// the pair runCapture has to fold into one error.
		fmt.Fprintln(os.Stderr, ghHelperStderr)
		os.Exit(1)
	default:
		t.Skip("child half of TestRunCapture_returnsAfterWaitDelayWhenAChildHoldsThePipe")
	}
}

// TestRunCapture covers the two answers gh gives that the tool has to read:
// a token on stdout, and a refusal explained on stderr. The child is this test
// binary rather than gh itself, which keeps the test hermetic — the devshell
// has no gh, and reaching the real one would need a process-global PATH that
// no parallel test may set.
func TestRunCapture(t *testing.T) {
	t.Parallel()

	t.Run("stdout comes back whole", func(t *testing.T) {
		t.Parallel()

		out, err := runCapture(t.Context(), os.Args[0],
			"-test.run=^TestGhHelperProcess$",
			"-gh-helper-mode="+ghHelperPrinter,
		)
		if err != nil {
			t.Fatalf("runCapture() error = %v, want nil", err)
		}
		// The trailing newline survives: trimming is resolveToken's job, and
		// a runCapture that trimmed would hide a child printing nothing but
		// whitespace.
		if !strings.HasPrefix(out, ghHelperToken+"\n") {
			t.Errorf("runCapture() = %q, want it to start with the token and a newline", out)
		}
	})

	t.Run("a failed child's stderr is folded into the error", func(t *testing.T) {
		t.Parallel()

		_, err := runCapture(t.Context(), os.Args[0],
			"-test.run=^TestGhHelperProcess$",
			"-gh-helper-mode="+ghHelperFailure,
		)
		if err == nil {
			t.Fatal("runCapture() error = nil, want the child's failure")
		}
		// Without this the caller sees "exit status 1" and nothing about the
		// login that expired, which is the whole reason gh was asked.
		if !strings.Contains(err.Error(), "gh auth login") {
			t.Errorf("runCapture() error = %q, want it to carry the child's stderr", err)
		}
	})
}

// TestResolveToken_needsGetenv covers the programmer error of not injecting an
// environment reader, which would otherwise panic at the first lookup.
func TestResolveToken_needsGetenv(t *testing.T) {
	t.Parallel()

	if _, err := ResolveToken(t.Context(), nil); !errors.Is(err, ErrConfig) {
		t.Errorf("ResolveToken error = %v, want ErrConfig", err)
	}
}

// TestNextPage_rejectsLinksThatWouldLeaveTheBaseURL names the Link targets a
// server could return that are not a path under the client's own base URL.
// Each is answered with "", which the caller already reads as "stop paging":
// a next link this parser cannot reduce to a rooted path is one worth not
// following at all.
func TestNextPage_rejectsLinksThatWouldLeaveTheBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		link string
	}{
		{
			// The crasher fuzzing found: an empty authority leaves a path
			// beginning "//", and appending that to the base URL hands the
			// next parser a host made of what used to be path.
			name: "empty authority",
			link: `<A:////00000000 00000000000>; rel=next`,
		},
		{
			// Concatenation would make this "https://api.github.comrepos".
			name: "relative",
			link: `<repos?page=2>; rel="next"`,
		},
		{
			name: "opaque",
			link: `<mailto:nobody@example.com>; rel="next"`,
		},
		{
			name: "dot-dot segment",
			link: `<https://api.github.com/user/../../repos>; rel="next"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := nextPage(tc.link); got != "" {
				t.Errorf("nextPage(%q) = %q, want \"\"", tc.link, got)
			}
		})
	}
}

// TestNextPage_keepsOnlyPathAndQuery pins the host-stripping the parser
// exists for: whatever host the header names, the client is sent back to its
// own base URL. A scheme-relative target names a host just as an absolute one
// does, so both reduce to the same path.
func TestNextPage_keepsOnlyPathAndQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		link string
		want string
	}{
		{
			name: "absolute",
			link: `<https://evil.example/user/repos?page=2>; rel="next"`,
			want: "/user/repos?page=2",
		},
		{
			name: "scheme-relative",
			link: `<//evil.example/user/repos?page=2>; rel="next"`,
			want: "/user/repos?page=2",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := nextPage(tc.link); got != tc.want {
				t.Errorf("nextPage(%q) = %q, want %q", tc.link, got, tc.want)
			}
		})
	}
}

// FuzzNextPage holds the Link-header parser to its invariants over arbitrary
// input: it never panics, and whatever it returns is either nothing or a
// rooted path that, appended to the base URL, still addresses the base host.
// Parsing alone is too weak an invariant — "//evil.example/x" parses.
func FuzzNextPage(f *testing.F) {
	f.Add("")
	f.Add(`<https://api.github.com/user/repos?per_page=100&page=2>; rel="next", ` +
		`<https://api.github.com/user/repos?per_page=100&page=7>; rel="last"`)
	f.Add(`<https://api.github.com/user/repos?per_page=100&page=1>; rel="prev", ` +
		`<https://api.github.com/user/repos?per_page=100&page=1>; rel="first"`)
	f.Add(`<https://api.github.com/user/repos>; rel="next"`)
	f.Add(`<https://api.github.com/user/repos>`)
	f.Add("<://nope>; rel=\"next\"")
	f.Add(`<//evil.example/user/repos>; rel="next"`)

	const base = "https://api.github.com"

	f.Fuzz(func(t *testing.T, link string) {
		got := nextPage(link)
		if got == "" {
			return
		}
		if !strings.HasPrefix(got, "/") || strings.HasPrefix(got, "//") {
			t.Errorf("nextPage(%q) = %q, want a rooted path", link, got)
			return
		}
		u, err := url.Parse(base + got)
		if err != nil {
			t.Errorf("nextPage(%q) = %q, which does not parse onto %s: %v", link, got, base, err)
			return
		}
		if u.Host != "api.github.com" {
			t.Errorf("nextPage(%q) = %q, which addresses host %q, not the base host", link, got, u.Host)
		}
	})
}

// TestClient_getJSON_returnsNoResponseWithAnError pins the ordinary Go
// contract on the one helper every REST read goes through: a caller that
// checks the error and returns never sees the response, so a non-nil one
// beside an error is a value nobody may rely on and nobody should be handed.
func TestClient_getJSON_returnsNoResponseWithAnError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "unexpected status", status: http.StatusInternalServerError, body: `{"message":"Server Error"}`},
		{name: "undecodable body", status: http.StatusOK, body: `{"message":`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Errorf("write body: %v", err)
				}
			}))
			t.Cleanup(server.Close)
			c, err := New(Options{Owner: "someone", Token: "token", BaseURL: server.URL})
			if err != nil {
				t.Fatalf("New() error = %v, want nil", err)
			}
			// The 500 is retried four times before it aborts, and this test is
			// about what the helper hands back, not about the wait.
			c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }

			var out struct{ Message string }
			resp, err := c.getJSON(t.Context(), "/anything", &out)
			if err == nil {
				t.Fatal("getJSON error = nil, want an error")
			}
			if resp != nil {
				t.Errorf("getJSON response = %+v alongside an error, want nil", resp)
			}
		})
	}
}
