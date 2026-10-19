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
// A [Writer] writes the entries of a table to a [Sink] in the order of
// Compare, and a [Reader] reads the table through a [File]. A table has four
// parts:
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
//     CRC32C. [Reader.Span] searches the index for the data blocks of a key
//     range.
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
// [WriterOptions.MaxSize] returns the most bytes of a table from the
// [Counts] of its entries and its fragments, so a store reserves the space
// of a table before the Writer writes it.
//
// # Partitions
//
// With the PartitionSize of WriterOptions, the Writer cuts the index into
// index partitions of about PartitionSize bytes and the filter into filter
// partitions of at most PartitionSize bytes. A top-level index of each kind
// has an entry for each of its partitions, and the footer states the
// top-level indexes in place of the index block and the filter block. The
// data blocks have the bytes of a table without partitions. A table in
// partitions has format version 2, and a table without them version 1.
//
// A Reader reads every version. Of a table without partitions it keeps the
// index block and the filter block in memory. Of a table in partitions it
// keeps the top-level indexes, so its memory grows with the number of
// partitions and not with the number of keys. A lookup reads the filter
// partition and the index partition of its key as it reads a data block,
// through the cache.
//
// # Range tombstones
//
// A range tombstone deletes the keys from its start up to its end, which the
// tombstone excludes, at a sequence. [Writer.AddRangeDelete] adds the
// fragments of range tombstones in the order that [RangeDeleter] states, so
// the fragments of a table do not overlap. A [Fragmenter] splits tombstones
// that overlap at their overlap points, and passes the fragments to the
// Writer. The Writer keeps the fragments in a block of fragments, and the
// Reader keeps that block in memory. [Reader.RangeDeletes] returns a
// [RangeDeleteIter] over the fragments, whose moves do not read a block. The
// package does not apply a fragment to the entries that it covers. The
// engine compares the sequence of each fragment with the sequences of those
// entries.
//
// # Prefixes
//
// With the Prefix of WriterOptions, the Writer also builds a filter of the
// prefixes of the keys, and records the name of the [PrefixExtractor] in the
// table. The filter of prefixes has the layout and the partitions of the
// filter of the keys. [Reader.MayContainPrefix] asks it, so a scan of the
// keys of a prefix skips a table without such a key. A Reader uses the
// filter of prefixes only when the Prefix of its [ReaderOptions] has the
// name of the extraction of the table.
//
// A table with fragments or with a filter of prefixes has format version 3.
// Its footer states a block of places in place of the filter. That block
// states the places of the filter, of the block of fragments and of the
// filter of prefixes. A table of version 3 has the index and the filter of
// version 1, or of version 2 in partitions.
//
// # Contexts
//
// Every call that can read or write a table takes the context of its
// caller: [Open], [Reader.Get], [Reader.MayContain], [Reader.MayContainPrefix],
// [Reader.Span], each move of a [Source], the methods of a [BlockCache],
// [Writer.Add], [Writer.Close], and the methods of a [RangeDeleter] and of a
// Fragmenter. The package passes the context to the File, the Sink and the
// cache. A File or a Sink that waits ends its wait with the error of the
// context. A move of an [Iter] that needs a block checks the context first,
// so a scan stops at the next block after its context ended, also when the
// cache contains the block. Next, Prev and SeekGE ignore the context when
// they move within the current block. A cursor of [IterOptions] with NoFill
// takes the blocks that the cache contains and does not add the blocks that
// it reads, so a scan of a compaction keeps the blocks of the reads in the
// cache. A cursor with ReadAhead reads the data blocks of ReadAhead bytes
// with one read of the File, so a scan makes one read for many blocks.
//
// # Block caches
//
// A [BlockCache] keeps the decoded data blocks and partitions that the
// readers of its tables share. On a miss, a Reader adds a copy of the block
// or the partition that it read.
// A [LendingCache] keeps its blocks in [Block] buffers. On a miss of such a
// cache, a Reader copies the block into a Block of [NewBlock] and adds that
// Block. The cache and every cursor that reads a Block keep a reference to
// it. The last [Block.Release] returns the buffer to a pool of size classes,
// which lends the buffer for the next block. Package
// [go.thesmos.sh/core/sstable/blockcache] implements a LendingCache over
// [go.thesmos.sh/core/cache.Cache].
//
// A Reader passes a LendingCache the [BlockRole] of each block: [DataBlock],
// [IndexBlock] or [FilterBlock]. The cache can then count the lookups of the
// data blocks apart from the lookups of the partitions. Readers of different
// tables that share a cache need different identities in the CacheID of
// their [ReaderOptions]. A Reader does not check the CRC32C of a block that
// the cache returns, so the Readers of two tables with one identity read the
// blocks of each other.
//
// # Merges
//
// A [Source] is a cursor over a sorted run of entries that moves in both
// directions. An [Iter] of a table and the cursor of a memtable are sources.
// A [Merger] yields the union of any number of sources in the order of
// Compare, from the first entry forward or from the last entry back.
// [Merger.Close] closes every source of the merge that implements io.Closer,
// so an engine releases the cursors of a read with one call.
//
// # Concurrency
//
// A Reader is safe for concurrent use. A Writer, a Fragmenter, an Iter, a
// RangeDeleteIter and a Merger are for one goroutine.
//
// # Errors
//
// The functions return [ErrOptions], [ErrOrder], [ErrClosed], [ErrTooLarge],
// [ErrRange], [ErrPrefix] and [ErrCorrupt], and errors that wrap the errors
// of the Sink and the File. A read or a write that ends with its context
// returns an error that wraps the error of the context.
//
// # Allocation contract
//
//   - Writer.Add and Writer.AddRangeDelete do not allocate once the buffers
//     of the current block, the index, the hashes of the filters, the
//     partitions and the fragments have room. [Writer.Reset] keeps the
//     buffers for the next table, so a table after Reset allocates the keys
//     of its summary once the buffers have grown. A Writer releases its
//     buffers once [Writer.Footprint] exceeds the MaxRetained of
//     [WriterOptions], and the next table grows them again.
//   - [Fragmenter.Add] and [Fragmenter.Flush] do not allocate once the
//     buffers of the tombstones that overlap have room.
//   - Reader.Get, Reader.MayContain, Reader.MayContainPrefix, Reader.Span,
//     Reader.NewIter, Reader.RangeDeletes, Iter.Close and the moves of an
//     Iter, a RangeDeleteIter and a Merger do not allocate once the pooled
//     cursors have grown to the largest block and the largest partition. A
//     read through a [BlockCache] allocates a copy of each block or
//     partition that the cache does not contain, apart from the reads of a
//     cursor with NoFill. A read through a LendingCache copies such a block
//     into a Block from the pool, which allocates only when the pool of its
//     class is empty.
//   - [NewWriter], [NewFragmenter], Writer.Close and [Open] allocate the
//     Writer, the Fragmenter or the Reader and their buffers.
//     [Reader.Footprint] returns the bytes of the buffers that a Reader
//     keeps.
//
// # Dependency position
//
// Imports bytes, cmp, context, encoding/binary, errors, fmt, hash/crc32, io,
// math, math/bits, slices, strconv and sync/atomic from the standard
// library, and go.thesmos.sh/core/compress/snappy, go.thesmos.sh/core/errs,
// go.thesmos.sh/core/filter, go.thesmos.sh/core/hash/xxh3 and
// go.thesmos.sh/core/pool from this module.
package sstable
