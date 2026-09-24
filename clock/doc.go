// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package clock defines the time seam used by every thesmos library.
//
// The seam exists so libraries can be tested deterministically: the
// production [Clock] reads wall time and schedules timers via the
// standard library, while [go.thesmos.sh/core/clock/fake.Clock] in
// tests advances virtual time on demand without real-time blocking.
//
// # Hybrid Logical Clock
//
// The values returned by [Clock.Now] are [Instant] values that carry
// both the wall-clock time and a Lamport-style logical counter,
// tagged with the originating [NodeID]. That gives callers a total
// causal ordering across nodes through [Instant.HappensBefore]. A
// caller that needs only wall time uses [Clock.Time] or [Instant.Time]
// for the stdlib [time.Time] view.
//
// Implementations that do not need distributed causal ordering, such
// as the fake clock used in tests, may treat [Clock.Update] as a no-op
// and emit instants whose [Instant.Logical] field counts calls. The
// interface contract is uniform, and the strength of the ordering
// guarantee depends on the implementation.
//
// # UTC with an error bound
//
// A [Clock] states nothing about how far its time is from UTC. A
// [UTCSource] returns a [UTCReading]: a time, a bound on its distance
// from UTC, and whether the source considers the clock synchronised. A
// caller that stamps records under a rule on clock accuracy checks
// [UTCReading.Within] before it stamps. UTCSource is a separate
// interface, so every Clock keeps its contract.
//
// # Provided implementations
//
//   - go.thesmos.sh/core/clock/hlc: the production HLC clock built on
//     [time.Now].
//   - go.thesmos.sh/core/clock/fake: a deterministic virtual-time clock
//     for tests. It is also a [UTCSource] whose error a test sets.
//   - go.thesmos.sh/core/clock/kernel: a [UTCSource] over the Linux
//     kernel's time discipline.
//
// # Allocation contract
//
// [Clock.Now], [Clock.Time], and [Clock.Update] must not allocate.
// [Clock.NewTimer] is a cold-path constructor and may allocate the
// underlying timer struct and channel. [Instant] and [UTCReading] are
// value types passed by value.
package clock
