// Copyright ThesmOS B.V. 2026
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

// fakeTimer implements [clock.Timer] for [Clock]. mu serialises Stop
// and Reset, which replaces waiter.
type fakeTimer struct {
	clock  *Clock
	waiter *waiter
	mu     sync.Mutex
}

// C returns the timer's tick channel.
func (t *fakeTimer) C() <-chan time.Time { return t.waiter.ch }

// Stop cancels the tick of a pending timer and drops an unreceived tick
// from the channel. A [time.Timer] of Go 1.23 and later does the same. A
// receive from the channel after Stop returns then blocks. Stop returns
// true if the timer was pending or its tick was unreceived, and false if
// its tick was received or the timer was stopped. A stopped timer has
// neither a pending waiter nor a tick, so a second Stop returns false.
func (t *fakeTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.clock.mu.Lock()
	active := t.clock.removeWaiter(t.waiter)
	// The clock sends the tick of a waiter while its mutex is locked, so
	// no tick can arrive after the drop.
	select {
	case <-t.waiter.ch:
		active = true
	default:
	}
	t.clock.mu.Unlock()
	return active
}

// Reset schedules the tick of the timer for d virtual nanoseconds after
// the current virtual time. Like Stop, it drops an unreceived tick from
// the channel. Reset returns true if the timer was pending or its tick
// was unreceived at the time of the call.
//
// Reset keeps the channel of the timer, so a caller that took the channel
// from an earlier call of C receives the tick of the reset timer on it.
func (t *fakeTimer) Reset(d time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.clock.mu.Lock()
	wasActive := t.clock.removeWaiter(t.waiter)

	// Drop an unreceived tick of the earlier deadline. A full channel
	// would otherwise block the send of the new waiter while the clock
	// mutex is locked.
	select {
	case <-t.waiter.ch:
		wasActive = true
	default:
	}

	deadline := t.clock.now.Add(d)
	w := &waiter{deadline: deadline, ch: t.waiter.ch}
	t.waiter = w

	if !t.clock.now.Before(deadline) {
		w.fire(deadline)
	} else {
		t.clock.waiters = append(t.clock.waiters, w)
	}
	t.clock.mu.Unlock()

	return wasActive
}
