// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package ulid provides an [id.Generator] producing ULIDs —
// Universally Unique Lexicographically-Sortable Identifiers.
//
// # Wire format
//
// 128 bits = 16 bytes:
//
//	bytes 0..5 : 48-bit Unix-millisecond timestamp, big-endian
//	bytes 6..15: 80 random bits
//
// The big-endian timestamp prefix means bytewise lexicographic
// order matches generation order: sort a list of ULIDs and you
// get them in chronological order, no separate timestamp column
// needed. This is the load-bearing property versus UUID — index
// locality on insert, "newest first" queries by trailing key
// scan, monotonic stream IDs.
//
// # Encoding
//
// [Format] returns the canonical 26-character Crockford base32
// encoding. The raw [id.ID] bytes are the underlying 16-byte
// ULID; consumers that store the bytes directly need no
// conversion.
//
// # Construction
//
// [Generator] depends on a [clock.Clock] (for the timestamp) and
// a [rand.Rand] (for the entropy). Production callers wire the
// real clock and a CSPRNG-grade [rand/crypto.Rand]; tests wire
// [clock/fake.Clock] and [rand/constant.Rand] / [rand/seeded.Rand]
// for deterministic identifier streams.
//
// # Comparison with UUIDv4
//
// Use ULID when:
//
//   - Identifiers are stored sorted (database primary keys,
//     append-only logs, time-series indices).
//   - "Newest first" queries are common.
//   - Index locality on insert matters.
//
// Use UUIDv4 ([id/uuidv4]) when:
//
//   - The identifier should NOT leak temporal information.
//   - Generation order should not be inferable from the bytes.
//
// # Monotonicity within a millisecond
//
// Two ULIDs minted in the same millisecond have unrelated random
// suffixes and may sort in either order. ULIDs do NOT guarantee
// strict monotonicity within a millisecond; consumers that need
// strict per-producer monotonicity compose ULID with
// [go.thesmos.sh/core/epoch.Counter] or use HLC instants from
// [go.thesmos.sh/core/clock].
package ulid
