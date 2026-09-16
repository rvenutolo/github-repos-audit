package github

import (
	"context"
	"time"
)

// DisableRetryWaits removes a client's backoff, so a test outside this package
// can drive the retry path without spending the seconds GitHub asked for. What
// the policy waits, and when, is asserted in this package, where it lives.
func DisableRetryWaits(c *Client) {
	c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
}
