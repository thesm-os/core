// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package hlc

import (
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
// [Clock.Now], [Clock.Time], and [Clock.Update] are zero-allocation.
// [Clock.NewTimer] allocates the wrapper struct and the underlying
// [time.Timer].
type Clock struct {
	nowFn   func() int64
	wall    int64
	mu      sync.Mutex
	logical uint32
	node    clock.NodeID
}

// Compile-time interface check.
var _ clock.Clock = (*Clock)(nil)

// New returns a production HLC [clock.Clock] tagged with node.
// Wall time is read from [time.Now].
func New(node clock.NodeID) *Clock {
	return &Clock{node: node, nowFn: nowUnixNano}
}

// NewWithSource returns an HLC [clock.Clock] that reads wall time
// from source instead of [time.Now]. Enables unit tests that
// exercise specific HLC branches (wall advance vs logical
// increment, peer-ahead vs local-ahead) without depending on
// real-clock cadence.
//
// source must return monotonically non-decreasing values; HLC's
// logical counter is what guarantees strict monotonicity even when
// source repeats a value across calls.
func NewWithSource(node clock.NodeID, source func() int64) *Clock {
	return &Clock{node: node, nowFn: source}
}

// nowUnixNano is the default wall-time source. Pulled out as a
// package-level function so the Clock struct does not capture an
// inline closure on every construction.
func nowUnixNano() int64 { return time.Now().UnixNano() }

// Now returns the next HLC [clock.Instant]. If the wall-time source
// has advanced past the last issued wall, the new wall is adopted
// and the logical counter resets to 1; otherwise the logical
// counter increments.
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
		c.logical++
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
// # Allocation contract
//
// Zero alloc.
func (c *Clock) Update(observed clock.Instant) clock.Instant {
	now := c.nowFn()

	c.mu.Lock()
	defer c.mu.Unlock()

	newWall := max(c.wall, observed.Wall, now)
	localMax := newWall == c.wall
	observedMax := newWall == observed.Wall

	switch {
	case !localMax && !observedMax:
		// Physical clock advanced past both — fresh tick.
		c.logical = 1
	case localMax && observedMax:
		// Tied — take the higher logical and step.
		c.logical = max(c.logical, observed.Logical) + 1
	case localMax:
		c.logical++
	default:
		// observedMax
		c.logical = observed.Logical + 1
	}
	c.wall = newWall

	return clock.Instant{Wall: c.wall, Logical: c.logical, Node: c.node}
}
