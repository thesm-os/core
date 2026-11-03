// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package xaesgcm provides an XAES-256-GCM [crypto.AEAD], as the C2SP
// specification at https://c2sp.org/XAES-256-GCM defines it.
//
// XAES-256-GCM derives an AES-256-GCM key for each message from its key and
// the first 96 bits of a 192-bit nonce, with the counter-mode KDF of NIST SP
// 800-108r1 over CMAC-AES-256. One key then seals about 2^80 messages with
// random nonces at a collision risk of 2^-32. AES-GCM with random 96-bit
// nonces seals about 2^32 messages at that risk.
//
// # Nonces
//
// Use [crypto.Seal] and [crypto.Open]. The 192-bit nonce has two halves:
//
//   - The first 96 bits are the nonce of the [crypto.AEAD], and crypto.Seal
//     reads them from its random source. The key derivation reads them.
//   - The FIPS 140-3 module of the standard library generates the last 96
//     bits, the nonce of AES-256-GCM under the derived key, and Seal writes
//     them before the ciphertext.
//
// That keeps the AEAD valid in FIPS 140-only mode, which refuses AES-GCM with
// a nonce from the caller. The bytes that follow the first half are the
// output of the specification for the whole nonce, so another
// implementation of XAES-256-GCM opens them.
//
// Reusing a 192-bit nonce under one key breaks XAES-256-GCM as it breaks
// AES-GCM. XAES-256-GCM does not commit to its key.
//
// # Concurrency
//
// The [crypto.AEAD] of [New] is safe for concurrent use. It has no mutable
// state after New returns.
//
// # Allocation contract
//
// Seal and Open allocate the AES-256 block and the GCM state of the key that
// each message derives, 2 objects. The public API of [crypto/aes] and
// [crypto/cipher] cannot set a new key into an existing block.
//
// # Errors
//
// [New] returns [go.thesmos.sh/core/crypto.ErrKeySize] for a key that is
// not 32 bytes long.
//
// # Dependency position
//
// Imports crypto/aes and crypto/cipher from the standard library, and
// go.thesmos.sh/core/crypto and go.thesmos.sh/core/pool from this module.
package xaesgcm
