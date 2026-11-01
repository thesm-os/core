// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package clock

import "time"

// DefaultMaxOffset is the max offset of a [Clock] in a deployment whose
// clocks NTP keeps within 500 ms of each other. [Clock.Update] refuses a
// peer instant whose wall is further ahead of the physical time of the
// clock than its max offset.
const DefaultMaxOffset = 500 * time.Millisecond

// Clock is the unified time seam. Every thesmos library reads time
// and schedules timers through a Clock instead of calling the
// standard library directly, so the same code can run against the
// real wall clock in production and against a virtual clock in
// tests.
//
// # Method semantics
//
//   - [Clock.Now] returns the current [Instant], including HLC
//     logical counter and node identity. Production HLC
//     implementations advance Logical when Wall does not move
//     forward, guaranteeing strictly-monotone instants even under
//     coarse or backward-jumping system clocks.
//   - [Clock.Time] returns the current wall time as a stdlib
//     [time.Time]. Equivalent to Now().Time(); offered as a
//     first-class method because it is the most common use site
//     across the ecosystem.
//   - [Clock.Elapsed] returns the time since an origin that the clock
//     fixes at its construction. A caller measures a duration as the
//     difference of two readings. A step of the wall time does not
//     change it.
//   - [Clock.NewTimer] creates a [Timer] that fires after at least d
//     has elapsed against this clock's time source. Production
//     clocks delegate to [time.NewTimer]; virtual clocks fire only
//     when the harness advances virtual time.
//   - [Clock.Update] merges an observed peer instant and returns
//     the resulting "after observation" instant. HLC implementations
//     advance the local clock past the peer. Update refuses a peer
//     instant beyond the max offset of the clock.
//
// # Concurrency
//
// Implementations must be safe for concurrent use. Multiple
// goroutines call [Clock.Now] from the dispatcher, request handlers,
// and background workers; serialisation is the implementation's
// responsibility.
//
// # Allocation contract
//
// [Clock.Now], [Clock.Time], [Clock.Elapsed] and [Clock.Update] must
// not allocate. [Clock.NewTimer] is the only constructor on the hot
// path and is permitted to allocate.
//
//dokimi:stub
//dokimi:suite
//dokimi:out path=../coretest/clocktest/
type Clock interface {
	// Now returns the current HLC instant.
	Now() Instant

	// Time returns the current wall time. Equivalent to
	// Now().Time(); exists as a first-class method because most
	// callers only want the stdlib representation.
	Time() time.Time

	// Elapsed returns the time since an origin that the clock fixes
	// once, at its construction. The difference of two readings is the
	// duration between them. Elapsed never decreases, and a step of the
	// wall time does not change it.
	Elapsed() time.Duration

	// NewTimer creates a Timer that fires after at least d has
	// elapsed against this clock's time source.
	NewTimer(d time.Duration) Timer

	// Update merges an observed peer instant into the local clock
	// and returns the resulting instant, which is causally after the
	// observed instant.
	//
	// Update returns [ErrOffset] and the zero Instant, and leaves the
	// clock unchanged, when the wall of observed is further ahead of
	// the physical time of the clock than its max offset. A caller
	// refuses the message that contains such an instant.
	Update(observed Instant) (Instant, error)
}

// Timer is a single deferred event whose tick is delivered on its
// channel. Routing every timer through this interface is what
// decouples library code from the physical wall clock — the same
// code that calls [Clock.NewTimer] in production reads from a
// virtual-time channel under test.
//
// # Standard-library parity
//
// Every method mirrors the semantics of a [time.Timer] of Go 1.23 and
// later, so production implementations are a thin wrapper over the
// standard library:
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
// Method calls on a single Timer are not synchronised against each
// other or against [Timer.Reset] / [Timer.Stop] on the same
// instance. Callers hold a Timer per logical wait and serialise
// access at the call site. The underlying [time.Timer] in
// production has the same constraint.
//
//dokimi:stub
//dokimi:suite
//dokimi:out path=../coretest/clocktest/
type Timer interface {
	// C returns the channel on which the tick is delivered.
	C() <-chan time.Time

	// Reset reschedules the timer to fire after d. Returns true
	// if the timer was active at the time of the call.
	Reset(d time.Duration) bool

	// Stop cancels a pending tick and drops an unreceived tick. Returns
	// true if the timer was active.
	Stop() bool
}
