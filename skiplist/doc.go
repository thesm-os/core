// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package skiplist implements the memtable of an LSM tree: an insert-only
// ordered map of entries that many goroutines write and read at once,
// without locks.
//
// An entry is a key, a 64-bit sequence and a value. A [List] orders its
// entries by key as bytes.Compare orders keys, and for one key from the
// highest sequence to the lowest. A read at the snapshot s calls [List.Get]
// or [Iter.SeekGE] with s, and the first entry that it finds with the key is
// the newest version at or below s. The sequence is opaque to the package.
// An engine packs its sequence number and the kind of an entry into it.
//
// # Memory
//
// A List keeps its nodes in an [arena.Bump] of uint32 words and its keys and
// values in an arena.Bump of bytes. Both arenas are free of pointers, so the
// garbage collector does not scan a List of any size. A node takes 8 words
// and one link per level, 38.3 bytes on average. Two of its words contain
// the first 8 bytes of its key, so a search reads the key from the data
// arena only when those bytes equal the first 8 bytes of the key that it
// searches for. One word links the node back to a node before it at level
// 0. [Config] sets the capacity of each arena. The capacities are fixed, and
// [List.Add] returns [ErrFull] for an entry that does not fit. An engine
// then freezes the full List and adds the entry to a new one.
//
// [Config.Data] and [Config.Nodes] pass memory of the caller to the arenas,
// and [NodeWords] returns the number of words that the node arena needs. An
// engine that moves the memory of a memtable between Lists and the arenas of
// [go.thesmos.sh/core/arena] then allocates the memory once. [List.Reset]
// zeroes the memory that the List used, so the next user of the memory finds
// it zero.
//
// # Heights
//
// A node has 1 level, plus 1 for each level k from 1 to 19 that a hash of
// its entry passes with a probability of e^-k. The hash takes the seed of
// the List, so a caller that does not know the seed cannot choose keys of
// one height.
//
// # Inserters
//
// [List.Add] searches every level from the head, in O(log n) steps for n
// entries. An [Inserter] keeps the nodes before its last entry, and its Add
// of a later entry searches each level from them. A writer that adds its
// entries in key order, as the replay of a sorted batch does, then compares
// each entry with a few nodes.
//
// # Concurrency
//
// Add, Get, [List.Len], [List.Size], [List.Cost] and [List.NewIter] are safe
// for concurrent use. An Inserter and an [Iter] are for one goroutine each.
// Their methods are safe while other goroutines add. [List.Reset] is not
// safe concurrently with any other method apart from Cost. An Add that
// returned is visible to every Get and to every move of a cursor that starts
// after it.
//
// # Cursors
//
// An Iter moves in both directions. A move forward follows a link of level
// 0, and [Iter.Prev] follows the link back of the current node. A writer
// moves the link back of the node after its entry to the new node once it
// links the node. Until then the link back is an earlier node, and Prev
// walks forward from it to the node before the current one. [Iter.SeekGE]
// of a cursor on an entry before its target first compares the target with
// the next entry. [Iter.Last], [Iter.SeekLT] and the other seeks search from
// the head, in O(log n) steps, about the time of a Get. Each move takes a
// context and ignores it, because a memtable does not wait. With these moves
// an Iter implements [go.thesmos.sh/core/sstable.Source].
//
// # Costs
//
// [List.Cost] returns the bytes that an Add of an entry reserves in the data
// arena and in the node arena, in the order of the fields of Config. An
// engine that reserves the cost of each entry before its Add knows that the
// Add fits, because the height of a node derives from the entry and the
// seed. [MaxCost] returns the cost of an entry in a node of the largest
// height. That cost bounds Cost in a List of any seed, so engines whose
// lists have other seeds compute the same bound for a batch.
//
// # Errors
//
// The functions return [ErrFull], [ErrExists] and [ErrSize].
//
// # Allocation contract
//
// [New] allocates the List, its two arenas and the array of each arena whose
// memory the Config does not provide. NewIter allocates the cursor. Add, Get,
// Len, Size, Cost, MaxCost, Reset, the Add of an Inserter and the methods of
// an Iter do not allocate.
//
// # Dependency position
//
// Imports bytes, cmp, context, encoding/binary, errors, math, math/bits and
// sync/atomic from the standard library, and go.thesmos.sh/core/arena,
// go.thesmos.sh/core/errs and go.thesmos.sh/core/hash/xxh3 from this module.
package skiplist
