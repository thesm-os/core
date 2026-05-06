// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package pcg provides a PCG-backed [rand.Rand] suitable for
// non-cryptographic randomness.
//
// PCG (Permuted Congruential Generator) is fast and statistically
// excellent. It is NOT cryptographically secure — for keys, nonces,
// or tokens use [crypto.Rand] or [seeded.Rand]. PCG is appropriate
// for sampling, shuffling, A/B variant assignment, and fault
// injection.
//
// PCG is deterministic: two [pcg.Rand] instances constructed with
// the same seed produce bit-identical Uint64 streams.
//
// PCG is NOT safe for concurrent use; each goroutine should hold
// its own instance.
package pcg
