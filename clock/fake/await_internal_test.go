// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fake

import (
	"strings"
	"testing"
	"time"

	"go.thesmos.sh/testkit"
)

// Internal rather than in the fake_test package because the watchdog
// bound is per-instance and unexported. Reaching it through an
// export_test.go bridge would be the shape that defeats gobco's type
// resolution and takes the whole branch-coverage run down with it.
func TestAwaitWaitersGivesUp(t *testing.T) {
	t.Parallel()

	t.Run("panics when the waiters never arrive", func(t *testing.T) {
		t.Parallel()

		// No goroutine ever blocks on this clock, so the count stays
		// at zero and only the watchdog can end the spin. Before it
		// existed this call never returned, and the failure surfaced
		// as the whole test binary hitting its deadline.
		c := New(time.Unix(0, 0))
		c.awaitTimeout = 10 * time.Millisecond

		testkit.Panics(t, func() { c.AwaitWaiters(1) },
			"awaiting a waiter that never registers must panic, not hang")
	})

	t.Run("the panic names both counts", func(t *testing.T) {
		t.Parallel()

		// The diagnosis is "how many did I expect, how many arrived".
		// A bare "timed out" would leave the reader to guess whether
		// the goroutine failed to start or n was simply too large.
		c := New(time.Unix(0, 0))
		c.awaitTimeout = 10 * time.Millisecond

		var msg string

		func() {
			defer func() {
				if r := recover(); r != nil {
					msg, _ = r.(string)
				}
			}()
			c.AwaitWaiters(3)
		}()

		testkit.True(t, strings.Contains(msg, "AwaitWaiters(3)"),
			"the panic must name the count awaited")
		testkit.True(t, strings.Contains(msg, "0 waiter(s) registered"),
			"the panic must name the count reached")
	})

	t.Run("does not fire when the waiters do arrive", func(t *testing.T) {
		t.Parallel()

		// The happy path must not be sensitive to the bound: a waiter
		// that registers promptly returns long before it expires.
		c := New(time.Unix(0, 0))
		c.awaitTimeout = 10 * time.Millisecond

		go func() { c.NewTimer(time.Second) }()

		c.AwaitWaiters(1)
	})

	t.Run("a composed zero Clock falls back to the default bound", func(t *testing.T) {
		t.Parallel()

		// No constructor produces this, but the field is unexported
		// rather than unreachable. An unset bound must mean "the
		// default", never "expire on the first check".
		var c Clock

		go func() { c.NewTimer(time.Second) }()

		c.AwaitWaiters(1)
	})
}
