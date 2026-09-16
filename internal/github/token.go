package github

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// tokenEnvVar is the one environment variable this tool reads for credentials.
//
//nolint:gosec // G101: the name of the variable to read, not a credential
const tokenEnvVar = "GITHUB_TOKEN"

// ghHostname is passed to gh explicitly. A bare `gh auth token` answers for
// whichever host gh considers active, which on a machine logged in to a
// GitHub Enterprise instance as well is not necessarily github.com — and the
// wrong token fails as a 404 on every private repository rather than as an
// authentication error, which is a much slower thing to diagnose.
const ghHostname = "github.com"

// ghWaitDelay is how long, once the context is cancelled, runCapture waits
// for the child's pipes to close before closing them itself. Without it a gh
// that has spawned a helper still holding stdout keeps Wait blocked past the
// interrupt.
const ghWaitDelay = 2 * time.Second

// errNoToken reports that neither GITHUB_TOKEN nor gh produced a token.
var errNoToken = errors.New("no github token")

// ResolveToken returns the token every read is made with: GITHUB_TOKEN when
// getenv has it, and otherwise whatever `gh auth token --hostname github.com`
// prints. getenv is injected rather than read from the process so that run and
// its tests share one code path.
func ResolveToken(ctx context.Context, getenv func(string) string) (Secret, error) {
	return resolveToken(ctx, getenv, ghAuthToken)
}

// resolveToken is ResolveToken with the gh fallback injected, so a test can
// exercise both branches without a gh binary on PATH.
func resolveToken(ctx context.Context, getenv func(string) string, gh func(context.Context) (string, error)) (Secret, error) {
	if getenv == nil {
		return "", fmt.Errorf("%w: no getenv", ErrConfig)
	}
	if token := strings.TrimSpace(getenv(tokenEnvVar)); token != "" {
		return Secret(token), nil
	}

	token, err := gh(ctx)
	if err != nil {
		// A child killed by its context reports the signal, not the context,
		// so the cancellation has to be put back for main to exit 130.
		if ctx.Err() != nil {
			return "", fmt.Errorf("gh auth token: %w", ctx.Err())
		}
		return "", fmt.Errorf("%s unset and gh auth token failed: %w", tokenEnvVar, err)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("%w: %s unset and gh auth token printed nothing", errNoToken, tokenEnvVar)
	}
	return Secret(token), nil
}

// ghAuthToken shells out to the gh CLI. Every argument is a constant, so there
// is no injection surface here.
func ghAuthToken(ctx context.Context) (string, error) {
	return runCapture(ctx, "gh", "auth", "token", "--hostname", ghHostname)
}

// runCapture runs name with args and returns its standard output, folding a
// failed child's stderr into the error because that is where gh explains an
// expired login. It is separate from ghAuthToken so a test can run a child
// that hangs; no caller passes anything but a constant argv.
func runCapture(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: every call site passes a constant argv
	// A child killed by its context can leave a descendant holding stdout,
	// and Wait blocks on the pipe rather than on the process. WaitDelay is
	// what bounds that wait, so an interrupt cannot hang the tool.
	cmd.WaitDelay = ghWaitDelay
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, snippet(exitErr.Stderr))
		}
		return "", err
	}
	return string(out), nil
}
