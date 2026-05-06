// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package sha3 provides SHA3-256, SHA3-384, and SHA3-512
// [crypto.Hasher] implementations backed by [crypto/sha3] (Go
// 1.24+).
//
// The SHA-3 family (FIPS 202) is structurally distinct from the
// SHA-2 family: it uses the Keccak sponge construction rather
// than Merkle-Damgård, eliminating the length-extension property
// that constrains SHA-2 protocols. SHA3 is the right default
// when domain-separated hashing or post-quantum future-proofing
// matters; SHA-256 / SHA-512 remain appropriate where existing
// SHA-2 deployments dictate the choice.
//
// All methods on every Hasher are zero-allocation on the hot
// path; [crypto.Hasher.NewStream] allocates the underlying hash
// state once.
package sha3
