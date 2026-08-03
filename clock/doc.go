// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package clock defines the time seam used by every thesmos library.
//
// The seam exists so libraries can be tested deterministically: the
// production [Clock] reads wall time and schedules timers via the
// standard library, while a [fake.Clock] in tests advances virtual
// time on demand without real-time blocking.
//
// # Hybrid Logical Clock
//
// The values returned by [Clock.Now] are [Instant] values that carry
// both the wall-clock time and a Lamport-style logical counter,
// tagged with the originating [NodeID]. This is enough to give
// callers a total causal ordering across nodes — see
// [Instant.HappensBefore] — while remaining transparent to callers
// that only care about wall time, which can use [Clock.Time] or
// [Instant.Time] for the stdlib [time.Time] view.
//
// Implementations that do not need distributed causal ordering (the
// [fake] clock used in tests, for example) may treat [Clock.Update]
// as a no-op and emit instants whose [Instant.Logical] field simply
// counts calls. The interface contract is uniform; the strength of
// the ordering guarantee depends on the implementation.
//
// # Provided implementations
//
// The [hlc] sub-package provides the production HLC clock built on
// [time.Now]. The [fake] sub-package provides a deterministic
// virtual-time clock for tests. Both implement [Clock].
//
// # Allocation contract
//
// [Clock.Now], [Clock.Time], and [Clock.Update] must not allocate.
// [Clock.NewTimer] is a cold-path constructor and may allocate the
// underlying timer struct and channel. [Instant] is a value type
// passed by value.
package clock
