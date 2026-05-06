// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package sha256 provides an HMAC-SHA-256 [crypto.MAC]
// implementation backed by [crypto/hmac] and [crypto/sha256].
//
// [New] takes the keying material and returns a [MAC] bound to
// it. The key is copied at construction; callers may zero or
// reuse the source buffer immediately afterwards.
//
// [MAC.Sign] and [MAC.Verify] each allocate the underlying HMAC
// state once per call (the stdlib has no zero-allocation
// HMAC primitive analogous to `sha256.Sum256`). [MAC.NewStream]
// allocates the wrapper plus the underlying HMAC state on
// construction; subsequent [crypto.Stream.Write] / [crypto.Stream.Sum]
// / [crypto.Stream.Reset] calls reuse them and are zero-alloc.
// Hot-path consumers should construct one stream per goroutine
// and reuse it across Reset cycles.
//
// All keys lengths are accepted; HMAC handles short and long
// keys per RFC 2104. Cryptographic guidance recommends a key at
// least as long as the underlying hash output (32 bytes for
// HMAC-SHA-256), and at minimum 16 bytes of high-entropy
// material. Enforcing a minimum is a higher-layer policy
// concern; this seam does not reject short keys.
package sha256
