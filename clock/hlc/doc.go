// Copyright Thesmos 2026
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
//   - A peer's observed instant is ahead of the local wall clock.
//
// HLC achieves this by tracking the maximum of (last issued wall,
// system wall) and incrementing a Lamport logical counter when the
// wall component does not advance.
//
// # Construction
//
// [New] returns a [clock.Clock] tagged with the given [clock.NodeID]
// and reading wall time from [time.Now]. [NewWithSource] accepts a
// custom wall-time source for unit tests that exercise specific
// HLC branch logic without depending on real-clock cadence.
package hlc
