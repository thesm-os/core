// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"strconv"

	"go.thesmos.sh/core/compress/snappy"
	"go.thesmos.sh/core/filter"
	"go.thesmos.sh/core/hash/xxh3"
)

// minTableSize is the size of the smallest table, a table of no entries. Its
// index block contains only the count of its restart points in 4 bytes. The
// trailer of the index block and the footer of 80 bytes follow it.
const minTableSize = 4 + trailerLen + footerLen

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

	// CacheID is the identity of the table in Cache. The readers of
	// different tables that share a Cache need different identities. A
	// Reader checks the CRC32C of each block that it reads from the File,
	// and not of a block that Cache returns. The Readers of two tables with
	// one identity therefore read the blocks of each other as valid data.
	// [go.thesmos.sh/core/sstable/blockcache.Cache.NewID] issues identities
	// that never repeat within one cache. It never issues 0, so none of its
	// identities equals the CacheID of a Reader that sets none.
	CacheID uint64
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
	// IndexBlock is the role of an index partition of a table of format
	// version 2. A Reader keeps the index block of a table of format version
	// 1 in memory and never looks it up in a cache.
	IndexBlock BlockRole = 2 // index
	// FilterBlock is the role of a filter partition of a table of format
	// version 2. A Reader keeps the filter block of a table of format
	// version 1 in memory and never looks it up in a cache.
	FilterBlock BlockRole = 3 // filter
)

// Valid reports whether r is [DataBlock], [IndexBlock] or [FilterBlock].
func (r BlockRole) Valid() bool {
	return DataBlock <= r && r <= FilterBlock
}

// String returns "data", "index" or "filter", and "BlockRole(N)" for a value
// that is not valid.
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
// alone. A Reader of a table of format version 1 passes only [DataBlock].
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

// Reader reads a table through a [File]. [Reader.Get] looks up one key, and
// [Reader.NewIter] returns a cursor over the entries. [Reader.Span] returns
// the data blocks of a key range from the index. The Reader keeps the
// index block, the filter block and the summary of a table of format version
// 1 in memory. Of a table of format version 2 it keeps the two top-level
// indexes and the summary, and reads the index partitions and the filter
// partitions as it reads data blocks. The Reader reads a block for each
// lookup or move that needs it, under the context of that call.
// [Reader.Footprint] returns the bytes of the buffers that it keeps.
//
// # Concurrency
//
// A Reader is safe for concurrent use.
//
// # Allocation contract
//
// [Open] allocates the Reader, the index block, the filter block or the two
// top-level indexes, and the keys of the summary. Get, [Reader.MayContain],
// [Reader.Span] and NewIter do not allocate once the pool of cursors has a
// cursor whose buffers have grown to the largest block and the largest
// partition. A read through a [BlockCache] allocates the copy of each block
// or partition that it adds. A read through a [LendingCache] takes a [Block]
// from a pool in place of that copy.
type Reader struct {
	// f contains the table.
	f File

	// cache keeps the decoded blocks and partitions under cacheID, or is
	// nil.
	cache BlockCache

	// lending is cache when cache is a LendingCache, and nil otherwise.
	lending LendingCache

	// index is the index block of a table of format version 1, or the
	// top-level index of the index of format version 2.
	index []byte

	// filterIndex is the top-level index of the filter of a table of format
	// version 2 with a filter, and nil otherwise.
	filterIndex []byte

	// filter is the filter of a table of format version 1 when hasFilter is
	// true.
	filter filter.Fuse

	// summary describes the table.
	summary Summary

	// cacheID is the identity of the table in cache.
	cacheID uint64

	// dataEnd is the offset after the trailer of the last data block.
	dataEnd uint64

	// partEnd is the offset after the trailer of the last partition of a
	// table of format version 2, where its top-level indexes start.
	partEnd uint64

	// filterSize is the capacity of the filter block that filter reads, or
	// of filterIndex, and 0 for a table without a filter.
	filterSize int

	// hasFilter reports whether the table has a filter.
	hasFilter bool

	// partitioned reports whether the table has format version 2.
	partitioned bool
}

// Open reads the footer, the index block and the filter block of the table
// of size bytes in file under ctx, and checks the checksum and the layout of
// each. Of a table of format version 2 it reads the two top-level indexes
// in place of the two blocks. It compares the CRC32C of each block with the
// CRC32C that the footer states for it. It reads the first entry of the
// first data block for the smallest key of the summary, and the last entry
// of the index for the largest key. It does not add the first data block or
// its partition to the cache.
//
// Error modes:
//
//   - A table shorter than 89 bytes returns [ErrCorrupt].
//   - A footer with another magic number, a failed checksum, a version other
//     than 1 and 2 or places of the index and the filter that do not fit the
//     table returns ErrCorrupt. A footer of a table without a filter that
//     states a filter offset or a filter CRC32C returns ErrCorrupt too.
//   - An index block, a filter block, a top-level index, a first partition
//     or a first data block that fails its checksum or its layout returns
//     ErrCorrupt. So does a block whose CRC32C differs from the CRC32C that
//     the footer or its index entry states.
//   - An error of file, such as the error of ctx at the end of a wait,
//     returns an error that wraps it. A read of fewer bytes without an error
//     returns an error that wraps io.ErrUnexpectedEOF.
//
// # Allocation contract
//
// Allocates the Reader, the footer, the index block, the filter block or the
// top-level indexes and the keys of the summary, and a cursor when the pool
// has none.
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
		version != formatVersion && version != partitionedVersion {

		return nil, ErrCorrupt
	}

	// The index ends at the trailer before the footer, and a filter ends at
	// the trailer before the index.
	indexOff, indexLen, indexCRC := be.Uint64(f[0:]), be.Uint64(f[8:]), be.Uint32(f[16:])
	filterOff, filterLen, filterCRC := be.Uint64(f[20:]), be.Uint64(f[28:]), be.Uint32(f[36:])
	end := uint64(size) - footerLen - trailerLen
	if indexLen > end || indexOff != end-indexLen {
		return nil, ErrCorrupt
	}
	dataEnd := indexOff
	if filterLen != 0 {
		//dokimi:mutate-skip ror-boundary: an index at offset 5 leaves a filter no byte, which the next check refuses
		if indexOff < trailerLen ||
			filterLen > indexOff-trailerLen || filterOff != indexOff-trailerLen-filterLen {

			return nil, ErrCorrupt
		}
		dataEnd = filterOff
	} else if filterOff != 0 || filterCRC != 0 {
		return nil, ErrCorrupt
	}

	index, err := readBlock(ctx, file, indexOff, indexLen, indexCRC)
	if err != nil {
		return nil, err
	}
	rd := &Reader{
		f: file, index: index, dataEnd: dataEnd, partEnd: dataEnd, partitioned: version == partitionedVersion,
		summary: Summary{
			//nolint:gosec // G115: a Writer counts its entries in an int64
			Entries: int64(be.Uint64(f[40:])), Size: size, MinSeq: be.Uint64(f[48:]), MaxSeq: be.Uint64(f[56:]),
		},
	}

	// Every entry of the index is a restart point, so the last restart point
	// is the last entry. An index of no entries has none. The last entry of
	// the top-level index of the index is the last entry of the table too.
	var c blockCursor
	c.init(index)
	if c.restart(c.count - 1) {
		n := len(c.key) - seqLen
		rd.summary.LargestKey, rd.summary.LargestSeq = append([]byte{}, c.key[:n]...), be.Uint64(c.key[n:])
	}
	if c.err != nil {
		return nil, c.err
	}

	if filterLen != 0 {
		b, err := readBlock(ctx, file, filterOff, filterLen, filterCRC)
		if err != nil {
			return nil, err
		}
		if rd.partitioned {
			rd.filterIndex = b
		} else if rd.filter, err = filter.ParseFuse(b); err != nil {
			return nil, ErrCorrupt
		}
		rd.hasFilter, rd.filterSize = true, cap(b)
	}
	if rd.partitioned {
		dataEnd, err := rd.firstPartition()
		if err != nil {
			return nil, err
		}
		rd.dataEnd = dataEnd
	}

	// Open sets the cache after it reads the first block, so the read does
	// not add the block to the cache.
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
// the capacity of the index block, of the filter block and of the two keys
// of the summary, and of the two top-level indexes in place of the two
// blocks of a table of format version 2. The buffers do not change after
// [Open], so every call returns the same value. Footprint does not count the
// blocks and the partitions of a [BlockCache] or the cursors of the pool that
// every Reader shares.
//
// # Allocation contract
//
// Zero-alloc.
func (r *Reader) Footprint() int {
	return cap(r.index) + r.filterSize + cap(r.summary.SmallestKey) + cap(r.summary.LargestKey)
}

// Span returns the data blocks of the table that can contain an entry whose
// key is at or after lower and before upper. A nil lower starts the range
// at the first key of the table, and a nil upper ends it after the last key.
// Span searches the index of a table of format version 1 in memory and does
// not read a block. Of a table of format version 2 it reads the index
// partitions of lower and upper under ctx, and counts the data blocks before
// each partition from the top-level index.
//
// The first block of the range is the first block whose last key is at or
// after lower. The last block is the first block whose last key is at or
// after upper, or the last block of the table. That block can start at or
// after upper, so Span can count one block without an entry of the range.
// Span returns the zero Span for a range whose upper is at or before lower
// and for a range whose lower is after the last key. A table of either
// format version returns the same Span for the same entries and options.
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
	if upper != nil && bytes.Compare(lower, upper) >= 0 {
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
	var c blockCursor
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
// filter of a table of format version 1 is in memory. Of a table of format
// version 2, MayContain reads the filter partition of key under ctx, through
// the cache of r, and reports false without a read for a key after the last
// key of the table.
//
// Error modes:
//
//   - A ctx that ended returns the error of ctx for a table of format
//     version 2, also when the cache contains the partition.
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
	if !r.partitioned {
		return !r.hasFilter || r.filter.MayContain(xxh3.Hash(key)), nil
	}
	it := r.NewIter(IterOptions{})
	var may bool
	may, it.err = it.mayContain(ctx, key)

	return may, it.Close()
}

// Get returns the value and the sequence of the first entry at or after the
// entry (key, seq) when that entry has key. For a snapshot seq, it is the
// newest version of key at or below seq. Get appends the value to dst and
// returns the extended slice. It returns dst unchanged and reports false
// when the filter refuses key, when the first entry at or after (key, seq)
// has another key, or when no entry follows.
//
// Get asks the filter first. A lookup of a key that the table does not
// contain reads a data block only when the filter reports a false positive.
// Get reads at most one data block under ctx, through the cache when the
// Reader has one, and adds the block that it reads to the cache. Of a table
// of format version 2 it also reads the filter partition and the index
// partition of key, as it reads a data block.
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
	// The filter of a table of format version 1 is in memory, so a key that
	// it refuses takes no cursor.
	if !r.partitioned && r.hasFilter && !r.filter.MayContain(xxh3.Hash(key)) {
		return dst, 0, false, nil
	}

	// The lookup ends before the seek when the filter partition refuses key
	// or fails. Close returns the error of the cursor.
	it := r.NewIter(IterOptions{})
	may := true
	if r.partitioned {
		may, it.err = it.mayContain(ctx, key)
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
	if r.partitioned {
		it.top.init(r.index)
	} else {
		it.index.init(r.index)
	}

	return it
}

// firstPartition returns the offset of the first partition of a table of
// format version 2, where its data blocks end: the first filter partition,
// or the first index partition of a table without filter partitions. A
// table without partitions has no data blocks, and firstPartition returns
// partEnd for it. It returns ErrCorrupt for a top-level index of the filter
// without room for its restart offsets, and for a first entry that does not
// decode or whose offset is past partEnd.
func (r *Reader) firstPartition() (uint64, error) {
	var top blockCursor
	if r.hasFilter {
		top.init(r.filterIndex)
		if top.err != nil {
			return 0, top.err
		}
	}
	if top.count == 0 {
		top.init(r.index)
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
