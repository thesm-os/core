// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fake

import (
	"fmt"
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

	// awaitTimeout overrides the [Clock.AwaitWaiters] watchdog.
	// Zero means the default. It is per instance, so the watchdog's
	// own test can shorten it without changing state that other
	// tests read. This package's tests run in parallel.
	awaitTimeout time.Duration

	// utcError is the MaxError that [Clock.ReadUTC] reports. Guarded
	// by mu.
	utcError time.Duration

	// utcUnsynced reports whether [Clock.ReadUTC] reports an
	// unsynchronised reading. The zero value is synchronised. Guarded
	// by mu.
	utcUnsynced bool
}

// Compile-time interface checks.
var (
	_ clock.Clock     = (*Clock)(nil)
	_ clock.UTCSource = (*Clock)(nil)
)

// New returns a [Clock] initialised to origin with [clock.NodeID]
// zero. Use [NewWithNode] when tests assert on cross-node ordering.
func New(origin time.Time) *Clock {
	return NewWithNode(origin, 0)
}

// NewWithNode returns a [Clock] initialised to origin and tagged
// with node. A test that verifies HLC ordering across simulated nodes
// builds two Clocks with distinct node values, and
// [clock.Instant.HappensBefore] breaks ties on Node when Wall and
// Logical are equal.
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

// Time returns the current virtual time as a stdlib [time.Time]. It is
// Now().Time(), so it advances the logical counter as [Clock.Now]
// does, and the [clock.Clock] contract applies uniformly across
// implementations.
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
// [Clock.Advance] for forward progress, and Set for a test that needs
// virtual time at a specific calendar instant, such as 2030-01-01.
// Setting time backward is allowed. It does not retract waiters that
// have already fired.
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
// AwaitWaiters yields to the scheduler between checks, so it costs
// little when the goroutines are already running.
//
// # Failure semantics
//
// Waiting for waiters that never arrive is a programmer error: the
// awaited goroutines are not calling a blocking clock operation, or
// n is larger than the number that ever will. AwaitWaiters panics
// after a watchdog interval of two seconds.
//
// The interval bounds goroutines that will never register, not slow
// ones. A correct wait completes in microseconds, because the awaited
// goroutine only has to make its first blocking clock call. The
// watchdog therefore cannot make a correct test flaky on a contended
// runner, and it expires far inside any plausible `go test -timeout`.
//
// The panic reports the count registered and the count expected, and
// it fails the test that called AwaitWaiters. A hang left to the
// [testing] deadline would fail slowly and name whichever test was
// running when the process died.
//
// The bound is real elapsed time, not virtual time. It measures the
// test process's patience with goroutines that will not start, which
// no amount of [Clock.Advance] can affect.
//
// # Concurrency
//
// Safe for concurrent use. The spin holds c.mu only for the length
// of a slice read.
func (c *Clock) AwaitWaiters(n int) {
	// No constructor sets awaitTimeout, so every ordinary Clock uses the
	// default. Only this package's watchdog test sets it, so the test
	// does not spend the whole interval proving that the panic occurs.
	//
	// The default is set in the function body, where it has a coverage
	// counter and mutation testing can reach its mutants. A package
	// const has neither.
	//
	// Two seconds is six orders of magnitude above a correct
	// registration. It also expires before the per-mutant budget of
	// mutation testing, which is a small multiple of a suite's baseline.
	bound := c.awaitTimeout
	if bound <= 0 {
		bound = 2 * time.Second
	}

	deadline := time.Now().Add(bound)

	for {
		c.mu.Lock()
		count := len(c.waiters)
		c.mu.Unlock()

		if count >= n {
			return
		}

		if time.Now().After(deadline) {
			panic(fmt.Sprintf( //nolint:forbidigo // a hung test helper is a programmer error; see Failure semantics
				"clock/fake: AwaitWaiters(%d) gave up after %s with %d waiter(s) registered — "+
					"the awaited goroutines never called a blocking clock operation",
				n, bound, count,
			))
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

// removeWaiter unregisters w from the pending list. Returns true if w
// was pending, which means it had not fired. Must be called with c.mu
// held.
func (c *Clock) removeWaiter(w *waiter) bool {
	i := slices.Index(c.waiters, w)
	if i < 0 {
		return false
	}
	c.waiters = slices.Delete(c.waiters, i, i+1)
	return true
}
