// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package clock

import "time"

// DefaultMaxOffset is the max offset of a [Clock] in a deployment whose
// clocks NTP keeps within 500 ms of each other. [Clock.Update] refuses a
// peer instant whose wall is further ahead of the physical time of the
// clock than its max offset.
const DefaultMaxOffset = 500 * time.Millisecond

// Clock is the seam of time. Code reads time and schedules timers through
// a Clock instead of the standard library, so it runs against the wall
// clock in production and against a virtual clock in tests.
//
// # Method semantics
//
//   - [Clock.Now] returns the current [Instant] with its logical counter
//     and its node. An HLC implementation advances Logical when Wall does
//     not move forward, so its instants increase strictly also when the
//     system clock is coarse or steps back.
//   - [Clock.Time] returns the current wall time as a [time.Time]. It
//     returns Now().Time(), the form that most callers need.
//   - [Clock.Elapsed] returns the time since an origin that the clock
//     fixes at its construction. A caller measures a duration as the
//     difference of two readings. A step of the wall time does not
//     change it.
//   - [Clock.NewTimer] returns a [Timer] that sends its tick once at
//     least d has elapsed on the time source of the clock. A production
//     clock returns a timer of [time.NewTimer]. A virtual clock sends the
//     tick when the test advances its time.
//   - [Clock.Update] merges the instant of a peer into the clock and
//     returns an instant after the instant of the peer. Update refuses a
//     peer instant beyond the max offset of the clock.
//
// # Concurrency
//
// Implementations must be safe for concurrent use. A dispatcher, the
// handlers of requests and background workers call [Clock.Now] at once,
// and the implementation serialises the calls.
//
// # Allocation contract
//
// [Clock.Now], [Clock.Time], [Clock.Elapsed] and [Clock.Update] must
// not allocate. [Clock.NewTimer] is the only constructor on the hot
// path, and it may allocate.
//
//dokimi:stub
//dokimi:suite
//dokimi:out path=../coretest/clocktest/
type Clock interface {
	// Now returns the current HLC instant.
	//
	//dokimi:mixin time-aware
	Now() Instant

	// Time returns the current wall time, which is Now().Time(). Most
	// callers need only the [time.Time].
	//
	//dokimi:mixin time-aware
	Time() time.Time

	// Elapsed returns the time since an origin that the clock fixes
	// once, at its construction. The difference of two readings is the
	// duration between them. Elapsed never decreases, and a step of the
	// wall time does not change it.
	//
	//dokimi:mixin time-aware
	Elapsed() time.Duration

	// NewTimer returns a Timer that sends its tick once at least d has
	// elapsed on the time source of the clock.
	NewTimer(d time.Duration) Timer

	// Update merges an observed peer instant into the local clock
	// and returns the resulting instant, which is causally after the
	// observed instant.
	//
	// Update returns [ErrOffset] and the zero Instant, and leaves the
	// clock unchanged, when the wall of observed is further ahead of
	// the physical time of the clock than its max offset. A caller
	// refuses the message that contains such an instant.
	//
	//dokimi:refuse zero-on-error reason="Update fails only for a peer instant beyond the max offset of the clock"
	Update(observed Instant) (Instant, error)
}

// Timer delivers one tick on its channel after a delay. Code that creates
// its timers with [Clock.NewTimer] receives the ticks of the wall clock in
// production and the ticks of a virtual clock in tests.
//
// # Standard-library parity
//
// Each method has the semantics of the method of the same name of a
// [time.Timer] of Go 1.23 and later, so a production implementation
// wraps the standard library:
//
//   - [Timer.C] returns the tick channel. A receive after Reset or
//     Stop returns never yields a tick from before that call.
//   - [Timer.Reset] schedules the tick for d after the call and drops
//     an unreceived tick. It returns true if the timer was active at
//     the time of the call. An active timer is pending, or it has sent
//     a tick that nobody received.
//   - [Timer.Stop] cancels a pending tick and drops an unreceived tick.
//     It returns true if the timer was active, and false if its tick
//     was received or the timer was stopped.
//
// # Concurrency
//
// A Timer is safe for one goroutine at a time, as a [time.Timer] is. A
// caller creates a Timer for each wait and serialises the calls of
// [Timer.C], [Timer.Reset] and [Timer.Stop] on it.
//
//dokimi:stub
//dokimi:suite
//dokimi:out path=../coretest/clocktest/
type Timer interface {
	// C returns the channel on which the tick is delivered.
	C() <-chan time.Time

	// Reset schedules the tick for d after the call. It reports whether
	// the timer was active at the time of the call.
	Reset(d time.Duration) bool

	// Stop cancels a pending tick and drops an unreceived tick. It
	// reports whether the timer was active.
	//
	//dokimi:mixin side-effect
	Stop() bool
}
