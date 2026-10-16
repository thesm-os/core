// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package xxh3 computes XXH3-64, the 64-bit hash of xxHash, with seed 0 and
// the default secret.
//
// A filter or an index that a program persists needs a hash whose values do
// not change between processes. A seed of [hash/maphash] exists only in the
// process that created it. [Hash] and [HashString] return the values of
// xxHash 0.8 on every platform. The package reads its input with
// [encoding/binary] and multiplies with [math/bits], without assembly and
// without package unsafe.
//
// # Collisions
//
// XXH3 is not a cryptographic hash. Whoever chooses the keys can make two of
// them collide. Keys from untrusted input go into a table that hashes them
// with hash/maphash, whose seed the caller does not know.
//
// # Concurrency
//
// Hash and HashString are safe for concurrent use.
//
// # Allocation contract
//
// Hash and HashString do not allocate.
//
// # Dependency position
//
// Imports encoding/binary and math/bits from the standard library.
package xxh3
