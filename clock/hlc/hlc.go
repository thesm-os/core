// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package hlc

import (
	"math"
	"sync"
	"time"

	"go.thesmos.sh/core/clock"
)

// Physical is the physical clock of a [Clock]: the wall time that
// [Clock.Now] and [Clock.Update] merge, the durations of [Clock.Elapsed]
// and the timers of [Clock.NewTimer]. Every [clock.Clock] is a Physical, so
// a [go.thesmos.sh/core/clock/fake.Clock] can supply the virtual time of a
// test.
//
// # Concurrency
//
// The methods of a Physical must be safe for concurrent use, because the
// methods of a Clock call them without a lock.
type Physical interface {
	// Time returns the wall time.
	Time() time.Time

	// Elapsed returns the time since an origin that the physical clock
	// fixes once. Elapsed never decreases, and a step of the wall time
	// does not change it.
	Elapsed() time.Duration

	// NewTimer returns a Timer that sends its tick once Elapsed has
	// advanced by at least d.
	NewTimer(d time.Duration) clock.Timer
}

// Clock implements [clock.Clock] with Hybrid Logical Clock semantics over a
// [Physical] clock: the system clock of [New], the source of
// [NewWithSource], or the physical clock of [NewOver].
//
// Clock is safe for concurrent use by multiple goroutines.
//
// # Allocation contract
//
// [Clock.Now], [Clock.Time], [Clock.Elapsed] and [Clock.Update] are
// zero-allocation when the methods of the physical clock are. [Clock.NewTimer]
// allocates the Timer of the physical clock: for New and NewWithSource the
// wrapper struct and the underlying [time.Timer].
//
//dokimi:subject clock.Clock new=newClock
//dokimi:budget Clock.Now allocs=0
//dokimi:budget Clock.Time allocs=0
//dokimi:budget Clock.Elapsed allocs=0
//dokimi:budget Clock.Update allocs=0
type Clock struct {
	// physical is the physical clock that the Clock reads.
	physical Physical
	wall     int64

	// maxOffset is the furthest ahead of the physical time that Update
	// adopts the wall of a peer. It is not negative.
	maxOffset time.Duration

	mu      sync.Mutex
	logical uint32
	node    clock.NodeID
}

// Compile-time interface check.
var _ clock.Clock = (*Clock)(nil)

// New returns a production HLC [clock.Clock] tagged with node over the
// system clock. Wall time is read from [time.Now], and [Clock.Elapsed]
// reads the monotonic time of the system since the call of New.
// [Clock.Update] refuses a peer instant whose wall is more than maxOffset
// ahead of the wall time, and a maxOffset below zero counts as zero.
// [clock.DefaultMaxOffset] is the max offset of a deployment whose clocks
// NTP keeps within 500 ms of each other.
//
// # Allocation contract
//
// Allocates the Clock and its system clock.
func New(node clock.NodeID, maxOffset time.Duration) *Clock {
	return NewOver(node, maxOffset, system{origin: time.Now()})
}

// NewWithSource returns an HLC [clock.Clock] tagged with node that reads
// its wall time from source in place of [time.Now], with the max offset of
// [New]. A unit test of the merge sets the wall time of each call through
// source. [Clock.Elapsed] reads the monotonic time of the system since the
// call of NewWithSource, and [Clock.NewTimer] returns a timer of the
// system. A test that needs every time from its own clock uses [NewOver].
//
// source returns the Unix time in nanoseconds and must not decrease. The
// logical counter keeps the instants strictly increasing when source
// repeats a value.
//
// # Allocation contract
//
// Allocates the Clock and its physical clock.
func NewWithSource(node clock.NodeID, maxOffset time.Duration, source func() int64) *Clock {
	return NewOver(node, maxOffset, sourced{origin: time.Now(), wall: source})
}

// NewOver returns an HLC [clock.Clock] tagged with node over physical,
// with the max offset of [New]. [Clock.Now] and [Clock.Update] merge the
// Unix time of physical in nanoseconds, [Clock.Elapsed] returns the Elapsed
// of physical, and [Clock.NewTimer] returns its Timer. The Clock reads every
// time from physical, so two Clocks over equal physical clocks that read
// nothing from the system return equal instants and durations. A
// simulation runs each Clock over the clock of its host this way, and a
// test runs it over a [go.thesmos.sh/core/clock/fake.Clock].
//
// # Allocation contract
//
// Allocates the Clock.
func NewOver(node clock.NodeID, maxOffset time.Duration, physical Physical) *Clock {
	return &Clock{physical: physical, node: node, maxOffset: max(maxOffset, 0)}
}

// Now returns the next HLC [clock.Instant]. When the wall time of the
// physical clock is after the last wall of the clock, Now takes that wall
// and starts Logical at 1. Otherwise it adds 1 to Logical. After Logical
// 2^32 - 1 the wall moves forward by a nanosecond and Logical starts again
// at 1.
//
// # Allocation contract
//
// Zero alloc when the Time of the physical clock does not allocate.
func (c *Clock) Now() clock.Instant {
	now := c.physical.Time().UnixNano()

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

// Time returns the wall time of [Clock.Now] as a [time.Time], which is
// Now().Time(). It advances the state of the clock as Now does.
//
// # Allocation contract
//
// Zero alloc when the Time of the physical clock does not allocate.
func (c *Clock) Time() time.Time {
	return c.Now().Time()
}

// Elapsed returns the Elapsed of the physical clock: for [New] and
// [NewWithSource] the time since the constructor ran, on the monotonic
// time of the system. A step of the wall clock does not change it, and it
// takes no lock.
//
// # Allocation contract
//
// Zero alloc when the Elapsed of the physical clock does not allocate.
func (c *Clock) Elapsed() time.Duration {
	return c.physical.Elapsed()
}

// Update merges an observed peer instant into the local clock and
// returns the resulting instant.
//
// The merge follows the canonical HLC rule from Kulkarni et al.,
// "Logical Physical Clocks":
//
//	newWall = max(local.Wall, observed.Wall, physical)
//	newLogical follows the walls that equal newWall:
//	  - neither local nor observed → 1
//	  - both local and observed    → max(local.Logical, observed.Logical) + 1
//	  - local alone                → local.Logical + 1
//	  - observed alone             → observed.Logical + 1
//
// A Logical of 2^32 - 1 moves the wall forward by a nanosecond and
// starts Logical again at 1, as in [Clock.Now].
//
// Error modes: an observed wall more than the max offset ahead of the
// physical time returns [clock.ErrOffset] and the zero Instant, and leaves
// the clock unchanged. ErrOffset classifies as
// [go.thesmos.sh/core/errs.Integrity].
//
// # Allocation contract
//
// Zero alloc when the Time of the physical clock does not allocate.
func (c *Clock) Update(observed clock.Instant) (clock.Instant, error) {
	now := c.physical.Time().UnixNano()

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
		// The physical time is after both walls, so Logical starts at 1.
		c.wall = newWall
		c.logical = 1
	case localMax && observedMax:
		// The two walls are equal, so the larger Logical steps.
		c.step(max(c.logical, observed.Logical))
	case localMax:
		c.step(c.logical)
	default:
		// The wall of the peer is the largest.
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

// system is the physical clock of [New]: the wall time of [time.Now], the
// monotonic time of the system since origin, and the timers of
// [time.NewTimer]. origin is a reading of time.Now with its monotonic
// reading. system is safe for concurrent use, and its Time and Elapsed do
// not allocate.
type system struct {
	origin time.Time
}

var _ Physical = system{}

// Time returns [time.Now].
func (system) Time() time.Time { return time.Now() }

// Elapsed returns the time since origin on the monotonic clock of the
// system.
func (s system) Elapsed() time.Duration { return time.Since(s.origin) }

// NewTimer returns a [clock.Timer] over [time.NewTimer] of d. It allocates
// the wrapper struct and the underlying time.Timer.
func (system) NewTimer(d time.Duration) clock.Timer { return &timer{t: time.NewTimer(d)} }

// sourced is the physical clock of [NewWithSource]: the wall time of a
// source in nanoseconds since the Unix epoch, and the durations and the
// timers of the system. It is safe for concurrent use when the source is,
// and its Time does not allocate.
type sourced struct {
	system

	// wall is the source of the wall time.
	wall func() int64
}

var _ Physical = sourced{}

// Time returns the time of the source.
func (s sourced) Time() time.Time { return time.Unix(0, s.wall()) }
