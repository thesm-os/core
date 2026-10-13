// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package constant provides deterministic [rand.Rand] sources for tests
// that need to assert against a specific randomness branch.
//
// A [Rand] returns the same Uint64 on every call; [Rand.Read]
// fills the buffer with the byte representation of that Uint64
// repeated.
//
// A [Counter] returns draws that differ in their low bits, while
// [rand.Float64] of each of its first 2,048 draws returns one value. A
// test passes a Counter to a component that draws both identifiers and
// jitter from one source.
//
// The value is constant per [Rand], not across the package: [New]
// takes the uint64 to return, so two Rands can differ.
//
// constant is for unit tests only. It is not cryptographically secure,
// and not statistically random: the draws of a Rand are identical, and
// the draws of a Counter differ in their low bits alone.
package constant
