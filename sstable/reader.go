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
	// Cache keeps the decoded data blocks of the Reader. Without a Cache the
	// Reader reads every block from the File.
	Cache BlockCache

	// CacheID is the identity of the table in Cache. The readers of
	// different tables that share a Cache need different identities.
	CacheID uint64
}

// BlockCache keeps the decoded data blocks of the readers that share it. Its
// key is the identity of the table and the offset of the block in the table.
// Each call takes the context of the read that looks the block up, so a
// cache counts and traces its lookups under that context.
//
// A Reader never writes a block that it adds to the cache or that Get
// returns, and the cache must not change a block either. The capacity and
// the eviction of the cache are the policy of its owner.
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

// Reader reads a table through a [File]. [Reader.Get] looks up one key, and
// [Reader.NewIter] returns a cursor over the entries. The Reader keeps the
// index block, the filter block and the summary of the table in memory, and
// reads a data block for each lookup or move that needs it, under the
// context of that call.
//
// # Concurrency
//
// A Reader is safe for concurrent use.
//
// # Allocation contract
//
// [Open] allocates the Reader, the index block, the filter block and the
// keys of the summary. Get, [Reader.MayContain] and NewIter do not allocate
// once the pool of cursors has a cursor whose buffers have grown to the
// largest block. A read through the cache allocates the copy of each block
// that it adds.
type Reader struct {
	// f contains the table.
	f File

	// cache keeps the decoded data blocks under cacheID, or is nil.
	cache BlockCache

	// index is the index block.
	index []byte

	// filter is the filter of the table when hasFilter is true.
	filter filter.Fuse

	// summary describes the table.
	summary Summary

	// cacheID is the identity of the table in cache.
	cacheID uint64

	// dataEnd is the offset after the trailer of the last data block.
	dataEnd uint64

	// hasFilter reports whether the table has a filter.
	hasFilter bool
}

// Open reads the footer, the index block and the filter block of the table
// of size bytes in file under ctx, and checks the checksum and the layout of
// each. It compares the CRC32C of the index block and the filter block with
// the CRC32C that the footer states for each. It reads the first entry of
// the first data block for the smallest key of the summary, and the last
// entry of the index for the largest key. It does not add the first data
// block to the cache.
//
// Error modes:
//
//   - A table shorter than 89 bytes returns [ErrCorrupt].
//   - A footer with another magic number, a failed checksum, another version
//     or places of the index and the filter that do not fit the table
//     returns ErrCorrupt. A footer of a table without a filter that states
//     a filter offset or a filter CRC32C returns ErrCorrupt too.
//   - An index block, a filter block or a first data block that fails its
//     checksum or its layout returns ErrCorrupt. So does a block whose CRC32C
//     differs from the CRC32C that the footer or its index entry states.
//   - An error of file, such as the error of ctx at the end of a wait,
//     returns an error that wraps it. A read of fewer bytes without an error
//     returns an error that wraps io.ErrUnexpectedEOF.
//
// # Allocation contract
//
// Allocates the Reader, the footer, the index block, the filter block and
// the keys of the summary, and a cursor when the pool has none.
func Open(ctx context.Context, file File, size int64, opts ReaderOptions) (*Reader, error) {
	if size < minTableSize {
		return nil, ErrCorrupt
	}
	f := make([]byte, footerLen)
	if err := readAt(ctx, file, f, size-footerLen); err != nil {
		return nil, err
	}
	be := binary.BigEndian
	if be.Uint64(f[72:]) != magic || be.Uint32(f[footerChecked:]) != crc32.Checksum(f[:footerChecked], castagnoli) ||
		be.Uint32(f[64:]) != formatVersion {

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
		f: file, index: index, dataEnd: dataEnd,
		summary: Summary{
			//nolint:gosec // G115: a Writer counts its entries in an int64
			Entries: int64(be.Uint64(f[40:])), Size: size, MinSeq: be.Uint64(f[48:]), MaxSeq: be.Uint64(f[56:]),
		},
	}

	// Every entry of the index is a restart point, so the last restart point
	// is the last entry. An index of no entries has none.
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
		if rd.filter, err = filter.ParseFuse(b); err != nil {
			return nil, ErrCorrupt
		}
		rd.hasFilter = true
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

// MayContain reports whether the table may contain an entry with key. It
// reports true for every key of the table, and true for every key when the
// table has no filter. For a key that the table does not contain, a filter
// of b bits per fingerprint reports true with a probability of 2^-b.
//
// # Allocation contract
//
// Zero-alloc.
func (r *Reader) MayContain(key []byte) bool {
	return !r.hasFilter || r.filter.MayContain(xxh3.Hash(key))
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
// Reader has one, and adds the block that it reads to the cache.
//
// Error modes:
//
//   - A data block that fails its checksum, its decompression or its layout
//     returns [ErrCorrupt].
//   - A ctx that ended returns the error of ctx when Get needs a block, also
//     when the cache contains the block.
//   - An error of the File, such as the error of ctx at the end of a wait,
//     returns an error that wraps it.
//
// # Allocation contract
//
// Zero-alloc when dst has room for the value, the pool of cursors has a
// cursor whose buffers have grown to the block, and the cache contains the
// block or the Reader has no cache. A block that the cache does not contain
// allocates its copy for the cache.
func (r *Reader) Get(ctx context.Context, key []byte, seq uint64, dst []byte) ([]byte, uint64, bool, error) {
	if !r.MayContain(key) {
		return dst, 0, false, nil
	}

	it := r.NewIter(IterOptions{})
	ok := it.SeekGE(ctx, key, seq) && bytes.Equal(it.Key(), key)
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
	it.index.init(r.index)

	return it
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
