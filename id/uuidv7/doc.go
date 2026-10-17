// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package uuidv7 provides an [id.Generator] of the version-7 UUIDs of
// RFC 9562 and [UUID], their value type. A UUIDv7 has a 48-bit Unix
// timestamp in milliseconds, followed by 74 bits that order the IDs of one
// generator and keep them unique.
//
// # Layout
//
//	bytes 0-5:  unix_ts_ms, the Unix milliseconds, big-endian
//	byte  6:    version 7 in the high 4 bits, then the high 4 bits of rand_a
//	byte  7:    the low 8 bits of rand_a
//	byte  8:    variant 10 in the high 2 bits, then the high 6 bits of rand_b
//	bytes 9-15: the low 56 bits of rand_b
//
// A [Generator] writes the fraction of the millisecond into the 12 bits of
// rand_a, and 62 random bits into rand_b, so the bytes of its IDs sort in
// the order of their creation. [TimestampMillis] returns unix_ts_ms, and
// [Valid] checks the version and the variant.
//
// # When to use
//
// Use a UUIDv7 when identifiers must be UUIDs and their byte order must
// follow their order of creation, as for the keys of an index or a log.
// A UUIDv7 reveals its creation time to the millisecond. Use
// [go.thesmos.sh/core/id/uuidv4] when an identifier must not reveal when
// it was created.
//
// # Construction
//
// [Generator] reads the time from a [clock.Clock] and entropy from a
// [rand.Rand]. Production callers pass [go.thesmos.sh/core/clock/hlc.Clock]
// and [go.thesmos.sh/core/rand/crypto.Rand]. Tests pass
// [go.thesmos.sh/core/clock/fake.Clock] and
// [go.thesmos.sh/core/rand/seeded.Rand] for a deterministic stream of
// identifiers.
//
// # Encoding
//
// [Format] returns the text form of RFC 9562,
// "xxxxxxxx-xxxx-7xxx-yxxx-xxxxxxxxxxxx", where y is 8, 9, a or b, and
// [Parse] reads it back. [AppendFormat] appends the same text to a
// caller's buffer and does not allocate when the buffer has room for it.
// The bytes of the [id.ID] are the 16 bytes of the UUID, so a caller that
// stores the bytes does not convert them.
//
// # Value type
//
// [UUID] is a UUID as an array of its 16 bytes. [FromID] converts an [id.ID]
// of 128 bits to a UUID, and [UUID.ID] converts the UUID back. A struct that
// embeds UUID encodes as the UUID in kanon, JSON and gob. A map keyed by such
// a struct hashes and compares 16 bytes. The zero UUID means no UUID. Its
// binary form is no bytes, and its text is the text of the Nil UUID.
//
// The codecs of UUID accept a UUID of any version, as [Parse] does.
// [UUID.Valid] reports whether a UUID is a UUIDv7.
package uuidv7
