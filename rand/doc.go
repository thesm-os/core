// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package rand defines the randomness seam used by every thesmos
// library.
//
// The seam exists so libraries can be tested deterministically: the
// production source draws bytes from [crypto/rand] (cryptographic)
// or a PCG generator (non-cryptographic); under test, callers
// substitute a deterministic [seeded.Rand] that produces the same
// byte stream from a known seed.
//
// # Crypto-grade vs. non-crypto-grade
//
// [Rand] is a shape, not a strength claim. Some implementations are
// cryptographically secure ([crypto], [seeded]); others are not
// ([pcg], [fixed]). Implementations document their security
// properties in their own godoc — pick the right implementation for
// your use case:
//
//   - Key generation, nonces, tokens, anything an attacker would
//     benefit from predicting → use [crypto] or [seeded].
//   - Sampling, shuffling, A/B variant assignment, fault injection
//     → [pcg] is faster and adequate.
//   - Tests asserting on specific randomness branches → [fixed].
//
// # Interface shape
//
// [Rand] exposes both [Rand.Uint64] for fast PRNG use and
// [Rand.Read] for byte-stream use. Implementations natively
// implement whichever is fastest and derive the other.
//
// # Allocation contract
//
// [Rand.Uint64] must not allocate. [Rand.Read] should not allocate
// per-call; implementations that need scratch space hold it on the
// receiver.
package rand
