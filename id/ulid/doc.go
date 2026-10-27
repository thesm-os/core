// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package ulid provides an [id.Generator] of the ULIDs of the ULID
// specification and [ULID], their value type. A ULID has a 48-bit Unix
// timestamp in milliseconds, followed by 80 random bits.
//
// # Layout
//
//	bytes 0-5:  the Unix milliseconds, big-endian
//	bytes 6-15: 80 random bits
//
// The bytes of ULIDs sort in the order of their milliseconds. An index of
// ULIDs takes new keys at its end, and a scan from the end returns the newest
// first. [TimestampMillis] returns the milliseconds.
//
// # When to use
//
// Use a ULID when identifiers are stored sorted, as the keys of an index, a
// log or a time series. A ULID reveals its creation time to the millisecond.
// Use [go.thesmos.sh/core/id/uuidv4] when an identifier must not reveal when
// it was created.
//
// Two ULIDs of the same millisecond have unrelated random bits and sort in
// either order. A caller that needs a strict order of the ULIDs of one
// producer combines a ULID with [go.thesmos.sh/core/epoch.Counter], or uses
// the HLC instants of [go.thesmos.sh/core/clock].
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
// [Format] returns the canonical text of the ULID specification: 26
// characters of Crockford's base32 in uppercase. [ParseULID] reads it back
// in either case and with the substitutions of the specification.
// [AppendFormat] appends the same text to a caller's buffer and does not
// allocate when the buffer has room for it. The bytes of the [id.ID] are the
// 16 bytes of the ULID, so a caller that stores the bytes does not convert
// them.
//
// # Value type
//
// [ULID] is a ULID as an array of its 16 bytes. [FromID] converts an [id.ID]
// of 128 bits to a ULID, and [ULID.ID] converts the ULID back. A struct that
// embeds ULID encodes as the ULID in kanon, JSON and gob. A map keyed by such
// a struct hashes and compares 16 bytes. The zero ULID means no ULID. Its
// binary form is no bytes, and its text is 26 zeros.
//
//dokimi:sentinel
package ulid
