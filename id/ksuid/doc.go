// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package ksuid provides an [id.Generator] of K-Sortable Unique Identifiers
// and [KSUID], their value type. A KSUID has 160 bits: a 32-bit timestamp in
// seconds since [Epoch], followed by 128 random bits.
//
// # Layout
//
//	bytes 0-3:  the seconds since Epoch, big-endian
//	bytes 4-19: 128 random bits
//
// The bytes of KSUIDs sort in the order of their seconds. [Epoch] is the Unix
// second 1400000000, 2014-05-13T16:53:20Z, and the 32-bit timestamp ends in
// 2150. Two KSUIDs of the same second have unrelated random bits and sort in
// either order. [TimestampSeconds] returns the Unix seconds.
//
// # When to use
//
// A KSUID and a ULID both sort by their time of creation. A KSUID sorts by
// the second and has 128 random bits. A ULID of [go.thesmos.sh/core/id/ulid]
// sorts by the millisecond and has 80 random bits. Use a KSUID for a producer
// that creates many identifiers per second, or for an alphanumeric text in
// URL paths and keys. Use a ULID for an order within a second, or for a text
// that people transcribe, because its decoder accepts the common misreadings
// of Crockford's base32.
//
// # Construction
//
// [Generator] reads the time from a [clock.Clock] and entropy from a
// [rand.Rand]. Production callers pass a real clock and
// [go.thesmos.sh/core/rand/crypto.Rand]. Tests pass
// [go.thesmos.sh/core/clock/fake.Clock] and
// [go.thesmos.sh/core/rand/constant.Rand] or
// [go.thesmos.sh/core/rand/seeded.Rand] for a deterministic stream of
// identifiers.
//
// # Encoding
//
// [Format] returns the canonical text: 27 characters of the base62 alphabet
// "0..9A..Za..z". [Parse] reads it back and is case-sensitive.
// [AppendFormat] appends the same text to a caller's buffer and does not
// allocate when the buffer has room for it. The bytes of the [id.ID] are the
// 20 bytes of the KSUID, so a caller that stores the bytes does not convert
// them.
//
// # Value type
//
// [KSUID] is a KSUID as an array of its 20 bytes. [FromID] converts an
// [id.ID] of 160 bits to a KSUID, and [KSUID.ID] converts the KSUID back. A
// struct that embeds KSUID encodes as the KSUID in kanon, JSON and gob. A map
// keyed by such a struct hashes and compares 20 bytes. The zero KSUID means
// no KSUID. Its binary form is no bytes, and its text is 27 zeros.
package ksuid
