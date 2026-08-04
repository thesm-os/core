// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package ksuid provides an [id.Generator] producing K-Sortable
// Unique Identifiers — 160-bit time-sortable identifiers with a
// 4-byte Unix-second timestamp prefix and a 16-byte random
// payload.
//
// # Wire format
//
// 160 bits = 20 bytes:
//
//	bytes  0..3: 32-bit Unix-second timestamp (big-endian) since
//	             the KSUID epoch (1400000000 = 2014-05-13Z UTC).
//	             Provides ~136 years of valid range from epoch.
//	bytes  4..19: 128 random bits.
//
// The big-endian timestamp prefix means bytewise lexicographic
// order matches generation order at second granularity. Two
// KSUIDs minted within the same second sort by their random
// suffix (effectively random order); KSUID does NOT guarantee
// strict monotonicity within a second.
//
// # Encoding
//
// [Format] returns the canonical 27-character base62 encoding
// using the alphabet "0..9A..Za..z". The raw [id.ID] bytes are
// the underlying 20-byte KSUID; consumers that store the bytes
// directly need no conversion.
//
// # Comparison with ULID
//
// KSUID and ULID are both time-sortable 160-/128-bit identifiers
// with random suffixes. Differences:
//
//   - **Range**: KSUID's 4-byte Unix-second timestamp covers
//     ~136 years; ULID's 6-byte millisecond timestamp covers
//     ~10889 years. ULID rolls over later but uses 2 more bytes
//     for the timestamp.
//   - **Granularity**: KSUID sorts at second granularity; ULID
//     sorts at millisecond granularity. ULID's intra-second
//     sub-sort is also random, so the practical difference is
//     a higher chance of "ties" in sort order under ULID for
//     bursts of inserts within a single millisecond — but ULID
//     wins on event ordering at sub-second granularity.
//   - **Entropy**: KSUID has 128 random bits; ULID has 80. KSUID
//     is more collision-resistant within a single second.
//   - **Encoding**: KSUID is 27-char base62 (alphanumeric);
//     ULID is 26-char Crockford base32 (no I, L, O, U).
//
// Use KSUID when consumers want a longer entropy field (high-
// throughput producers minting many IDs per second) or when
// alphanumeric encoding is desirable for URL paths and database
// keys. Use ULID ([id/ulid]) when sub-second sort granularity
// matters or when humans transcribe identifiers manually
// (Crockford base32 is more typo-resistant).
//
// # Construction
//
// [Generator] depends on a [clock.Clock] (for the timestamp)
// and a [rand.Rand] (for the entropy). Production callers wire
// the real clock and a CSPRNG-grade [rand/crypto.Rand]; tests
// wire [clock/fake.Clock] and [rand/constant.Rand] /
// [rand/seeded.Rand] for deterministic identifier streams.
package ksuid
