// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package uuidv4 provides an [id.Generator] of the version-4 UUIDs of
// RFC 9562: 122 random bits, 4 version bits and 2 variant bits.
//
// # When to use
//
// Use a UUIDv4 when an identifier must not reveal when it was created.
// Among one billion UUIDv4s, the probability of a collision is about
// 1 in 10^19.
//
// For identifiers whose byte order matches their order of creation, which
// keeps inserts into an index local and lets a scan return the newest
// first, use [go.thesmos.sh/core/id/uuidv7] or
// [go.thesmos.sh/core/id/ulid].
//
// # Construction
//
// [Generator] reads entropy from a [rand.Rand]. Production callers pass
// [go.thesmos.sh/core/rand/crypto.Rand]. Tests pass
// [go.thesmos.sh/core/rand/seeded.Rand] or
// [go.thesmos.sh/core/rand/constant.Rand] for a deterministic stream of
// identifiers.
//
// # Encoding
//
// [Format] returns the text form of RFC 9562,
// "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx", where y is 8, 9, a or b, and
// [Parse] reads it back. The bytes of the [id.ID] are the 16 bytes of the
// UUID, so a caller that stores the bytes does not convert them.
package uuidv4
