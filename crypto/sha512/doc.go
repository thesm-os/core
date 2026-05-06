// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package sha512 provides SHA-384 and SHA-512 [crypto.Hasher]
// implementations backed by [crypto/sha512].
//
// SHA-384 is the truncated variant of SHA-512 that NSA's CNSA
// 2.0 suite mandates for new National Security Systems through
// the post-quantum transition. SHA-512 is the full 64-byte
// variant, used where 256 bits of collision resistance is the
// design margin.
//
// All methods on every Hasher are zero-allocation on the hot
// path; [crypto.Hasher.NewStream] allocates the underlying hash
// state once.
package sha512
