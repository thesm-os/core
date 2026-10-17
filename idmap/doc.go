// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package idmap implements hash maps keyed by the fixed-size identifiers of
// package [go.thesmos.sh/core/id], by array types of the same sizes, and by
// the identifier types that a caller declares over them.
//
// A Go map hashes every byte of a key with the general hash of the runtime.
// The maps of this package hash the 64-bit words of an identifier with one
// seeded multiplication. [Map] looks up identifiers faster than a Go map
// does, and fills a presized map without an allocation per key.
// [Concurrent] stores integers for many goroutines without a lock for its
// readers and without an allocation per store.
//
// # Types
//
//   - [Key] is the type set of the fixed-size identifiers: [id.ID128],
//     [id.ID160] and [id.ID256].
//   - [Map] is a hash map for one goroutine, from keys of 16, 20 or 32 bytes
//     to values of any type. It grows by doubling. [Map.Clone] copies its
//     arrays, so a copy takes no rehash.
//   - [Typed] is a hash map for one goroutine whose keys are of an
//     identifier type over [id.ID]. It keeps the keys of each size in a Map
//     of that size.
//   - [Word] is the type set of the values of a [Concurrent]: integers of
//     32 and 64 bits.
//   - [Concurrent] is a hash map of a fixed capacity for many goroutines.
//
// # Identifier types
//
// A caller declares an identifier type as a struct whose one field embeds
// an identifier of package id or a value type of an identifier package, such
// as [ulid.ULID]. The compiler keeps two such types apart:
//
//	type ShardID struct{ id.ID128 }
//	type StreamID struct{ id.ID }
//	type OrderID struct{ ulid.ULID }
//
// Each constructor of a Map takes one kind of key:
//
//   - [New] takes the identifiers of Key.
//   - [New128], [New160] and [New256] take an array type of 16, 20 or 32
//     bytes, such as [ulid.ULID] or [ksuid.KSUID].
//   - [NewTyped128], [NewTyped160] and [NewTyped256] take an identifier type
//     over ID128, ID160 or ID256.
//   - [NewTypedULID], [NewTypedKSUID], [NewTypedUUIDv4] and [NewTypedUUIDv7]
//     take an identifier type over a [ulid.ULID], a [ksuid.KSUID], a
//     [uuidv4.UUID] or a [uuidv7.UUID].
//
// [NewTyped] returns a Typed of an identifier type over [id.ID]. Each map
// hashes a key as it hashes the identifier of the same bytes. Its All yields
// keys of its key type. A Concurrent takes the identifiers of Key alone.
//
// # Hashing
//
// Each map takes two secret words from hash/maphash when it is created, as
// a Go map takes a seed from the runtime. The hash combines the first two
// words of a key with the secret words, multiplies them into 128 bits, and
// combines the two halves by exclusive or. The bytes of an ID160 or an
// ID256 past the first 16 enter a second multiplication of the same form.
// A caller who does not know the secret words cannot choose keys of one
// hash, so keys from untrusted input do not make a lookup walk a long run.
//
// # Concurrency
//
// A [Map] and a [Typed] are not safe for concurrent use. A [Concurrent] is,
// and its Load, Store and Delete are linearizable.
//
// # Allocation contract
//
//   - [New] and [Map.Clone] allocate the map and its three arrays. The other
//     constructors of a Map also allocate the hash function of the key type.
//     [Map.Set] allocates three arrays when it grows the map, and nothing
//     otherwise. The other methods of a Map do not allocate, and neither does
//     a range loop over [Map.All] that the compiler inlines the iterator into.
//   - [NewTyped] allocates the map. [Typed.Set] allocates a Map and its three
//     arrays for the first key of each size, and three arrays when it grows a
//     Map. [Typed.Clone] allocates the copy and a copy of each of its Maps.
//     The other methods of a Typed do not allocate.
//   - [NewConcurrent] allocates the map and its buckets, and for an ID160
//     or an ID256 key the array of the key words past the first 16 bytes.
//     The methods of a Concurrent do not allocate.
//
// # Errors
//
// [Concurrent.Store] returns [ErrFull], classified
// [go.thesmos.sh/core/errs.Transient], for a new key when the map has its
// capacity of keys.
//
// # Dependency position
//
// Imports encoding/binary, errors, hash/maphash, iter, math/bits, runtime,
// slices and sync/atomic from the standard library, and
// go.thesmos.sh/core/errs, go.thesmos.sh/core/id,
// go.thesmos.sh/core/id/ksuid, go.thesmos.sh/core/id/ulid,
// go.thesmos.sh/core/id/uuidv4 and go.thesmos.sh/core/id/uuidv7 from this
// module.
package idmap
