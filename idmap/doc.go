// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package idmap implements hash maps keyed by the fixed-size identifiers of
// package [go.thesmos.sh/core/id].
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
//   - [Key] is the type set of the keys: [id.ID128], [id.ID160] and
//     [id.ID256].
//   - [Map] is a hash map for one goroutine, from identifiers to values of
//     any type. It grows by doubling. [Map.Clone] copies its arrays, so a
//     copy takes no rehash.
//   - [Word] is the type set of the values of a [Concurrent]: integers of
//     32 and 64 bits.
//   - [Concurrent] is a hash map of a fixed capacity for many goroutines.
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
// A [Map] is not safe for concurrent use. A [Concurrent] is, and its Load,
// Store and Delete are linearizable.
//
// # Allocation contract
//
//   - [New] and [Map.Clone] allocate the map and its three arrays.
//     [Map.Set] allocates three arrays when it grows the map, and nothing
//     otherwise. The other methods of a Map do not allocate, and neither
//     does a range loop over [Map.All] that the compiler inlines the
//     iterator into.
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
// go.thesmos.sh/core/errs and go.thesmos.sh/core/id from this module.
package idmap
