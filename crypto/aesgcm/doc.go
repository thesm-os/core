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
// Use [crypto.Seal] and [crypto.Open], which draw a fresh nonce per
// message and prepend it. The embedded [cipher.AEAD] methods are
// available for callers managing nonces themselves; reusing a nonce
// under one key breaks the construction completely.
//
// # Concurrency
//
// The returned [crypto.AEAD] is safe for concurrent use. It holds no
// mutable state — the underlying [cipher.AEAD] is read-only after
// construction.
package aesgcm
