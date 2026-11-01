// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package hlc

import (
	"math"
	"sync"
	"time"

	"go.thesmos.sh/core/clock"
)

// Clock implements [clock.Clock] using the system wall clock with
// Hybrid Logical Clock semantics.
//
// Clock is safe for concurrent use by multiple goroutines.
//
// # Allocation contract
//
// [Clock.Now], [Clock.Time], [Clock.Elapsed] and [Clock.Update] are
// zero-allocation. [Clock.NewTimer] allocates the wrapper struct and the
// underlying [time.Timer].
//
//dokimi:subject clock.Clock new=newClock
//dokimi:budget Clock.Now allocs=0
//dokimi:budget Clock.Time allocs=0
//dokimi:budget Clock.Elapsed allocs=0
//dokimi:budget Clock.Update allocs=0
type Clock struct {
	// origin is the time of the construction of the clock. It carries the
	// monotonic reading of [time.Now], which Elapsed measures from.
	origin time.Time

	nowFn func() int64
	wall  int64

	// maxOffset is the furthest ahead of the physical time that Update
	// adopts the wall of a peer. It is not negative.
	maxOffset time.Duration

	mu      sync.Mutex
	logical uint32
	node    clock.NodeID
}

// Compile-time interface check.
var _ clock.Clock = (*Clock)(nil)

// New returns a production HLC [clock.Clock] tagged with node. Wall
// time is read from [time.Now]. [Clock.Update] refuses a peer instant
// whose wall is more than maxOffset ahead of the wall time, and a
// maxOffset below zero counts as zero. [clock.DefaultMaxOffset] is
// the max offset of a deployment whose clocks NTP keeps within 500 ms
// of each other.
func New(node clock.NodeID, maxOffset time.Duration) *Clock {
	return NewWithSource(node, maxOffset, nowUnixNano)
}

// NewWithSource returns an HLC [clock.Clock] that reads wall time
// from source instead of [time.Now], with the max offset of [New].
// Enables unit tests that exercise specific HLC branches (wall
// advance vs logical increment, peer-ahead vs local-ahead) without
// depending on real-clock cadence. [Clock.Elapsed] reads the
// monotonic time of the system and not source.
//
// source must return monotonically non-decreasing values; HLC's
// logical counter is what guarantees strict monotonicity even when
// source repeats a value across calls.
func NewWithSource(node clock.NodeID, maxOffset time.Duration, source func() int64) *Clock {
	return &Clock{origin: time.Now(), node: node, nowFn: source, maxOffset: max(maxOffset, 0)}
}

// nowUnixNano is the default wall-time source. Pulled out as a
// package-level function so the Clock struct does not capture an
// inline closure on every construction.
func nowUnixNano() int64 { return time.Now().UnixNano() }

// Now returns the next HLC [clock.Instant]. If the wall-time source
// has advanced past the last issued wall, the new wall is adopted
// and the logical counter resets to 1; otherwise the logical
// counter increments. After Logical 2^32 - 1 the wall moves forward
// by a nanosecond and Logical starts again at 1.
//
// # Allocation contract
//
// Zero alloc.
func (c *Clock) Now() clock.Instant {
	now := c.nowFn()

	c.mu.Lock()
	defer c.mu.Unlock()

	if now > c.wall {
		c.wall = now
		c.logical = 1
	} else {
		c.step(c.logical)
	}

	return clock.Instant{Wall: c.wall, Logical: c.logical, Node: c.node}
}

// Time returns the current wall time as a stdlib [time.Time].
// Equivalent to Now().Time(); offered as a first-class method for
// the common case where callers only want the stdlib representation.
//
// Time advances the HLC state in the same way as Now.
//
// # Allocation contract
//
// Zero alloc.
func (c *Clock) Time() time.Time {
	return c.Now().Time()
}

// Elapsed returns the time since [New] or [NewWithSource] constructed
// the clock. It reads the monotonic time of the system, which a step
// of the wall clock does not change, and it takes no lock.
//
// # Allocation contract
//
// Zero alloc.
func (c *Clock) Elapsed() time.Duration {
	return time.Since(c.origin)
}

// Update merges an observed peer instant into the local clock and
// returns the resulting instant.
//
// The merge follows the canonical HLC rule from Kulkarni et al.,
// "Logical Physical Clocks":
//
//	newWall = max(local.Wall, observed.Wall, physical)
//	newLogical depends on which input wins:
//	  - physical strictly wins  → reset to 1 (fresh tick)
//	  - local and observed tied → max(both logicals) + 1
//	  - local wins              → local.Logical + 1
//	  - observed wins           → observed.Logical + 1
//
// A Logical of 2^32 - 1 moves the wall forward by a nanosecond and
// starts Logical again at 1, as in [Clock.Now].
//
// Error modes: an observed wall more than the max offset of [New]
// ahead of the physical time returns [clock.ErrOffset], classified
// [go.thesmos.sh/core/errs.Integrity], with the zero Instant, and the
// clock is unchanged.
//
// # Allocation contract
//
// Zero alloc.
func (c *Clock) Update(observed clock.Instant) (clock.Instant, error) {
	now := c.nowFn()

	// limit is the latest peer wall that the clock merges: the physical
	// time plus the max offset, or the largest wall when the sum does not
	// fit an int64. maxOffset is not negative.
	limit := int64(math.MaxInt64)
	//dokimi:mutate-skip ror-boundary: at the boundary the sum is the largest wall, which the limit has already
	if now < math.MaxInt64-int64(c.maxOffset) {
		limit = now + int64(c.maxOffset)
	}

	if observed.Wall > limit {
		return clock.Instant{}, clock.ErrOffset
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	newWall := max(c.wall, observed.Wall, now)
	localMax := newWall == c.wall
	observedMax := newWall == observed.Wall

	switch {
	case !localMax && !observedMax:
		// Physical clock advanced past both — fresh tick.
		c.wall = newWall
		c.logical = 1
	case localMax && observedMax:
		// Tied — take the higher logical and step.
		c.step(max(c.logical, observed.Logical))
	case localMax:
		c.step(c.logical)
	default:
		// observedMax
		c.wall = newWall
		c.step(observed.Logical)
	}

	return clock.Instant{Wall: c.wall, Logical: c.logical, Node: c.node}, nil
}

// step sets the Logical of c to the Logical after logical at the wall
// of c. After Logical 2^32 - 1 it moves the wall forward by a
// nanosecond and sets Logical 1. The caller locks c.mu.
func (c *Clock) step(logical uint32) {
	if logical == math.MaxUint32 {
		c.wall++
		c.logical = 1

		return
	}

	c.logical = logical + 1
}
