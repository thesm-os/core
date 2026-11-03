// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package fake

import (
	"fmt"
	"math"
	"runtime"
	"slices"
	"sync"
	"time"

	"go.thesmos.sh/core/clock"
)

// Clock is a [clock.Clock] in virtual time. The virtual time moves
// only at [Clock.Advance] and [Clock.Set]. Each [Clock.Now] adds 1 to
// the logical counter, so the instants between two moves of the
// virtual time are ordered.
//
// [Clock.Elapsed] is the sum of the durations of Advance. Set and
// Update change the wall time alone, as a step of a wall clock leaves
// a monotonic clock unchanged. [Clock.Update] refuses a peer instant
// whose wall is more than [clock.DefaultMaxOffset] ahead of the
// virtual time.
//
// Each pending timer of the clock is a waiter. [Clock.Waiters] counts
// the waiters, and [Clock.AwaitWaiters] waits for them.
//
// # Concurrency
//
// Safe for concurrent use. One mutex per Clock guards the virtual time,
// the logical counter, the elapsed time and the waiters.
//
// # Allocation contract
//
// Now, Time, Elapsed and Update do not allocate.
//
//dokimi:subject clock.Clock new=newClock
//dokimi:budget Clock.Now allocs=0
//dokimi:budget Clock.Time allocs=0
//dokimi:budget Clock.Elapsed allocs=0
//dokimi:budget Clock.Update allocs=0
type Clock struct {
	// now is the virtual time. Guarded by mu.
	now time.Time

	// waiters are the pending timers, in the order of their
	// registration. Guarded by mu.
	waiters []*waiter

	// elapsed is the sum of the durations of Advance. Guarded by mu.
	elapsed time.Duration

	mu sync.Mutex

	// logical is the Logical of the last instant, and node the NodeID
	// of every instant. logical is guarded by mu.
	logical uint32
	node    clock.NodeID

	// awaitTimeout is the bound of [Clock.AwaitWaiters]. Zero selects
	// the default of two seconds. The internal test of the bound sets
	// it on a Clock of its own, so the tests that run in parallel keep
	// the default.
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

// Now returns the current virtual instant. Its Wall is the UnixNano of
// the virtual time. Each call adds 1 to the Logical, and a move of the
// virtual time starts it again at 1, so two calls between two moves
// return distinct, ordered instants. After Logical 2^32 - 1, the
// virtual time moves forward by a nanosecond and Logical starts again
// at 1.
//
// # Allocation contract
//
// Zero alloc.
func (c *Clock) Now() clock.Instant {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.step(c.logical)
	return clock.Instant{
		Wall:    c.now.UnixNano(),
		Logical: c.logical,
		Node:    c.node,
	}
}

// Time returns the current virtual time as a [time.Time]. It returns
// Now().Time(), so it adds 1 to the logical counter as [Clock.Now]
// does.
//
// # Allocation contract
//
// Zero alloc.
func (c *Clock) Time() time.Time {
	return c.Now().Time()
}

// Elapsed returns the sum of the durations of [Clock.Advance] since
// [New]. [Clock.Set] and [Clock.Update] do not change it.
//
// # Allocation contract
//
// Zero alloc.
func (c *Clock) Elapsed() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.elapsed
}

// Update merges the instant of a peer into the clock, and returns an
// instant that happens after observed, as [clock.Clock] requires. For
// a peer ahead of the virtual time, the clock takes the wall of the
// peer and the Logical after the Logical of the peer. For a peer at
// the wall of the clock, it takes the larger Logical plus 1. For a peer
// behind, it takes the next local Logical. A Logical of 2^32 - 1 moves
// the virtual time forward by a nanosecond, as in [Clock.Now].
//
// Error modes: an observed wall more than [clock.DefaultMaxOffset]
// ahead of the virtual time returns [clock.ErrOffset], classified
// [go.thesmos.sh/core/errs.Integrity], with the zero Instant, and the
// clock is unchanged.
func (c *Clock) Update(observed clock.Instant) (clock.Instant, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	localWall := c.now.UnixNano()

	// limit is the latest peer wall that the clock merges: the virtual time
	// plus the max offset, or the largest wall when the sum does not fit an
	// int64.
	limit := int64(math.MaxInt64)
	//dokimi:mutate-skip ror-boundary: at the boundary the sum is the largest wall, which the limit has already
	if localWall < math.MaxInt64-int64(clock.DefaultMaxOffset) {
		limit = localWall + int64(clock.DefaultMaxOffset)
	}

	if observed.Wall > limit {
		return clock.Instant{}, clock.ErrOffset
	}

	if observed.Wall > localWall {
		c.now = time.Unix(0, observed.Wall)
		c.step(observed.Logical)
	} else if observed.Wall == localWall {
		c.step(max(c.logical, observed.Logical))
	} else {
		c.step(c.logical)
	}
	return clock.Instant{
		Wall:    c.now.UnixNano(),
		Logical: c.logical,
		Node:    c.node,
	}, nil
}

// Advance moves the virtual time forward by d, adds d to
// [Clock.Elapsed], and sends the tick of every waiter whose deadline is
// at or before the new virtual time. A d that is not positive changes
// nothing, so the virtual time moves only forward. Advance starts the
// logical counter again, so the next [Clock.Now] returns Logical 1.
func (c *Clock) Advance(d time.Duration) {
	if d <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.elapsed += d
	c.logical = 0
	c.fireWaiters()
}

// Set replaces the virtual time with t, and sends the tick of every
// waiter whose deadline is at or before t, as [Clock.Advance] does. A
// test moves the clock forward with Advance, and uses Set for a
// calendar instant such as 2030-01-01. t can be before the virtual
// time, and Set does not take back a tick that the clock has sent. Set
// starts the logical counter again. It models a step of a wall clock,
// so it does not change [Clock.Elapsed].
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
	c.logical = 0
	c.fireWaiters()
}

// AwaitWaiters blocks until the clock has at least n waiters, as
// [Clock.Waiters] counts them. A test calls it after it starts a
// goroutine that blocks on the clock and before it advances the clock,
// so the Advance sends the tick of that goroutine:
//
//	go func() { clock.Sleep(clk, 5*time.Second) }()
//	clk.AwaitWaiters(1)
//	clk.Advance(6 * time.Second)
//
// AwaitWaiters yields to the scheduler between two reads of the count.
//
// # Failure semantics
//
// AwaitWaiters panics when the count is still below n two seconds
// after the call. The panic message contains n, the bound and the
// count, and the panic fails the test that called AwaitWaiters. The
// bound runs in real time, and no Advance moves it. The bound ends a
// test whose goroutines never block on the clock before the deadline
// of go test. It also ends a test whose n exceeds the number of
// waiters that ever register.
//
// A goroutine that works before its first blocking clock call, such as
// a loop that queries a database before it waits for its next round,
// can take longer than the bound. A test of such a goroutine polls
// Waiters with a bound of its own.
//
// # Concurrency
//
// Safe for concurrent use. AwaitWaiters locks the mutex of the clock
// only to read the count.
func (c *Clock) AwaitWaiters(n int) {
	// Only the internal test of the bound sets awaitTimeout. The default
	// is a statement of the body, so it has a coverage counter and
	// mutants. A package const would have neither. The budget of a mutant
	// in a mutation run is a small multiple of the time of a suite, and
	// two seconds expires before that budget.
	bound := c.awaitTimeout
	if bound <= 0 {
		bound = 2 * time.Second
	}

	deadline := time.Now().Add(bound)

	for {
		count := c.Waiters()
		if count >= n {
			return
		}

		if time.Now().After(deadline) {
			panic(fmt.Sprintf( //nolint:forbidigo // waiters that do not arrive fail the test of the caller
				"fake: AwaitWaiters(%d) gave up after %s with %d waiter(s) registered: "+
					"the awaited goroutines did not call a blocking clock operation in time",
				n, bound, count,
			))
		}

		runtime.Gosched()
	}
}

// Waiters returns the number of waiters of the clock: the timers of
// [Clock.NewTimer], [clock.Sleep], [clock.After] and [clock.Wait]
// whose deadline the virtual time has not passed and that have not
// stopped. A timer of a duration that is not positive is no waiter,
// because its tick is sent at once.
//
// A test of a goroutine that works before its first blocking clock call
// waits for the count with a bound of its own:
//
//	assert.EventuallyTrue(t, 30*time.Second, func() bool { return clk.Waiters() >= 2 }, msg)
//
// The count also drops when a waiter leaves, such as the timer of a
// [clock.Wait] whose context ended. [Clock.AwaitWaiters] returns at a
// count of at least n, so it cannot show the drop.
//
// # Concurrency
//
// Safe for concurrent use. A goroutine can register or stop a timer
// right after the call, so the count can be out of date when Waiters
// returns.
func (c *Clock) Waiters() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.waiters)
}

// fireWaiters sends the tick of every waiter whose deadline is at or
// before c.now, and removes those waiters from the list. The caller
// has locked c.mu.
func (c *Clock) fireWaiters() {
	c.waiters = slices.DeleteFunc(c.waiters, func(w *waiter) bool {
		if c.now.Before(w.deadline) {
			return false
		}
		w.fire(w.deadline)
		return true
	})
}

// step sets the Logical of c to the Logical after logical at the
// virtual time. After Logical 2^32 - 1 it moves the virtual time
// forward by a nanosecond and sets Logical 1. It does not change
// [Clock.Elapsed]. The caller has locked c.mu.
func (c *Clock) step(logical uint32) {
	if logical == math.MaxUint32 {
		c.now = c.now.Add(time.Nanosecond)
		c.logical = 1

		return
	}

	c.logical = logical + 1
}

// removeWaiter takes w out of the list of waiters, and reports whether
// the list contained w. A waiter leaves the list when the clock sends
// its tick, so true means that the clock has not sent the tick of w.
// The caller has locked c.mu.
func (c *Clock) removeWaiter(w *waiter) bool {
	i := slices.Index(c.waiters, w)
	if i < 0 {
		return false
	}
	c.waiters = slices.Delete(c.waiters, i, i+1)
	return true
}
