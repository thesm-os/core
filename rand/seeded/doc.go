// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package seeded provides a deterministic CSPRNG-quality
// [rand.Rand] for simulation and unit tests.
//
// The generator is HMAC-SHA-256(seed, counter) emitting 32 bytes
// per block. Same seed → bit-identical byte stream across runs and
// Go versions; the underlying primitive is in the standard library
// so behaviour does not depend on a third-party crypto library.
//
// # When to use seeded over pcg
//
// Use [seeded.Rand] when:
//
//   - The test asserts on cross-version reproducibility (the math/
//     rand/v2 PCG output is implementation-defined and may change
//     across Go versions; HMAC-SHA-256 is fixed).
//   - The test or simulation injects randomness into a
//     cryptographic context (key derivation, nonce sampling for
//     security tests). seeded matches CSPRNG strength but with
//     deterministic seeding.
//
// Otherwise [pcg.Rand] is faster.
//
// [seeded.Rand] is safe for concurrent use.
package seeded
