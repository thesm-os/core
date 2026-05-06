// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package uuidv4 provides an [id.Generator] producing RFC 4122
// version-4 UUIDs (122 random bits + 4 version bits + 2 variant
// bits).
//
// # When to use
//
// Use UUIDv4 when the identifier should NOT leak temporal
// information. The 122 random bits give a vanishingly small
// collision probability (≈ 5 × 10⁻¹⁰ for one billion identifiers).
//
// For time-sortable identifiers — where bytewise lexicographic
// order matches generation order, useful for index locality and
// "newest first" queries without an explicit timestamp column —
// use [id/ulid] instead.
//
// # Construction
//
// [Generator] depends on a [rand.Rand]. Production callers wire
// [rand/crypto.Rand] (CSPRNG-grade); tests wire [rand/seeded.Rand]
// or [rand/fixed.Rand] for deterministic identifier streams.
//
// # Encoding
//
// [Format] returns the canonical RFC 4122 hyphenated hex form
// ("xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx" where y ∈ {8, 9, a, b}).
// The raw [id.ID] bytes are the underlying 16-byte UUID; consumers
// that store the bytes directly need no conversion.
package uuidv4
