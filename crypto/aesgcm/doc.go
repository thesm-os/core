// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package aesgcm provides an AES-GCM [crypto.AEAD] implementation
// backed by [crypto/aes] and [crypto/cipher].
//
// AES-128-GCM and AES-256-GCM are the two authenticated-encryption
// constructions the Go standard library can express, and both are
// NIST-approved under SP 800-38D. The key length passed to [New]
// selects between them, so a caller rotating from 128- to 256-bit
// keys changes one input rather than one type.
//
// On hardware with AES-NI and CLMUL — every current x86-64 and
// arm64 server part — the standard library uses them, and GCM runs
// at several GB/s. Elsewhere it falls back to a constant-time
// software implementation, which is markedly slower but not
// vulnerable to cache-timing attacks.
//
// # Nonces
//
// Use [crypto.Seal] and [crypto.Open], which place a fresh nonce in
// every envelope. The two constructors differ in who generates it:
//
//   - [New] takes the nonce from the caller. crypto.Seal reads it from
//     the random source it is given.
//   - [NewRandomNonce] generates the nonce inside the standard library's
//     FIPS 140-3 module and prepends it to the ciphertext. It is the
//     only construction FIPS 140-only mode accepts, and crypto.Seal
//     reads no entropy for it.
//
// Both produce the same envelope bytes and open each other's
// envelopes. The embedded [cipher.AEAD] methods of a [New] AEAD are
// available for callers managing nonces themselves; reusing a nonce
// under one key breaks the construction completely.
//
// # Concurrency
//
// The returned [crypto.AEAD] is safe for concurrent use. It has no
// mutable state — the underlying [cipher.AEAD] is read-only after
// construction.
package aesgcm
