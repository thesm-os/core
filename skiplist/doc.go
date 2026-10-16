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
// garbage collector does not scan a List of any size. A node takes 5 words
// and one link per level, 26.3 bytes on average. The arenas have a fixed
// capacity, and [List.Add] returns [ErrFull] for an entry that does not
// fit. An engine then freezes the full List and adds the entry to a new
// one.
//
// # Heights
//
// A node has 1 level, plus 1 for each level k from 1 to 19 that a hash of
// its entry passes with a probability of e^-k. The hash takes the seed of
// the List, so a caller that does not know the seed cannot choose keys of
// one height.
//
// # Concurrency
//
// Add, Get, [List.Len], [List.Size] and [List.NewIter] are safe for
// concurrent use. An [Iter] is for one goroutine, and its moves are safe
// while other goroutines add. [List.Reset] is not safe concurrently with any
// other method. An Add that returned is visible to every Get and to every
// move of a cursor that starts after it.
//
// # Errors
//
// The functions return [ErrFull], [ErrExists] and [ErrSize].
//
// # Allocation contract
//
// [New] allocates the List, its two arenas and their arrays. NewIter
// allocates the cursor. Add, Get, Len, Size, Reset and the methods of an
// Iter do not allocate.
//
// # Dependency position
//
// Imports bytes, cmp, errors, math, math/bits and sync/atomic from the
// standard library, and go.thesmos.sh/core/arena, go.thesmos.sh/core/errs
// and go.thesmos.sh/core/hash/xxh3 from this module.
package skiplist
