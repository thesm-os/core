// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package sstable writes and reads the sorted tables of an LSM tree, and
// merges sorted runs of entries into one order.
//
// An entry is a key, a 64-bit sequence and a value. [Compare] orders entries
// by key as bytes.Compare orders keys, and for one key from the highest
// sequence to the lowest. A read at the snapshot s calls [Reader.Get] or
// [Iter.SeekGE] with s, and the first entry that it finds with the key is
// the newest version at or below s. The sequence is opaque to the package.
// An engine packs its sequence number and the kind of an entry into it.
//
// # Tables
//
// A [Writer] writes the entries of a table to an io.Writer in the order of
// Compare, and a [Reader] reads the table through an io.ReaderAt. A table has
// four parts:
//
//   - Data blocks, whose size the BlockSize of [WriterOptions] sets. At a
//     restart point of a block the Writer stores the whole key. For every
//     other entry it stores only the bytes of the key after the prefix that
//     the key shares with the key before it.
//   - A filter block with the encoding of a [filter.Fuse] over the
//     [xxh3.Hash] of each distinct key. A lookup of a key that the table does
//     not contain reads a data block only when the filter reports a false
//     positive.
//   - An index block with an entry for each data block. The entry has the
//     last key of the block, the place of the block in the table and its
//     CRC32C.
//   - A footer of 80 bytes with the places and the CRC32C checksums of the
//     index and the filter, the number of entries, the lowest and the highest
//     sequence, a version and a magic number.
//
// A trailer of 5 bytes follows every block. It contains the codec of the
// block and the CRC32C of the block and its codec. With the CRC32C of the
// footer, a change of any bit of a table fails a checksum. A read compares
// the CRC32C of each block with the CRC32C that the index or the footer
// states for it, so a valid block at the place of another block returns
// [ErrCorrupt]. [Writer.Close] documents the layout in full, and the layout
// does not change between releases.
//
// # Merges
//
// A [Source] is a cursor over a sorted run of entries that moves in both
// directions. An [Iter] of a table and the cursor of a memtable are sources.
// A [Merger] yields the union of any number of sources in the order of
// Compare, from the first entry forward or from the last entry back.
//
// # Concurrency
//
// A Reader is safe for concurrent use. A Writer, an Iter and a Merger are for
// one goroutine.
//
// # Errors
//
// The functions return [ErrOptions], [ErrOrder], [ErrClosed], [ErrTooLarge]
// and [ErrCorrupt], and errors that wrap the errors of the io.Writer and the
// io.ReaderAt.
//
// # Allocation contract
//
//   - Writer.Add does not allocate once the buffers of the current block,
//     the index and the hashes of the filter have room.
//   - Reader.Get, Reader.MayContain, Reader.NewIter, Iter.Close and the moves
//     of an Iter and a Merger do not allocate once the pooled cursors have
//     grown to the largest block. A read through a [BlockCache] allocates a
//     copy of each block that the cache does not contain.
//   - [NewWriter], Writer.Close and [Open] allocate the Writer or the Reader
//     and their buffers.
//
// # Dependency position
//
// Imports bytes, cmp, encoding/binary, errors, fmt, hash/crc32, io, math,
// slices and strconv from the standard library, and
// go.thesmos.sh/core/compress/snappy, go.thesmos.sh/core/errs,
// go.thesmos.sh/core/filter, go.thesmos.sh/core/hash/xxh3 and
// go.thesmos.sh/core/pool from this module.
package sstable
