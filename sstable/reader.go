// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"slices"
	"strconv"

	"go.thesmos.sh/core/compress/snappy"
	"go.thesmos.sh/core/filter"
	"go.thesmos.sh/core/hash/xxh3"
	"go.thesmos.sh/core/pool"
)

// minTableSize is the size of the smallest table, a table of no entries. Its
// index block contains only the count of its restart points in 4 bytes. The
// trailer of the index block and the footer of 80 bytes follow it.
const minTableSize = 4 + trailerLen + footerLen

// checkers are the states of [Reader.Check] that a Check returned, with
// their buffers.
var checkers = pool.NewPool(func() *checker { return new(checker) })

// File is the storage of a table that a [Reader] reads. ReadAt has the
// contract of io.ReaderAt. A ReadAt that waits, such as for a permit of a
// table of open files or for a budget of disk I/O, ends its wait with the
// error of ctx. An *os.File or a bytes.Reader becomes a File through a type
// whose ReadAt drops ctx.
//
// # Concurrency
//
// The methods of a File must be safe for concurrent use, because the cursors
// of a Reader read at once.
type File interface {
	// ReadAt reads len(p) bytes at off into p, as io.ReaderAt reads them.
	ReadAt(ctx context.Context, p []byte, off int64) (int, error)
}

// ReaderOptions configure a [Reader].
type ReaderOptions struct {
	// Cache keeps the decoded data blocks and partitions of the Reader.
	// Without a Cache the Reader reads every block from the File.
	Cache BlockCache

	// Prefix is the extraction of the prefixes that [Reader.MayContainPrefix]
	// receives. A Reader uses the filter of prefixes of a table only when the
	// Name of Prefix equals the name of the extraction that the Writer
	// recorded in the table. With nil or with another name, [Open] does not
	// read the filter of prefixes, and MayContainPrefix reports true.
	Prefix PrefixExtractor

	// CacheID is the identity of the table in Cache. The readers of
	// different tables that share a Cache need different identities. A
	// Reader checks the CRC32C of each block that it reads from the File,
	// and not of a block that Cache returns. The Readers of two tables with
	// one identity therefore read the blocks of each other as valid data.
	// [go.thesmos.sh/core/sstable/blockcache.Cache.NewID] issues identities
	// that never repeat within one cache. It never issues 0, so none of its
	// identities equals the CacheID of a Reader that sets none.
	CacheID uint64

	// SuffixBytes is the width in bytes of the version at the end of each
	// key of the table, as the SuffixBytes of [WriterOptions] states. [Open]
	// returns [ErrSuffix] for a table whose Writer had another width. The
	// Reader then orders the keys of its seeks as [CompareWithSuffix] of the
	// width orders them.
	SuffixBytes int
}

// BlockCache keeps the decoded data blocks and partitions of the readers
// that share it. Its key is the identity of the table and the offset of the
// block in the table. Each call takes the context of the read that looks
// the block up, so a cache counts and traces its lookups under that context.
//
// A Reader never writes a block that it adds to the cache or that Get
// returns, and the cache must not change a block either. The capacity and
// the eviction of the cache are the policy of its owner. A cache that
// implements [LendingCache] lends a [Block] in place of a byte slice, and
// receives the [BlockRole] of each block.
//
// # Concurrency
//
// The methods of a BlockCache must be safe for concurrent use.
type BlockCache interface {
	// Get returns the block of the table id at offset, and reports whether
	// the cache contains it.
	Get(ctx context.Context, id, offset uint64) ([]byte, bool)

	// Add adds the block of the table id at offset. A Reader does not write
	// block again, so the cache may keep it.
	Add(ctx context.Context, id, offset uint64, block []byte)
}

// BlockRole is the role of a block in its table. A [Reader] passes the role
// of each block that it lends from a [LendingCache] or adds to it, so the
// cache can count its hits and its misses for each role. The zero BlockRole
// is not valid.
type BlockRole uint8

const (
	// DataBlock is the role of a data block.
	DataBlock BlockRole = 1 // data
	// IndexBlock is the role of an index partition of a table in partitions.
	// A Reader keeps the index block of a table without partitions in memory
	// and never looks it up in a cache.
	IndexBlock BlockRole = 2 // index
	// FilterBlock is the role of a partition of the filter of the keys or of
	// the filter of the prefixes of a table in partitions. A Reader keeps the
	// filter blocks of a table without partitions in memory and never looks
	// them up in a cache.
	FilterBlock BlockRole = 3 // filter
	// FragmentBlock is the role of a fragment partition of a table in
	// partitions with range tombstones. A Reader keeps the block of fragments
	// of a table without partitions in memory and never looks it up in a
	// cache.
	FragmentBlock BlockRole = 4 // fragments
)

// Valid reports whether r is [DataBlock], [IndexBlock], [FilterBlock] or
// [FragmentBlock].
func (r BlockRole) Valid() bool {
	return DataBlock <= r && r <= FragmentBlock
}

// String returns "data", "index", "filter" or "fragments", and
// "BlockRole(N)" for a value that is not valid.
//
// # Allocation contract
//
// Zero-alloc for a valid BlockRole. Another value allocates its text.
func (r BlockRole) String() string {
	switch r {
	case DataBlock:
		return "data"
	case IndexBlock:
		return "index"
	case FilterBlock:
		return "filter"
	case FragmentBlock:
		return "fragments"
	default:
		return "BlockRole(" + strconv.Itoa(int(r)) + ")"
	}
}

// LendingCache is a [BlockCache] that keeps each block in a [Block] and
// lends it to the readers. A Reader whose cache implements LendingCache
// calls Lend in place of Get, and AddBlock in place of Add. It copies a block
// that misses into a Block of [NewBlock] and adds that Block. A cursor keeps
// a reference to the Block of its current entry, and releases it when it
// moves to another block or closes. The last Release returns the buffer to
// its pool, so a cache that releases the blocks that leave it allocates no
// block for a miss.
//
// Lend and AddBlock receive the [BlockRole] of the block, so a cache can
// count the lookups of the data blocks apart from the lookups of the
// partitions. The block at one offset of a table always has the same role,
// so a cache keeps a block by the identity of the table and the offset
// alone. A Reader of a table without partitions passes only [DataBlock].
//
// # Concurrency
//
// The methods of a LendingCache must be safe for concurrent use.
type LendingCache interface {
	BlockCache

	// Lend returns the block of the table id at offset with a reference
	// that the caller releases, and reports whether the cache contains it.
	// role is the role of the block in the table. The bytes of the block do
	// not change until that Release.
	Lend(ctx context.Context, id, offset uint64, role BlockRole) (*Block, bool)

	// AddBlock adds b as the block of the table id at offset. role is the
	// role of the block in the table. The cache takes a reference of its
	// own and releases it once the block leaves the cache. The caller keeps
	// its own reference.
	AddBlock(ctx context.Context, id, offset uint64, role BlockRole, b *Block)
}

// Span is the part of a table that the entries of a key range can occupy.
// [Reader.Span] returns it.
type Span struct {
	// Blocks is the number of the data blocks that can contain an entry of
	// the range.
	Blocks int

	// Bytes is the stored size of those blocks and their trailers in the
	// table.
	Bytes int64
}

// BlockDamage is a block of a table that fails its checksum, its
// decompression or its layout, as [Reader.Check] reports it. After and Last
// bound the keys of the block by the keys that the index of the table states
// for it. Their meaning follows the role of the block:
//
//   - A data block contains entries whose keys are at or after After and at
//     or before Last. After is the key of the index entry of the block before
//     it, and Last the key of the index entry of the block. The bounds include
//     After itself, because two versions of one key can be in two blocks.
//   - An index partition places data blocks whose keys are at or after After
//     and at or before Last. After and Last are the keys of the top-level
//     entries of the partition before it and of the partition.
//   - A filter partition has the hashes of the keys or the prefixes after
//     After and at or before Last. After is the last key or prefix of the
//     partition before it, and Last the last key or prefix of the partition.
//   - A fragment partition contains fragments that start at or after After
//     and end at or before Last: the ends of the last fragments of the
//     partition before it and of the partition.
//
// After is nil for the first block of its kind. The keys of a filter
// partition and of a fragment partition of a table with versions have no
// versions.
type BlockDamage struct {
	// Err is an error that wraps [ErrCorrupt].
	Err error

	// After and Last bound the keys of the block.
	After, Last []byte

	// Offset is the place of the block in the table, and Size the size of
	// the block and its trailer.
	Offset, Size int64

	// Role is the role of the block in the table.
	Role BlockRole
}

// tableFilter is a filter of a table: of its keys, or of the prefixes of its
// keys. Of a table without partitions, the Reader keeps the filter block in
// fuse. Of a table in partitions, it keeps the top-level index of the filter
// partitions in index.
type tableFilter struct {
	// index is the top-level index of the filter of a table in partitions.
	index []byte

	// fuse is the filter block of a table without partitions.
	fuse filter.Fuse

	// size is the capacity of the block that fuse reads, or of index.
	size int

	// ok reports whether the Reader has the filter.
	ok bool
}

// Reader reads a table through a [File]. [Reader.Get] looks up one key, and
// [Reader.NewIter] returns a cursor over the entries. [Reader.Span] returns
// the data blocks of a key range from the index, and [Reader.RangeDeletes]
// returns a cursor over the fragments of the range tombstones.
// [Reader.Check] reads every block that a read can load, and reports each
// block that fails its checks. The Reader keeps the summary of a table in
// memory. Of a table without partitions it keeps the index block, the filter
// blocks and the block of fragments. Of a table in partitions it keeps the
// top-level indexes, and reads the index partitions, the filter partitions
// and the fragment partitions as it reads data blocks. A table in partitions
// of format version 3 without fragment partitions has a block of fragments,
// which the Reader keeps too. The Reader reads a block for each lookup or
// move that needs it, under the context of that call. [Reader.Footprint]
// returns the bytes of the buffers that it keeps.
//
// # Concurrency
//
// A Reader is safe for concurrent use.
//
// # Allocation contract
//
// [Open] allocates the Reader, the blocks and the top-level indexes that the
// Reader keeps, and the keys of the summary. Get, [Reader.MayContain],
// [Reader.MayContainPrefix], [Reader.Span], NewIter and RangeDeletes do not
// allocate once the pools of cursors have a cursor whose buffers have grown
// to the largest block and the largest partition. A read through a
// [BlockCache] allocates the copy of each block or partition that it adds. A
// read through a [LendingCache] takes a [Block] from a pool in place of that
// copy.
type Reader struct {
	// f contains the table.
	f File

	// cache keeps the decoded blocks and partitions under cacheID, or is
	// nil.
	cache BlockCache

	// lending is cache when cache is a LendingCache, and nil otherwise.
	lending LendingCache

	// index is the index block of a table without partitions, or the
	// top-level index of the index of a table in partitions.
	index []byte

	// frags is the block of fragments of a table with fragments without
	// fragment partitions, and nil otherwise.
	frags []byte

	// fragTop is the top-level index of the fragment partitions of a table
	// with fragment partitions, and nil otherwise.
	fragTop []byte

	// keys is the filter of the keys. prefixes is the filter of the prefixes
	// when the Prefix of the options has the name of the extraction of the
	// table.
	keys, prefixes tableFilter

	// summary describes the table.
	summary Summary

	// cacheID is the identity of the table in cache.
	cacheID uint64

	// dataEnd is the offset after the trailer of the last data block.
	dataEnd uint64

	// partEnd is the offset after the trailer of the last partition of a
	// table in partitions, where the parts after the partitions start.
	partEnd uint64

	// suffix is the width of the versions at the end of the keys of the
	// table.
	suffix int

	// partitioned reports whether the index and the filters of the table are
	// cut into partitions.
	partitioned bool
}

// Open reads the footer of the table of size bytes in file under ctx, and the
// parts of the table that the Reader keeps. Of a table without partitions it
// reads the index block and the filter block, and of a table in partitions
// their top-level indexes. Of a table of format version 3 it first reads the
// block of places. It then also reads the filter of prefixes, and the block
// of fragments or the top-level index of the fragment partitions. It reads
// the filter of prefixes only when the Prefix of opts has the name of the
// extraction of the table. Open checks the checksum and the layout of each
// part, and compares the CRC32C of each with the CRC32C that the footer or
// the block of places states for it. It reads the first entry of the first
// data block for the smallest key of the summary, and the last entry of the
// index for the largest key. Of a table with fragment partitions it reads
// the first partition for the start of the first fragment. It does not add
// a block or a partition that it reads to the cache.
//
// Error modes:
//
//   - A table shorter than 89 bytes returns [ErrCorrupt].
//   - A footer with another magic number, a failed checksum or a version
//     other than 1, 2 and 3 returns ErrCorrupt.
//   - A part that does not end at the trailer before the part after it, or
//     before the footer, returns ErrCorrupt. So does a place of a missing
//     part with an offset or a CRC32C.
//   - A block of places with a flag other than bits 0, 1 and 2, or with a
//     field that does not decode, returns ErrCorrupt. So does a block of
//     places with bit 1 of fragment partitions without bit 0 or without a
//     place of the fragments, a width of versions of 0, or a byte after its
//     last field.
//   - A table whose width of versions differs from the SuffixBytes of opts
//     returns [ErrSuffix].
//   - An index block, a filter block, a top-level index, a block of
//     fragments, a first partition or a first data block that fails its
//     checksum or its layout returns ErrCorrupt. So does a block whose
//     CRC32C differs from the CRC32C that the footer, the block of places or
//     its index entry states, and a top-level index of the fragments without
//     entries.
//   - An error of file, such as the error of ctx at the end of a wait,
//     returns an error that wraps it. A read of fewer bytes without an error
//     returns an error that wraps io.ErrUnexpectedEOF.
//
// # Allocation contract
//
// Allocates the Reader, the footer, the block of places and its name of the
// extraction, the parts that the Reader keeps and the keys of the summary,
// and a cursor of each kind that it uses when the pool has none.
func Open(ctx context.Context, file File, size int64, opts ReaderOptions) (*Reader, error) {
	if size < minTableSize {
		return nil, ErrCorrupt
	}
	f := make([]byte, footerLen)
	if err := readAt(ctx, file, f, size-footerLen); err != nil {
		return nil, err
	}
	be := binary.BigEndian
	version := be.Uint32(f[64:])
	if be.Uint64(f[72:]) != magic || be.Uint32(f[footerChecked:]) != crc32.Checksum(f[:footerChecked], castagnoli) ||
		version < formatVersion || version > placedVersion {

		return nil, ErrCorrupt
	}

	// The filter fields of the footer place the filter, or the block of
	// places of format version 3. That block places the filter, the block of
	// fragments and the filter of prefixes.
	index := blockPlace{off: be.Uint64(f[0:]), n: be.Uint64(f[8:]), crc: be.Uint32(f[16:])}
	p := places{filter: blockPlace{off: be.Uint64(f[20:]), n: be.Uint64(f[28:]), crc: be.Uint32(f[36:])}}
	start := uint64(size) - footerLen
	var ok bool
	if version == placedVersion {
		placed := p.filter
		if start, ok = tail(start, placed); !ok {
			return nil, ErrCorrupt
		}
		b, err := readBlock(ctx, file, placed.off, placed.n, placed.crc)
		if err != nil {
			return nil, err
		}
		if !p.decode(b) {
			return nil, ErrCorrupt
		}
	}
	// A table without the flag of versions has versions of 0 bytes.
	if opts.SuffixBytes < 0 || uint64(opts.SuffixBytes) != p.suffix {
		return nil, ErrSuffix
	}
	// The index ends at the block of places or at the footer, and each part
	// before it ends at the part after it.
	if start, ok = tail(start, index, p.prefix, p.filter, p.fragments); !ok {
		return nil, ErrCorrupt
	}

	b, err := readBlock(ctx, file, index.off, index.n, index.crc)
	if err != nil {
		return nil, err
	}
	rd := &Reader{
		f: file, index: b, dataEnd: start, partEnd: start, partitioned: version == partitionedVersion || p.partitioned,
		suffix: opts.SuffixBytes,
		summary: Summary{
			//nolint:gosec // G115: a Writer counts its entries in an int64
			Entries: int64(be.Uint64(f[40:])), Size: size, MinSeq: be.Uint64(f[48:]), MaxSeq: be.Uint64(f[56:]),
		},
	}

	// Every entry of the index is a restart point, so the last restart point
	// is the last entry. An index of no entries has none. The last entry of
	// the top-level index of the index is the last entry of the table too.
	var c blockCursor
	c.init(b)
	if c.restart(c.count - 1) {
		n := len(c.key) - seqLen
		rd.summary.LargestKey, rd.summary.LargestSeq = append([]byte{}, c.key[:n]...), be.Uint64(c.key[n:])
	}
	if c.err != nil {
		return nil, c.err
	}

	// A Reader of another extraction does not read the filter of prefixes.
	if opts.Prefix == nil || opts.Prefix.Name() != p.name {
		p.prefix = blockPlace{}
	}
	for _, part := range [...]struct {
		f     *tableFilter
		place blockPlace
	}{{f: &rd.keys, place: p.filter}, {f: &rd.prefixes, place: p.prefix}} {
		if part.place.n == 0 {
			continue
		}
		b, err := readBlock(ctx, file, part.place.off, part.place.n, part.place.crc)
		if err != nil {
			return nil, err
		}
		if rd.partitioned {
			if c.init(b); c.err != nil {
				return nil, c.err
			}
			part.f.index = b
		} else if part.f.fuse, err = filter.ParseFuse(b); err != nil {
			return nil, ErrCorrupt
		}
		part.f.ok, part.f.size = true, cap(b)
	}
	if p.fragments.n != 0 {
		b, err := readBlock(ctx, file, p.fragments.off, p.fragments.n, p.fragments.crc)
		if err != nil {
			return nil, err
		}
		if p.fragmentParts {
			// A table without fragments has no top-level index of them.
			if c.init(b); c.err != nil || c.count == 0 {
				return nil, ErrCorrupt
			}
			rd.fragTop = b
		} else {
			if err := rd.summarizeBlock(b); err != nil {
				return nil, err
			}
			rd.frags = b
		}
	}
	if rd.partitioned {
		dataEnd, err := rd.firstPartition()
		if err != nil {
			return nil, err
		}
		rd.dataEnd = dataEnd
	}

	// Open sets the cache after it reads the first block and the first
	// fragment partition, so the reads do not add them to the cache.
	if rd.fragTop != nil {
		if err := rd.summarizeParts(ctx); err != nil {
			return nil, err
		}
	}
	it := rd.NewIter(IterOptions{})
	if it.First(ctx) {
		rd.summary.SmallestKey, rd.summary.SmallestSeq = append([]byte{}, it.Key()...), it.Seq()
	}
	if err := it.Close(); err != nil {
		return nil, err
	}
	rd.cache, rd.cacheID = opts.Cache, opts.CacheID
	rd.lending, _ = opts.Cache.(LendingCache)

	return rd, nil
}

// Summary returns the summary of the table. It equals the summary that
// [Writer.Close] returned for the table.
//
// # Allocation contract
//
// Zero-alloc. The Reader shares the keys of the summary with the caller, so
// the caller must not change them.
func (r *Reader) Summary() Summary {
	return r.summary
}

// Footprint returns the bytes of memory that the buffers of r take. It adds
// the capacity of the index block or of the top-level index of the index, of
// each filter block or top-level index of a filter that the Reader keeps, of
// the block of fragments or the top-level index of the fragment partitions,
// and of the four keys of the summary. The buffers do not change after
// [Open], so every call returns the same value. Footprint does not count the
// blocks and the partitions of a [BlockCache] or the cursors of the pools
// that every Reader shares.
//
// # Allocation contract
//
// Zero-alloc.
func (r *Reader) Footprint() int {
	s := &r.summary
	n := cap(r.index) + r.keys.size + r.prefixes.size + cap(r.frags) + cap(r.fragTop)

	return n + cap(s.SmallestKey) + cap(s.LargestKey) + cap(s.RangeDeleteStart) + cap(s.RangeDeleteEnd)
}

// Span returns the data blocks of the table that can contain an entry whose
// key is at or after lower and before upper. A nil lower starts the range
// at the first key of the table, and a nil upper ends it after the last key.
// Of a table with versions, Span orders lower, upper and the keys of the
// index by [CompareWithSuffix] of the SuffixBytes of [ReaderOptions].
// Span searches the index of a table without partitions in memory and does
// not read a block. Of a table in partitions it reads the index partitions of
// lower and upper under ctx, and counts the data blocks before each partition
// from the top-level index.
//
// The first block of the range is the first block whose last key is at or
// after lower. The last block is the first block whose last key is at or
// after upper, or the last block of the table. That block can start at or
// after upper, so Span can count one block without an entry of the range.
// Span returns the zero Span for a range whose upper is at or before lower
// and for a range whose lower is after the last key. A table of any format
// version returns the same Span for the same entries and options.
//
// Span searches the index in halves for lower, and does not search for an
// empty lower. It searches for upper from the block of lower in steps that
// double, so a range of d blocks takes about 2·log2(d+2) more comparisons.
//
// Error modes:
//
//   - An entry of an index that does not decode returns [ErrCorrupt]. So do
//     a place of a block or a partition outside its part of the table, and
//     a last block that does not end after the start of the first block.
//   - A ctx that ended before a read of a partition returns the error of
//     ctx, and an error of the File returns an error that wraps it.
//
// # Allocation contract
//
// Zero-alloc once the pool of cursors has a cursor whose buffers have grown
// to the largest partition.
func (r *Reader) Span(ctx context.Context, lower, upper []byte) (Span, error) {
	if upper != nil && CompareWithSuffix(lower, 0, upper, 0, r.suffix) >= 0 {
		return Span{}, nil
	}
	if r.partitioned {
		it := r.NewIter(IterOptions{})
		var s Span
		s, it.err = it.span(ctx, lower, upper)

		return s, it.Close()
	}

	// Every entry of the index is a restart point, so the number of a restart
	// point is the number of its block. restartValue returns a nil value for
	// an entry that does not fit, and place refuses it. Every block ends at
	// or after an empty lower.
	c := blockCursor{suffix: r.suffix}
	c.init(r.index)
	first, ok := 0, true
	//dokimi:mutate-skip ror-true: a search for an empty lower returns the first block, so the search changes only the time
	if len(lower) != 0 {
		first, ok = c.search(0, c.count, lower, math.MaxUint64)
	}
	after := c.count
	if ok && upper != nil {
		after, ok = c.gallop(first, upper, math.MaxUint64)
	}
	if !ok {
		return Span{}, ErrCorrupt
	}
	if first == c.count {
		return Span{}, nil
	}

	start, _, ok := r.place(c.restartValue(first))
	last, end := c.count-1, r.dataEnd
	if ok && after < c.count {
		var off, n uint64
		off, n, ok = r.place(c.restartValue(after))
		last, end = after, off+n+trailerLen
	}
	if !ok || end <= start {
		return Span{}, ErrCorrupt
	}

	//nolint:gosec // G115: end is at most dataEnd, inside a table whose size is an int64
	return Span{Blocks: last - first + 1, Bytes: int64(end - start)}, nil
}

// MayContain reports whether the table may contain an entry with key. It
// reports true for every key of the table, and true for every key when the
// table has no filter. For a key that the table does not contain, a filter
// of b bits per fingerprint reports true with a probability of 2^-b. The
// filter of a table without partitions is in memory. Of a table in
// partitions, MayContain reads the filter partition of key under ctx, through
// the cache of r, and reports false without a read for a key after the last
// key of the table. The filter of a table with versions has the keys without
// their versions, so MayContain reports true for key at every version of a
// key of the table.
//
// Error modes:
//
//   - A ctx that ended returns the error of ctx for a table in partitions
//     with a filter, also when the cache contains the partition.
//   - An entry of the top-level index of the filter or a filter partition
//     that does not check returns [ErrCorrupt].
//   - An error of the File returns an error that wraps it.
//
// # Allocation contract
//
// Zero-alloc once the pool of cursors has a cursor whose buffers have grown
// to the largest partition. A filter partition that a cache does not contain
// allocates its copy or its [Block] for the cache, as a data block does.
func (r *Reader) MayContain(ctx context.Context, key []byte) (bool, error) {
	return r.mayContain(ctx, &r.keys, key, r.suffix)
}

// MayContainPrefix reports whether the table may contain a key whose prefix
// is p, by the filter of prefixes. It reports true for the prefix of every
// key of the table. It also reports true for every p when the table has no
// filter of prefixes, and when the Prefix of [ReaderOptions] is nil or has
// another name than the extraction of the table. For a prefix that no key of
// the table has, a filter of b bits per fingerprint reports true with a
// probability of 2^-b. Of a table with versions, p is a prefix of a key
// without its version. Of a table in partitions, MayContainPrefix reads the
// partition of p under ctx through the cache, as [Reader.MayContain] reads a
// filter partition. It reports false without a read for a p after the last
// prefix of the table.
//
// Error modes:
//
//   - A ctx that ended returns the error of ctx for a table in partitions
//     with a filter of prefixes, also when the cache contains the partition.
//   - An entry of the top-level index of the filter of prefixes or a
//     partition of it that does not check returns [ErrCorrupt].
//   - An error of the File returns an error that wraps it.
//
// # Allocation contract
//
// Zero-alloc once the pool of cursors has a cursor whose buffers have grown
// to the largest partition. A partition that a cache does not contain
// allocates its copy or its [Block] for the cache, as a data block does.
func (r *Reader) MayContainPrefix(ctx context.Context, p []byte) (bool, error) {
	return r.mayContain(ctx, &r.prefixes, p, 0)
}

// Get returns the value and the sequence of the first entry at or after the
// entry (key, seq) when that entry has key. For a snapshot seq, it is the
// newest version of key at or below seq. Get appends the value to dst and
// returns the extended slice. It returns dst unchanged and reports false
// when the filter refuses key, when the first entry at or after (key, seq)
// has another key, or when no entry follows.
//
// Get asks the filter first, for key without its version in a table with
// versions. key is the whole key, so Get returns an entry of key at its
// version alone. A lookup of a key that the table does not contain reads a
// data block only when the filter reports a false positive.
// Get reads at most one data block under ctx, through the cache when the
// Reader has one, and adds the block that it reads to the cache. Of a table
// in partitions it also reads the filter partition and the index partition
// of key, as it reads a data block. Get does not apply the fragments of the
// range tombstones of the table.
//
// Error modes:
//
//   - A data block or a partition that fails its checksum, its
//     decompression or its layout returns [ErrCorrupt].
//   - A ctx that ended returns the error of ctx when Get needs a block, also
//     when the cache contains the block.
//   - An error of the File, such as the error of ctx at the end of a wait,
//     returns an error that wraps it.
//
// # Allocation contract
//
// Zero-alloc when dst has room for the value, the pool of cursors has a
// cursor whose buffers have grown to the block and the partitions, and the
// cache contains them or the Reader has no cache. A block or a partition
// that a [BlockCache] does not contain allocates its copy for the cache. A
// block or a partition that a [LendingCache] does not contain takes a
// [Block] from a pool, which allocates only when the pool of its class is
// empty.
func (r *Reader) Get(ctx context.Context, key []byte, seq uint64, dst []byte) ([]byte, uint64, bool, error) {
	// The filter of a table without partitions is in memory, so a key that it
	// refuses takes no cursor.
	user := key[:max(len(key)-r.suffix, 0)]
	if !r.partitioned && r.keys.ok && !r.keys.fuse.MayContain(xxh3.Hash(user)) {
		return dst, 0, false, nil
	}

	// The lookup ends before the seek when the filter partition refuses key
	// or fails. Close returns the error of the cursor.
	it := r.NewIter(IterOptions{})
	may := true
	if r.partitioned && r.keys.ok {
		may, it.err = it.mayContain(ctx, r.keys.index, user)
	}
	ok := may && it.SeekGE(ctx, key, seq) && bytes.Equal(it.Key(), key)
	var found uint64
	if ok {
		dst, found = append(dst, it.Value()...), it.Seq()
	}
	if err := it.Close(); err != nil {
		return dst, 0, false, err
	}

	return dst, found, ok, nil
}

// NewIter returns a cursor over the entries of the table from the pool of
// cursors, under opts. The cursor is on no entry until [Iter.First],
// [Iter.Last], [Iter.SeekGE] or [Iter.SeekLT] moves it. The caller returns
// the cursor to the pool with [Iter.Close].
//
// # Allocation contract
//
// Zero-alloc when the pool has a cursor. Otherwise NewIter allocates one.
func (r *Reader) NewIter(opts IterOptions) *Iter {
	it := iters.Get()
	it.r, it.noFill, it.readAhead = r, opts.NoFill, opts.ReadAhead
	it.top.suffix, it.index.suffix, it.data.suffix = r.suffix, r.suffix, r.suffix
	if r.partitioned {
		it.top.init(r.index)
	} else {
		it.index.init(r.index)
	}

	return it
}

// RangeDeletes returns a cursor over the fragments of the range tombstones
// of the table, from the pool of cursors of fragments. The cursor is on no
// position until a move. A table without fragments returns a cursor without
// positions. The cursor of a table with fragment partitions reads them
// through the cache of r. The caller returns the cursor to the pool with
// [RangeDeleteIter.Close].
//
// # Allocation contract
//
// Zero-alloc when the pool has a cursor. Otherwise RangeDeletes allocates
// one.
func (r *Reader) RangeDeletes() *RangeDeleteIter {
	it := rangeIters.Get()
	it.r, it.c.suffix, it.part = r, r.suffix, -1
	if r.fragTop != nil {
		it.top.init(r.fragTop)
	} else {
		it.c.init(r.frags)
	}

	return it
}

// Check reads every block that a read of r can load from the File under
// ctx, and calls fn with each block that fails its checks. It then continues
// with the next block. It reads the data block of each entry of the index.
// Of a table in partitions it also reads each index partition, each filter
// partition and each fragment partition, and each partition of the filter of
// prefixes when r reads that filter. Check reads each block once from the
// File. It does not look the block up in the cache, and it does not add the
// block. It does not read the parts that r keeps in memory, which Open
// checked.
//
// A block fails its checks when its CRC32C differs from the CRC32C of its
// trailer or of its parent, when it does not decompress, or when its layout
// fails:
//
//   - A data block has restart points at entries without a shared prefix,
//     and entries that decode in the order of the table. Its first entry is
//     after the entry of the index entry of the block before it, and its last
//     entry is the entry of its own index entry.
//   - An index partition has entries at restart points that decode in the
//     order of the table, each with the place of a data block. Its first
//     entry is after the entry of the top-level entry before it, and its last
//     entry is the entry of its own top-level entry. Its count of entries is
//     the count of data blocks that the top-level index states for it.
//   - A filter partition parses as a [filter.Fuse].
//   - A fragment partition passes the checks of a partition that a
//     [RangeDeleteIter] reads from the File.
//
// A damaged index partition hides the places of its data blocks, so Check
// reports the partition and continues with the next partition. fn receives
// a [BlockDamage] that Check reuses. Its keys are parts of the blocks of the
// index, so they are valid until fn returns.
//
// Error modes:
//
//   - An error of fn ends Check with that error.
//   - A ctx that ended before the read of a block returns the error of ctx.
//   - An error of the File returns an error that wraps it.
//   - An entry of the index block or of a top-level index that does not
//     decode returns [ErrCorrupt]. Open checked the checksums of those
//     blocks, so such an entry is a fault of the Writer, and Check cannot
//     place the blocks after it.
//
// # Allocation contract
//
// Zero-alloc once the pool of the states of Check has a state whose buffers
// have grown to the largest block and the largest partition, apart from what
// fn allocates.
func (r *Reader) Check(ctx context.Context, fn func(*BlockDamage) error) error {
	ck := checkers.Get()
	defer ck.release()

	ck.r, ck.fn = r, fn
	ck.top.suffix, ck.index.suffix, ck.data.suffix = r.suffix, r.suffix, r.suffix
	if err := ck.dataBlocks(ctx); err != nil || !r.partitioned {
		return err
	}
	for _, f := range [...]*tableFilter{&r.keys, &r.prefixes} {
		if !f.ok {
			continue
		}
		if err := ck.filterParts(ctx, f.index); err != nil {
			return err
		}
	}
	if r.fragTop == nil {
		return nil
	}

	return ck.fragmentParts(ctx)
}

// mayContain reports whether the table may contain key by the filter f, for
// MayContain and MayContainPrefix. It asks the filter for key without its
// last suffix bytes, so MayContain passes the width of the versions and
// MayContainPrefix passes 0. A call with the width keeps MayContain small
// enough to inline. A table without the filter may contain every key. The
// filter block of a table without partitions is in memory. Of a table in
// partitions, mayContain takes a cursor and reads the partition of key, as
// the mayContain of [Iter] does.
func (r *Reader) mayContain(ctx context.Context, f *tableFilter, key []byte, suffix int) (bool, error) {
	//dokimi:mutate-skip ror-true: a key without its last 0 bytes is the key, so the short path changes only the time
	if suffix != 0 {
		key = key[:max(len(key)-suffix, 0)]
	}
	if !f.ok {
		return true, nil
	}
	if !r.partitioned {
		return f.fuse.MayContain(xxh3.Hash(key)), nil
	}
	it := r.NewIter(IterOptions{})
	var may bool
	may, it.err = it.mayContain(ctx, f.index, key)

	return may, it.Close()
}

// firstPartition returns the offset of the first partition of a table in
// partitions, where its data blocks end. It is the first partition of the
// filter of the keys, of the filter of the prefixes, of the index or of the
// fragments, of the first of them that has a partition. Open checks the
// top-level index of each. A table without partitions has no data blocks,
// and firstPartition returns partEnd for it. It returns ErrCorrupt for a
// first entry that does not decode or whose offset is past partEnd.
func (r *Reader) firstPartition() (uint64, error) {
	var top blockCursor
	for _, b := range [...][]byte{r.keys.index, r.prefixes.index, r.index, r.fragTop} {
		if top.init(b); top.count != 0 {
			break
		}
	}
	if top.count == 0 {
		return r.partEnd, nil
	}
	if !top.restart(0) {
		return 0, ErrCorrupt
	}
	off, n := binary.Uvarint(top.value)
	if n <= 0 || off > r.partEnd {
		return 0, ErrCorrupt
	}

	return off, nil
}

// summarizeBlock checks the block of fragments b of a table without
// fragment partitions, as checkFragments checks a block of fragments. It
// sets the count of the entries of b in the summary, and copies of the start
// of its first fragment and of the end of its last fragment. It returns the
// error of checkFragments.
func (r *Reader) summarizeBlock(b []byte) error {
	c := blockCursor{suffix: r.suffix}
	var last []byte
	count, end, err := checkFragments(&c, &last, b, nil)
	if err != nil {
		return err
	}
	// checkFragments decoded the first entry, so it decodes again.
	c.restart(0)
	n := len(c.key) - seqLen
	s := &r.summary
	s.RangeDeletes, s.RangeDeleteStart, s.RangeDeleteEnd = int64(count), append([]byte{}, c.key[:n]...),
		append([]byte{}, end...)

	return nil
}

// summarizeParts sets the count and the bounds of the fragments of a table
// with fragment partitions in the summary. The last entry of the top-level
// index of the fragments contains the end of the last fragment and the count
// of every entry. The first fragment partition contains the start of the
// first fragment, which summarizeParts reads under ctx through a cursor of
// fragments. It returns ErrCorrupt for a last entry that does not decode, and
// the error of the cursor.
func (r *Reader) summarizeParts(ctx context.Context) error {
	var top blockCursor
	top.init(r.fragTop)
	if !top.restart(top.count - 1) {
		return ErrCorrupt
	}
	_, _, count, _, ok := r.partition(top.value, true)
	if !ok {
		return ErrCorrupt
	}
	it := r.RangeDeletes()
	defer it.Close()
	if !it.First(ctx) {
		return cmp.Or(it.Err(), ErrCorrupt) //nolint:wrapcheck // cmp.Or returns the error of the cursor or ErrCorrupt
	}
	s := &r.summary
	//nolint:gosec // G115: a Writer counts its fragments in an int64
	s.RangeDeletes, s.RangeDeleteStart = int64(count), append([]byte{}, it.Start()...)
	s.RangeDeleteEnd = append([]byte{}, top.key[:len(top.key)-seqLen]...)

	return nil
}

// place returns the offset and the stored length of the data block of the
// index value v. It reports false for a value with other fields and for a
// block whose trailer does not end at or before dataEnd. [Iter] refuses the
// same values when it loads a block.
func (r *Reader) place(v []byte) (uint64, uint64, bool) {
	off, n1 := binary.Uvarint(v)
	// When the first varint fails, Uvarint decodes the same bytes for the
	// second and fails again, so n2 reports either failure.
	n, n2 := binary.Uvarint(v[max(n1, 0):])
	end := r.dataEnd
	if n2 <= 0 || len(v) != n1+n2+crcLen ||
		//dokimi:mutate-skip ror-boundary: the next check refuses a block of end bytes, because its trailer does not fit
		n > end ||
		end-n < trailerLen || off > end-n-trailerLen {

		return 0, 0, false
	}

	return off, n, true
}

// partition returns the place of the partition of the top-level entry value
// v: its offset, its length without its trailer, the number of data blocks
// up to its end for a counted value of the top-level index of the index,
// and its CRC32C. It reports false for a value with other fields and for a
// partition outside the bytes from dataEnd to partEnd.
func (r *Reader) partition(v []byte, counted bool) (off, n, blocks uint64, sum uint32, ok bool) {
	off, n1 := binary.Uvarint(v)
	// When a varint fails, Uvarint decodes the same bytes for the next one
	// and fails again, so the last varint reports a failure of any of them.
	n, last := binary.Uvarint(v[max(n1, 0):])
	at := max(n1, 0) + max(last, 0)
	if counted {
		var n3 int
		blocks, n3 = binary.Uvarint(v[at:])
		at, last = at+max(n3, 0), n3
	}
	end := r.partEnd
	if last <= 0 || len(v) != at+crcLen ||
		n > end ||
		end-n < trailerLen || off < r.dataEnd || off > end-n-trailerLen {

		return 0, 0, 0, 0, false
	}

	return off, n, blocks, binary.BigEndian.Uint32(v[at:]), true
}

// checker is the state of a [Reader.Check]. It has the Reader, the function
// of the caller and the damage that it passes to the function. It also has
// the buffers and the cursors of the blocks that it reads. A Check takes a
// checker from the pool of checkers and returns it with its buffers.
type checker struct {
	// r is the Reader of the Check.
	r *Reader

	// fn is the function of the caller of Check.
	fn func(*BlockDamage) error

	// damage is the damage that report passes to fn.
	damage BlockDamage

	// raw and buf receive a stored block and its trailer, and the decoding of
	// a compressed block. partRaw and partBuf receive an index partition,
	// which the checker keeps while it checks the data blocks of the
	// partition.
	raw, buf, partRaw, partBuf []byte

	// prev receives the internal key of the entry before the current one.
	prev []byte

	// top is on the top-level index that the checker walks, index on the
	// index block or the current index partition, and data on the block that
	// the checker checks. The width of the versions of each is the width of
	// r.
	top, index, data blockCursor
}

// release returns ck and its buffers to the pool of checkers.
func (ck *checker) release() {
	*ck = checker{
		raw:     ck.raw[:0],
		buf:     ck.buf[:0],
		partRaw: ck.partRaw[:0],
		partBuf: ck.partBuf[:0],
		prev:    ck.prev[:0],
		top:     blockCursor{key: ck.top.key[:0]},
		index:   blockCursor{key: ck.index.key[:0]},
		data:    blockCursor{key: ck.data.key[:0]},
	}
	checkers.Put(ck)
}

// dataBlocks checks the data block of each entry of the index. Of a table in
// partitions it first checks each index partition with checkIndex. A
// partition that fails takes a report, and dataBlocks continues with the next
// partition. It returns the first error of read, of indexBlocks and of
// report, and ErrCorrupt for an entry of the top-level index that does not
// decode.
func (ck *checker) dataBlocks(ctx context.Context) error {
	r := ck.r
	if !r.partitioned {
		ck.index.init(r.index)

		return ck.indexBlocks(ctx, nil)
	}
	ck.top.init(r.index)
	var after []byte
	blocks := uint64(0)
	for p := range ck.top.count {
		k := ck.top.restartKey(p)
		off, n, upTo, want, ok := r.partition(ck.top.restartValue(p), true)
		if k == nil || !ok {
			return ErrCorrupt
		}
		b, intact, err := ck.read(ctx, off, n, want, &ck.partRaw, &ck.partBuf)
		if err != nil {
			return err
		}
		if intact && upTo >= blocks && ck.checkIndex(b, after, k, upTo-blocks) {
			err = ck.indexBlocks(ctx, after)
		} else {
			err = ck.report(IndexBlock, off, n, after, k)
		}
		if err != nil {
			return err
		}
		after, blocks = k, upTo
	}

	return nil
}

// indexBlocks checks the data block of each entry of the index cursor: the
// index block, or an index partition that passed checkIndex. after is the
// internal key of the entry of the index before the first one, or nil for
// the first block of the table. A block that fails its checksum, its
// decompression or checkData takes a report. indexBlocks returns the first
// error of read and of report, and ErrCorrupt for an entry whose key or
// place does not decode.
func (ck *checker) indexBlocks(ctx context.Context, after []byte) error {
	for i := range ck.index.count {
		k, v := ck.index.restartKey(i), ck.index.restartValue(i)
		off, n, ok := ck.r.place(v)
		if k == nil || !ok {
			return ErrCorrupt
		}
		b, intact, err := ck.read(ctx, off, n, binary.BigEndian.Uint32(v[len(v)-crcLen:]), &ck.raw, &ck.buf)
		if err != nil {
			return err
		}
		if !intact || !ck.checkData(b, after, k) {
			if err := ck.report(DataBlock, off, n, after, k); err != nil {
				return err
			}
		}
		after = k
	}

	return nil
}

// checkData reports whether the data block b passes the checks of its
// layout. The block has at least one restart point, and the first is at the
// offset 0. Each restart point is at the offset of an entry without a shared
// prefix. The entries decode in the order of the table. The first entry is
// after the internal key after when after is not nil. The last entry is the
// internal key last of the index entry of the block.
func (ck *checker) checkData(b, after, last []byte) bool {
	c := &ck.data
	if c.init(b); c.err != nil {
		return false
	}
	ck.prev, c.key = append(ck.prev[:0], after...), c.key[:0]
	ordered := after != nil
	restart := 0
	for at := 0; at < c.end; at = c.after {
		if restart < c.count && int(binary.BigEndian.Uint32(c.block[c.end+4*restart:])) == at {
			c.key, restart = c.key[:0], restart+1
		}
		if !c.decode(at) {
			return false
		}
		n, pn := len(c.key)-seqLen, len(ck.prev)-seqLen
		if ordered && CompareWithSuffix(c.key[:n], binary.BigEndian.Uint64(c.key[n:]), ck.prev[:pn],
			binary.BigEndian.Uint64(ck.prev[pn:]), c.suffix) <= 0 {

			return false
		}
		ck.prev, ordered = append(ck.prev[:0], c.key...), true
	}

	return c.count != 0 && restart == c.count && bytes.Equal(ck.prev, last)
}

// checkIndex reports whether the index partition b passes the checks of its
// layout. The partition has blocks entries in the order of the table. Each
// entry is at a restart point after the entry before it in the bytes of b.
// Each entry has the place of a data block. The first entry is after the
// internal key after when after is not nil. The last entry is the internal
// key last of the top-level entry of the partition. checkIndex leaves the
// index cursor on b.
func (ck *checker) checkIndex(b, after, last []byte, blocks uint64) bool {
	c := &ck.index
	//nolint:gosec // G115: a block has fewer entries than bytes
	if c.init(b); c.err != nil || uint64(c.count) != blocks {
		return false
	}
	ck.prev = append(ck.prev[:0], after...)
	ordered := after != nil
	at := 0
	for i := range c.count {
		if !c.restart(i) || c.at != at {
			return false
		}
		if _, _, ok := ck.r.place(c.value); !ok {
			return false
		}
		n, pn := len(c.key)-seqLen, len(ck.prev)-seqLen
		if ordered && CompareWithSuffix(c.key[:n], binary.BigEndian.Uint64(c.key[n:]), ck.prev[:pn],
			binary.BigEndian.Uint64(ck.prev[pn:]), c.suffix) <= 0 {

			return false
		}
		ck.prev, ordered, at = append(ck.prev[:0], c.key...), true, c.after
	}

	return at == c.end && bytes.Equal(ck.prev, last)
}

// filterParts checks each partition of the filter whose top-level index is
// index. A partition that fails its checksum, its decompression or its parse
// as a filter.Fuse takes a report. filterParts returns the first error of
// read and of report, and ErrCorrupt for an entry of the top-level index
// that does not decode.
func (ck *checker) filterParts(ctx context.Context, index []byte) error {
	ck.top.init(index)
	var after []byte
	for p := range ck.top.count {
		k := ck.top.restartKey(p)
		off, n, _, want, ok := ck.r.partition(ck.top.restartValue(p), false)
		if k == nil || !ok {
			return ErrCorrupt
		}
		b, intact, err := ck.read(ctx, off, n, want, &ck.raw, &ck.buf)
		if err != nil {
			return err
		}
		if intact {
			_, err = filter.ParseFuse(b)
			intact = err == nil
		}
		if !intact {
			if err := ck.report(FilterBlock, off, n, after, k); err != nil {
				return err
			}
		}
		after = k
	}

	return nil
}

// fragmentParts checks each fragment partition of the table with
// checkPartition, as a RangeDeleteIter checks a partition that it reads. A
// partition that fails takes a report. fragmentParts returns the first
// error of read and of report, and ErrCorrupt for an entry of the top-level
// index of the fragments that does not decode.
func (ck *checker) fragmentParts(ctx context.Context) error {
	ck.top.init(ck.r.fragTop)
	var after []byte
	for p := range ck.top.count {
		k := ck.top.restartKey(p)
		off, n, _, want, ok := ck.r.partition(ck.top.restartValue(p), true)
		if k == nil || !ok {
			return ErrCorrupt
		}
		b, intact, err := ck.read(ctx, off, n, want, &ck.raw, &ck.buf)
		if err != nil {
			return err
		}
		if !intact || !checkPartition(ck.r, &ck.top, &ck.data, &ck.prev, b, p) {
			if err := ck.report(FragmentBlock, off, n, after, k); err != nil {
				return err
			}
		}
		after = k
	}

	return nil
}

// read reads the block of n stored bytes at off and its trailer from the
// File under ctx into *raw, and returns the bytes of the block. It decodes a
// compressed block into *buf. want is the CRC32C that the parent of the
// block states. read reports false for a block that fails its checksum or
// its decompression. It returns the error of a ctx that ended before the
// read, and an error that wraps an error of the File.
func (ck *checker) read(ctx context.Context, off, n uint64, want uint32, raw, buf *[]byte) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	//nolint:gosec // G115: the block lies inside a table whose size is an int64
	size := int(n) + trailerLen
	*raw = slices.Grow((*raw)[:0], size)[:size]
	//nolint:gosec // G115: the block lies inside the table
	if err := readAt(ctx, ck.r.f, *raw, int64(off)); err != nil {
		return nil, false, err
	}
	b, err := unseal(*raw, want, buf)

	return b, err == nil, nil
}

// report passes the damage of the block of n stored bytes at off with role
// to fn, and returns the error of fn. after and last are the internal keys
// of the entries of the index before the block and of the block, or nil for
// an after before the first block. The damage has them without their
// sequences.
func (ck *checker) report(role BlockRole, off, n uint64, after, last []byte) error {
	d := &ck.damage
	//nolint:gosec // G115: the block lies inside a table whose size is an int64
	*d = BlockDamage{
		Err: ErrCorrupt, Last: last[:len(last)-seqLen], Offset: int64(off), Size: int64(n) + trailerLen, Role: role,
	}
	if after != nil {
		d.After = after[:len(after)-seqLen]
	}

	return ck.fn(d)
}

// tail checks the places of the parts at the end of a table and returns the
// offset of the first of them. parts lists the parts from the last one back.
// The last part ends at the trailer before the offset end. Each other part
// ends at the trailer before the part after it. A part of no bytes is missing
// and has the offset 0 and the CRC32C 0. An index or a block of places of no
// bytes does not decode, so Open refuses it after its read. tail reports
// false for a part that does not end where it must, and for a missing part
// with another offset or CRC32C.
func tail(end uint64, parts ...blockPlace) (uint64, bool) {
	for _, p := range parts {
		if p.n == 0 {
			if p.off != 0 || p.crc != 0 {
				return 0, false
			}

			continue
		}
		//dokimi:mutate-skip ror-boundary: a part at the offset 5 leaves 0 bytes for a part before it, and the next check refuses a part of 1 byte or more there
		if end < trailerLen ||
			p.n > end-trailerLen || p.off != end-trailerLen-p.n {

			return 0, false
		}
		end = p.off
	}

	return end, true
}

// readBlock reads the stored block of n bytes at off and its trailer from f
// under ctx into a new buffer. It returns the bytes of the block. want is the
// CRC32C that the parent of the block states.
//
// Error modes:
//
//   - A block that fails its checksum, has a CRC32C other than want or fails
//     its decompression returns [ErrCorrupt].
//   - An error of f returns an error that wraps it.
func readBlock(ctx context.Context, f File, off, n uint64, want uint32) ([]byte, error) {
	b := make([]byte, n+trailerLen)
	//nolint:gosec // G115: Open checks that the block lies inside the table
	if err := readAt(ctx, f, b, int64(off)); err != nil {
		return nil, err
	}
	var decoded []byte

	return unseal(b, want, &decoded)
}

// unseal checks the trailer at the end of b and returns the bytes of the
// block. b is a stored block followed by its trailer, and want is the CRC32C
// that the parent of the block states. A block without compression is a
// part of b. unseal decodes a compressed block into *buf, and grows *buf when
// the block does not fit.
//
// Error modes:
//
//   - A block whose CRC32C differs from the CRC32C of its trailer or from
//     want, a compression byte other than 0 and 1, and a compressed block
//     that does not decode return [ErrCorrupt].
func unseal(b []byte, want uint32, buf *[]byte) ([]byte, error) {
	n := len(b) - trailerLen
	sum := crc32.Checksum(b[:n+1], castagnoli)
	if sum != binary.BigEndian.Uint32(b[n+1:]) || sum != want {
		return nil, ErrCorrupt
	}
	if kind := Compression(b[n]); kind == NoCompression {
		return b[:n:n], nil
	} else if kind != SnappyCompression {
		return nil, ErrCorrupt
	}
	decoded, err := snappy.AppendDecode((*buf)[:0], b[:n])
	if err != nil {
		return nil, ErrCorrupt
	}
	*buf = decoded

	return decoded, nil
}

// readAt reads len(p) bytes of f at off under ctx. It returns nil once f
// returned them all, also with an error such as io.EOF. For fewer bytes it
// returns an error that wraps the error of f, or io.ErrUnexpectedEOF when f
// returned no error.
func readAt(ctx context.Context, f File, p []byte, off int64) error {
	n, err := f.ReadAt(ctx, p, off)
	if n == len(p) {
		return nil
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}

	return fmt.Errorf("sstable: read %d bytes at %d: %w", len(p), off, err)
}
