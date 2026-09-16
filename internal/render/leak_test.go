package render_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain asserts the package leaves no goroutine behind. This package shells
// out to prettier in the end-to-end test, which is where an unreaped child or a
// stray os/exec goroutine would show up.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
