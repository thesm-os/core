// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fake

import (
	"sync"
	"time"

	"go.thesmos.sh/core/clock"
)

// Compile-time interface check.
var _ clock.Timer = (*fakeTimer)(nil)

// NewTimer creates a [clock.Timer] that fires when virtual time
// reaches now + d. The timer's channel does not receive until a
// caller invokes [Clock.Advance] (or [Clock.Set]) past the
// deadline.
//
// A non-positive d produces a timer that fires immediately on the
// first read of its channel — matching [time.NewTimer]'s behaviour.
func (c *Clock) NewTimer(d time.Duration) clock.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	deadline := c.now.Add(d)
	w := &waiter{deadline: deadline, ch: make(chan time.Time, 1)}
	if !c.now.Before(deadline) {
		// d <= 0 — fire immediately.
		w.fire(deadline)
	} else {
		c.waiters = append(c.waiters, w)
	}
	return &fakeTimer{clock: c, waiter: w}
}

// waiter is one goroutine blocked on a virtual-time deadline.
type waiter struct {
	ch       chan time.Time
	deadline time.Time
	once     sync.Once
}

// fire delivers at on the waiter's channel exactly once, even if
// fire is called concurrently from multiple sites (e.g. Advance
// and Set racing).
func (w *waiter) fire(at time.Time) {
	w.once.Do(func() { w.ch <- at })
}

// fakeTimer implements [clock.Timer] for [Clock].
type fakeTimer struct {
	clock   *Clock
	waiter  *waiter
	mu      sync.Mutex
	stopped bool
}

// C returns the timer's tick channel.
func (t *fakeTimer) C() <-chan time.Time { return t.waiter.ch }

// Stop prevents the timer from firing. Returns true if the timer
// was active (still pending), false if it had already fired or
// been stopped.
func (t *fakeTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return false
	}
	t.clock.mu.Lock()
	removed := t.clock.removeWaiter(t.waiter)
	t.clock.mu.Unlock()
	t.stopped = true
	return removed
}

// Reset reschedules the timer to fire d virtual nanoseconds from
// the current virtual time. Returns true if the timer was active
// at the time of the call.
//
// The timer's channel is reused so callers holding the channel
// from a prior C() call still receive on the right channel after
// the reset.
func (t *fakeTimer) Reset(d time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.clock.mu.Lock()
	wasActive := t.clock.removeWaiter(t.waiter)

	// Drain any leftover buffered value from a prior fire. Without
	// this, Reset on a fired-but-undrained timer would block when
	// the new waiter's fire() tries to send on a full channel
	// while holding the clock mutex.
	select {
	case <-t.waiter.ch:
	default:
	}

	deadline := t.clock.now.Add(d)
	w := &waiter{deadline: deadline, ch: t.waiter.ch}
	t.waiter = w
	t.stopped = false

	if !t.clock.now.Before(deadline) {
		w.fire(deadline)
	} else {
		t.clock.waiters = append(t.clock.waiters, w)
	}
	t.clock.mu.Unlock()

	return wasActive
}
