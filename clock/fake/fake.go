// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fake

import (
	"runtime"
	"slices"
	"sync"
	"time"

	"go.thesmos.sh/core/clock"
)

// Clock is a deterministic virtual-time [clock.Clock]. Time only
// advances when callers invoke [Clock.Advance] or [Clock.Set]; on
// each [Clock.Now], the logical counter increments so consecutive
// instants between advances remain ordered.
//
// Clock is safe for concurrent use by multiple goroutines.
type Clock struct {
	now     time.Time
	waiters []*waiter
	mu      sync.Mutex
	logical uint32
	node    clock.NodeID
}

// Compile-time interface check.
var _ clock.Clock = (*Clock)(nil)

// New returns a [Clock] initialised to origin with [clock.NodeID]
// zero. Use [NewWithNode] when tests assert on cross-node ordering.
func New(origin time.Time) *Clock {
	return NewWithNode(origin, 0)
}

// NewWithNode returns a [Clock] initialised to origin and tagged
// with node. Useful when a test verifies HLC ordering across
// simulated nodes — construct two Clocks with distinct node values
// and observe that [clock.Instant.HappensBefore] tie-breaks on Node
// when Wall and Logical are equal.
func NewWithNode(origin time.Time, node clock.NodeID) *Clock {
	return &Clock{now: origin, node: node}
}

// Now returns the current virtual instant. Wall is the virtual
// time's UnixNano value; Logical increments per call so consecutive
// Now calls between Advance invocations return distinct, ordered
// instants.
//
// # Allocation contract
//
// Zero alloc.
func (c *Clock) Now() clock.Instant {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logical++
	return clock.Instant{
		Wall:    c.now.UnixNano(),
		Logical: c.logical,
		Node:    c.node,
	}
}

// Time returns the current virtual time as a stdlib [time.Time].
// Unlike [Clock.Now], Time does not advance the logical counter —
// Equivalent to Now().Time(); calling Time advances the HLC state
// in the same way as [Clock.Now], so the [clock.Clock] contract
// holds uniformly across implementations.
//
// # Allocation contract
//
// Zero alloc.
func (c *Clock) Time() time.Time {
	return c.Now().Time()
}

// Update merges an observed peer instant into the fake clock,
// preserving the [clock.Clock] contract that the returned instant
// is strictly causally after observed.
//
// Virtual time has no real peers, but a naive return of [Clock.Now]
// would violate the contract when observed.Wall exceeds the
// fake clock's wall. The merge here adopts observed.Wall when it is
// ahead, so [Clock.Update] on a fake clock is safe to call from
// the same code paths that drive a production HLC.
func (c *Clock) Update(observed clock.Instant) clock.Instant {
	c.mu.Lock()
	defer c.mu.Unlock()

	localWall := c.now.UnixNano()
	if observed.Wall > localWall {
		c.now = time.Unix(0, observed.Wall)
		c.logical = observed.Logical + 1
	} else if observed.Wall == localWall {
		c.logical = max(c.logical, observed.Logical) + 1
	} else {
		c.logical++
	}
	return clock.Instant{
		Wall:    c.now.UnixNano(),
		Logical: c.logical,
		Node:    c.node,
	}
}

// Advance moves virtual time forward by d and wakes every waiter
// whose deadline has been reached. A non-positive d is a no-op:
// virtual time only moves forward.
//
// Advance also resets the logical counter on the next [Clock.Now]
// because virtual wall has moved.
func (c *Clock) Advance(d time.Duration) {
	if d <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.logical = 0
	c.fireWaiters()
}

// Set replaces the virtual time with t. Like [Clock.Advance], Set
// fires every waiter whose deadline has been reached. Use
// [Clock.Advance] for forward progress; reserve Set for tests that
// need to position virtual time at a specific calendar instant
// (e.g. "what if it is 2030-01-01?"). Setting time backward is
// allowed and supported by the test harness, but does not retract
// previously-fired waiters.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
	c.logical = 0
	c.fireWaiters()
}

// AwaitWaiters blocks until at least n goroutines have registered
// waiters with the clock (via [clock.Sleep], [clock.After], or
// [Clock.NewTimer]). Use this instead of [time.Sleep] for
// deterministic goroutine synchronisation in tests:
//
//	go func() { clock.Sleep(clk, 5*time.Second) }()
//	clk.AwaitWaiters(1)            // deterministic — no real-time
//	clk.Advance(6 * time.Second)
//
// AwaitWaiters yields to the scheduler between checks and is
// therefore cheap when the goroutines are already running. It will
// spin if the awaited goroutines never register; tests are expected
// to bound their wait via the [testing] framework's own deadlines.
func (c *Clock) AwaitWaiters(n int) {
	for {
		c.mu.Lock()
		count := len(c.waiters)
		c.mu.Unlock()
		if count >= n {
			return
		}
		runtime.Gosched()
	}
}

// fireWaiters fires every waiter whose deadline is at or before
// c.now and removes them from the pending list. Must be called
// with c.mu held.
func (c *Clock) fireWaiters() {
	c.waiters = slices.DeleteFunc(c.waiters, func(w *waiter) bool {
		if c.now.Before(w.deadline) {
			return false
		}
		w.fire(w.deadline)
		return true
	})
}

// removeWaiter unregisters w from the pending list. Returns true
// if w was found (i.e. had not yet fired). Must be called with
// c.mu held.
func (c *Clock) removeWaiter(w *waiter) bool {
	i := slices.Index(c.waiters, w)
	if i < 0 {
		return false
	}
	c.waiters = slices.Delete(c.waiters, i, i+1)
	return true
}
