package main

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain asserts the package leaves no goroutine behind. This package shells
// out to `gh auth token` with a WaitDelay, so a goroutine os/exec starts to
// enforce that delay outliving the command would be caught here.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
