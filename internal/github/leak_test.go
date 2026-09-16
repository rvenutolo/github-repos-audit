package github_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain asserts the package leaves no goroutine behind after every test has
// run. TestClient_Collect_isLeakFree keeps its own goleak.VerifyNone: this one
// is the net, that one names the fan-out as the culprit when it leaks.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
