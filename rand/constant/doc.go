// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package constant provides a constant-output [rand.Rand] for tests
// that need to assert against a specific randomness branch.
//
// A [Rand] returns the same Uint64 on every call; [Rand.Read]
// fills the buffer with the byte representation of that Uint64
// repeated.
//
// The value is constant per [Rand], not across the package: [New]
// takes the uint64 to return, so two Rands can differ.
//
// constant is for unit tests only. It is not cryptographically
// secure (the output is a constant) and not statistically random
// (subsequent draws are identical, not independent).
package constant
