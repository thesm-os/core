// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package id defines [ID], one value type for identifiers of 128, 160 and
// 256 bits, and the [Generator] interface that produces them.
//
// # Sizes
//
// [ID] stores an identifier of 128, 160 or 256 bits in one comparable
// value type. The constructors [New128], [New160] and [New256] set the
// size, [ID.Size] returns it, and [ID.Bytes] returns the active prefix.
// The fixed array leaves bytes unused for the shorter sizes. In exchange,
// an ID does not allocate a slice or alias its caller's bytes, and it is
// a valid map key, which a []byte is not.
//
// Identifiers shorter than 128 bits, such as uint64 counters, belong to
// [go.thesmos.sh/core/epoch]. A 32-byte Ed25519 public key fits in
// [Size256]. Values wider than 256 bits, such as ML-DSA public keys and
// signatures, are key material, and [go.thesmos.sh/core/crypto/sign]
// handles them.
//
// # Provided implementations
//
//   - [go.thesmos.sh/core/id/ulid] generates 128-bit ULIDs: a 48-bit
//     timestamp in milliseconds and 80 random bits, in Crockford base32.
//     They sort by creation time to the millisecond.
//   - [go.thesmos.sh/core/id/uuidv4] generates the 128-bit version-4
//     UUIDs of RFC 9562, with 122 random bits. They reveal nothing about
//     their creation time.
//   - [go.thesmos.sh/core/id/uuidv7] generates the 128-bit version-7 UUIDs
//     of RFC 9562: a 48-bit timestamp in milliseconds, the fraction of the
//     millisecond in 12 bits, and 62 random bits. The IDs of one generator
//     increase in byte order.
//   - [go.thesmos.sh/core/id/ksuid] generates 160-bit KSUIDs: a 32-bit
//     timestamp in seconds since the KSUID epoch and 128 random bits, in
//     base62. They sort by creation time to the second.
//   - [go.thesmos.sh/core/id/constant] returns one fixed ID, for fixtures
//     and deterministic tests.
//
// # Compile-time-distinct identifier types
//
// A consumer that needs identifier types the compiler keeps apart embeds
// [ID] in a struct:
//
//	type EpochID struct{ id.ID }
//	type AccumulatorID struct{ id.ID }
//
//	gen := ulid.New(clk, rng)
//	epochID := EpochID{gen.Generate()}
//
// Each struct has every method of [ID] through promotion and is
// comparable, and neither assigns to the other. A [Generator] returns an
// [ID], which the consumer wraps.
//
// A defined type, such as type EpochID id.ID, does not work. It has none
// of the methods of [ID], and code outside this package cannot construct
// one, because the fields of [ID] are unexported.
//
// # Decoding an identifier
//
// [FromBytes] builds an [ID] from a byte slice, for an identifier read
// from a wire or a database. Each generator package has a Parse function
// for its text form.
//
// # Binary encoding
//
// [ID.AppendBinary], [ID.MarshalBinary] and [ID.UnmarshalBinary] encode
// an ID as its bytes, with the size given by their length, and the zero
// ID as no bytes. Codecs that use a type's binary methods, such as
// encoding/gob, encode an ID through them.
//
// # Allocation contract
//
// [ID] is a value type of [MaxSize] bytes and a size byte, passed by
// value. [ID.IsZero], [ID.Size], [ID.Bytes], [ID.Compare], [ID.Equal],
// [ID.AppendBinary] into a buffer with room and [ID.UnmarshalBinary] do
// not allocate. [ID.String] and [ID.MarshalBinary] allocate their
// results.
//
// Each [Generator] documents its allocation contract. The generators of
// this module do not allocate per call when the
// [go.thesmos.sh/core/rand.Rand] and the [go.thesmos.sh/core/clock.Clock]
// that they read do not.
package id
