// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package crypto provides a [rand.Rand] backed by [crypto/rand].
//
// Use this implementation for cryptographic randomness: keys,
// nonces, tokens, anything an attacker would benefit from
// predicting. The byte stream is non-deterministic — there is no
// seed.
//
// [crypto/rand] is safe for concurrent use, so [Rand] is too.
package crypto
