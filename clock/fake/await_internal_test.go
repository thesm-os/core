// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package fake

import (
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
)

// TestAwaitWaitersGivesUp is an internal test, because the watchdog bound is a
// field of the Clock that the package does not export.
func TestAwaitWaitersGivesUp(t *testing.T) {
	t.Parallel()

	t.Run("panics when the waiters never arrive", func(t *testing.T) {
		t.Parallel()
		// No goroutine waits on this clock, so only the watchdog ends the
		// wait.
		c := New(time.Unix(0, 0))
		c.awaitTimeout = 10 * time.Millisecond

		assert.Panics(t, func() { c.AwaitWaiters(1) }, "AwaitWaiters of a waiter that never arrives panics")
	})

	t.Run("panics with both counts", func(t *testing.T) {
		t.Parallel()
		c := New(time.Unix(0, 0))
		c.awaitTimeout = 10 * time.Millisecond

		recovered := assert.Panics(t, func() { c.AwaitWaiters(3) }, "AwaitWaiters of waiters that never arrive panics")
		msg, _ := recovered.(string)
		expect.That(t, msg).
			Contains("AwaitWaiters(3)", "the panic names the count that AwaitWaiters waited for").
			Contains("0 waiter(s) registered", "the panic names the count of waiters that arrived")
	})

	t.Run("returns when the waiters arrive", func(t *testing.T) {
		t.Parallel()
		c := New(time.Unix(0, 0))
		c.awaitTimeout = 10 * time.Millisecond

		go func() { c.NewTimer(time.Second) }()

		c.AwaitWaiters(1)
	})

	t.Run("waits for the default bound on the zero Clock", func(t *testing.T) {
		t.Parallel()
		// No constructor returns a Clock without a bound, so the zero Clock
		// stands for a Clock that a struct literal builds.
		var c Clock

		go func() { c.NewTimer(time.Second) }()

		c.AwaitWaiters(1)
	})
}
