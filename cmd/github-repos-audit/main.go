// Command github-repos-audit reads every active repository on the account and reports what
// each one is missing.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// version is the version linked into release builds through
// -ldflags "-X main.version=..." (.ci/build-release). It stays "dev" in every
// build that does not link one; --version reports it through resolveVersion,
// which substitutes the module version that `go install module@vX.Y.Z` and a
// `go build` in a regular clone record.
var version = unlinkedVersion

func main() { // coverage-exempt: the process entry point; it only hands the os globals to run, which is tested directly
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	context.AfterFunc(ctx, stop) // first signal cancels ctx; a second one kills the process
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr)
	stop()
	if err != nil { // coverage-exempt: part of main, which no test can call
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(exitCode(err))
	}
}

// exitCode maps an error from run to this program's exit status. The four
// codes are documented in --help and must not drift from it.
func exitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errUsage):
		return 2
	case errors.Is(err, context.Canceled):
		return 130
	default:
		return 1
	}
}
