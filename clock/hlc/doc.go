// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package hlc provides a production [clock.Clock] backed by the
// system wall clock with Hybrid Logical Clock semantics.
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
// system wall) and incrementing a Lamport logical counter when the
// wall component does not advance. [Clock.Update] refuses a peer
// instant further ahead of the system wall than the max offset, with
// [clock.ErrOffset], so one peer with a clock far ahead cannot move
// the clock ahead.
//
// [Clock.Elapsed] reads the monotonic time of the system, so a step
// of the system clock does not change a duration.
//
// # Construction
//
// [New] returns a [clock.Clock] tagged with the given [clock.NodeID]
// and a max offset, and reads wall time from [time.Now].
// [NewWithSource] accepts a custom wall-time source for unit tests
// that exercise specific HLC branch logic without depending on
// real-clock cadence.
package hlc
