// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package radix implements an adaptive radix tree over byte-string keys,
// for queries over the prefixes of a key.
//
// [Map.LongestPrefix] returns the longest stored prefix of a key, and
// [Map.Prefixes] yields every stored prefix of a key. Each takes one descent
// of a [Map]. A B-tree needs one search for each candidate length of the
// prefix, and a hash map needs one lookup for each. A route table keyed by
// address prefixes, a table of access rules keyed by path prefixes and a
// zone table keyed by reversed domain labels each look up the longest
// stored prefix of a key. [Map.Scan] yields the keys that start with a
// prefix in the order of their bytes.
//
// go.thesmos.sh/core/btree is faster for ordered maps and range scans.
// go.thesmos.sh/core/idmap and Go's map are faster for point lookups.
//
// # Memory
//
// An inner node has room for 4, 16, 48 or 256 children and skips the key
// bytes that every key below it shares. The records of the nodes and the
// leaves are in the chunks of [arena.List] values, and uint32 handles link
// them. The garbage collector marks a map of any size without scanning its
// records, because the records contain no pointer. A leaf stores a key of up
// to 12, 28 or 60 bytes in place, as the InlineKeyLen of [Config] sets. A
// longer key keeps its bytes in a region of an [arena.RefSlabs].
//
// # Concurrency
//
// A Map is not safe for concurrent use. Any number of goroutines may read a
// Map that no goroutine writes.
//
// # Errors
//
// [Map.Set] returns [ErrFull] and [ErrTooLong].
//
// # Allocation contract
//
//   - [New] allocates the map.
//   - Map.Get, Map.LongestPrefix, [Map.Len] and a range loop over
//     Map.Prefixes, Map.Scan or [Map.All] do not allocate.
//   - [Map.Set], [Map.Delete] and [Map.Reset] allocate only to grow the
//     lists of records, the stacks of free records, and the slabs and free
//     lists for long keys.
//
// # Dependency position
//
// Imports cmp, encoding/binary, errors, iter, math/bits and slices from the
// standard library, and go.thesmos.sh/core/arena and go.thesmos.sh/core/errs
// from this module.
package radix
