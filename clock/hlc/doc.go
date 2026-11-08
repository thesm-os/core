// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package hlc provides a [clock.Clock] with Hybrid Logical Clock
// semantics over a [Physical] clock: the system clock in production, or a
// physical clock that the caller supplies.
//
// The HLC guarantees that every [clock.Instant] returned is strictly
// greater than all previously-issued instants from the same node,
// even when:
//
//   - The system clock is coarse (multiple calls within the same
//     nanosecond).
//   - The system clock jumps backward (NTP correction, VM
//     migration).
//   - A peer's observed instant is ahead of the local wall clock,
//     within the max offset of the clock.
//   - The logical counter reaches 2^32 - 1. The wall then moves
//     forward by a nanosecond.
//
// HLC achieves this by tracking the maximum of (last issued wall,
// physical wall) and incrementing a Lamport logical counter when the
// wall component does not advance. [Clock.Update] refuses a peer
// instant further ahead of the physical wall than the max offset, with
// [clock.ErrOffset], so one peer with a clock far ahead cannot move
// the clock ahead.
//
// [Clock.Elapsed] and [Clock.NewTimer] read the durations of the physical
// clock, so a step of the wall clock does not change a duration, and a
// timer ends on the time of Elapsed.
//
// # Construction
//
//   - [New] returns a [clock.Clock] tagged with the given [clock.NodeID]
//     and a max offset over the system clock: wall time from [time.Now],
//     and the monotonic time and the timers of the system.
//   - [NewWithSource] accepts a custom wall-time source for unit tests
//     that exercise specific HLC branch logic without depending on
//     real-clock cadence. Its durations and timers are those of the
//     system.
//   - [NewOver] reads every time from a physical clock of the caller,
//     such as the clock of a simulated host or a
//     [go.thesmos.sh/core/clock/fake.Clock], so it reads nothing from the
//     system.
package hlc
