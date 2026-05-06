// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package fixed provides a constant-output [rand.Rand] for tests
// that need to assert against a specific randomness branch.
//
// A [Rand] returns the same Uint64 on every call; [Rand.Read]
// fills the buffer with the byte representation of that Uint64
// repeated.
//
// fixed is for unit tests only. It is not cryptographically secure
// (the output is a constant) and not statistically random
// (subsequent draws are identical, not independent).
package fixed
