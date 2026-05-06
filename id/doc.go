// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package id defines [ID], the fixed-max-size identifier value
// type used across the thesmos ecosystem for time-sortable and
// random-collision-resistant identifiers, plus the [Generator]
// interface that produces them.
//
// # Three sizes in one type
//
// [ID] holds 128-, 160-, and 256-bit identifiers in a single
// comparable value type. The size is chosen at construction
// ([New128], [New160], [New256]) and reported by [ID.Size]; the
// active prefix is returned by [ID.Bytes]. The fixed-max layout
// costs slack for shorter identifiers but eliminates the
// slice-allocation, slice-aliasing, and map-key-incompatibility
// problems of a `[]byte`-shaped identifier.
//
// Identifiers shorter than 128 bits (uint64 counters) belong to
// [go.thesmos.sh/core/epoch]. Identifiers larger than 256 bits
// (Ed25519 public keys at 32 B fit in [Size256]; ML-DSA
// signatures and ML-KEM key shares are key material, not
// identifiers — those land in [crypto/sign] and [crypto/kem]
// when those seams ship).
//
// # Provided implementations
//
//   - [id/ulid] — 128-bit Crockford-base32 ULID (48-bit ms
//     timestamp || 80-bit randomness). Time-sortable; identifier
//     of choice for events, epochs, and stream entries.
//   - [id/uuidv4] — 128-bit RFC 4122 UUID v4 (122 random bits).
//     Non-sortable; identifier of choice when temporal
//     information should not leak.
//   - [id/ksuid] — 160-bit KSUID (32-bit Unix-second timestamp
//     since the KSUID epoch || 128-bit randomness), base62
//     encoded. Time-sortable at second granularity; identifier
//     of choice when consumers want a longer entropy field than
//     ULID provides.
//   - [id/fixed] — constant-output generator for fixtures and
//     deterministic tests.
//
// # Compile-time-distinct identifier types
//
// Consumers that want compile-time distinction between
// identifier vocabularies declare a typed alias and convert at
// the boundary:
//
//	type EpochID id.ID
//	type AccumulatorID id.ID
//
//	gen := ulid.New(clk, rng)
//	epochID := EpochID(gen.New())
//
// The [Generator] interface still produces [ID]; the consumer
// owns the typed conversion.
//
// # Allocation contract
//
// [ID] is a value type ([MaxSize]byte + uint8). Pass by value,
// comparable. [ID.IsZero], [ID.Size], [ID.Bytes], [ID.Compare],
// and [ID.Equal] are zero-allocation. [ID.String] allocates the
// encoded string.
//
// [Generator] implementations document their own allocation
// contracts; the canonical implementations in this module are
// zero-allocation per call when the underlying [rand.Rand.Uint64]
// is.
package id
