// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package btree implements ordered maps and sets as in-memory B+ trees.
//
// A Go map returns its keys in no order. A caller that needs its keys in
// order, a key range, the nearest key or the i-th key sorts on every read,
// or keeps a sorted slice whose insert costs O(n). A [Map] keeps its keys
// in order at O(log n) for each insert, delete and lookup.
//
// # Types
//
//   - [Map] orders its keys by [cmp.Compare].
//   - [MapFunc] orders its keys by a function that [NewMapFunc] receives.
//     Every comparison calls the function.
//   - [Set] is a set of keys ordered by [cmp.Compare].
//
// # Order
//
// [cmp.Compare] orders floating-point keys as follows:
//
//   - A NaN sorts before every other value.
//   - All NaNs are one key.
//   - -0.0 and 0.0 are one key.
//
// A search compares keys with [cmp.Less], which compiles to one comparison
// instruction for an integer key and to one string comparison for a
// string key.
//
// # Layout
//
//   - Only leaves contain values. A leaf contains up to 63 items, with its
//     keys in one array and its values in another, so a search reads keys
//     only.
//   - An internal node contains up to 63 separator keys and 64 children,
//     and counts the items under it, so [Map.At] and [Map.Rank] run in
//     O(log n).
//   - With no pointer field, a leaf of keys and values without pointers is
//     an object that the garbage collector marks without scanning.
//   - A node other than the root and the nodes at the right edge of the
//     tree contains at least 31 items or separators. A split leaves 32
//     items in each half. An insert at the right edge of the tree keeps the
//     node it splits full, so keys inserted in ascending order fill every
//     leaf.
//   - A merge puts the freed node on a free list of the map, one for each
//     kind of node, which keeps up to 64 nodes of that kind. A split takes
//     a node from the free list before it allocates. [Map.Reset] puts
//     every node of the map on the free lists.
//
// # Stored keys
//
//   - A [Map.Set] of a new key stores the key that it receives. A Set of a
//     key that the map contains keeps the stored key.
//   - Each separator of an internal node is the first key of the subtree
//     after it. A removal of that key gives the separator the new first
//     key of the subtree, or removes the separator with the subtree when
//     the subtree is empty.
//   - A map refers to no key that it removed. A caller may reuse the memory
//     of a key, such as the bytes of a []byte key of a [MapFunc], once no
//     map contains the key.
//
// # Writes during an iteration
//
// A write to a map during an iteration over it does not end the
// iteration. The map counts its writes. After each yield, the iterator
// compares the count with the value it read before the yield. When the
// count changed, the iterator descends from the root to the first key
// after the last key it yielded, or to the last key before it in a
// descending iteration, and continues from there. Such an iteration yields
// each key at most once and in order. It yields a key that a write inserts
// ahead of its position, and does not yield a key that a write deletes
// ahead of its position.
//
// # Clones
//
// [Map.Clone] returns a copy of a map in O(1). Each node records the ID of
// the map that may change it in place, and a clone gives both maps new
// IDs. A write copies every node on its path that has another map's ID,
// and copies only the occupied slots.
//
// # Concurrency
//
// A map is not safe for concurrent use. Any number of goroutines may read
// a map that no goroutine writes. A clone is a separate map, so one
// goroutine may read a clone while another writes the original.
//
// # Allocation contract
//
//   - Lookups, [Map.Len], [Map.At] and [Map.Rank] do not allocate.
//   - A range loop over an iterator does not allocate when the compiler
//     inlines the iterator into the loop.
//   - An insert allocates 1 object for each leaf split and 2 for each
//     split of an internal node that finds the free list empty. A map whose
//     size does not change reuses the nodes that its deletes free, and
//     does not allocate.
//   - A fill after [Map.Reset] allocates only the nodes that it needs
//     beyond those that the map kept.
//   - [Map.Clone] allocates the new map. After a clone, a write allocates
//     1 object for each leaf and 2 for each internal node that it copies.
package btree
