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
// # Performance vs SHA-2
//
// SHA-3 has no hardware acceleration on x86 today (no SHA-NI
// equivalent for Keccak), so software-only performance applies.
// On hardware with SHA-NI (every modern x86), throughput
// roughly:
//
//   - SHA3-256 vs SHA-256:   ≈ 4× slower (~600 MB/s vs 2.8 GB/s)
//   - SHA3-512 vs SHA-512:   ≈ 4× slower (~320 MB/s vs 1.4 GB/s)
//
// Prefer SHA-256 / SHA-512 unless the protocol specifically
// requires SHA-3 (PQC parameter expansion, sponge-based
// construction, NIST CNSA 2.0 algorithm-diversity mandate).
// On platforms without SHA-NI (older AMD, some ARM cores) the
// gap narrows but does not close.
//
// All methods on every Hasher are zero-allocation on the hot
// path; [crypto.Hasher.NewStream] allocates the underlying hash
// state once.
package sha3
