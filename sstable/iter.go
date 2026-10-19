// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"io"
	"math"
	"slices"

	"go.thesmos.sh/core/filter"
	"go.thesmos.sh/core/hash/xxh3"
	"go.thesmos.sh/core/pool"
)

// iters are the cursors that [Iter.Close] returned, with their buffers.
var iters = pool.NewPool(func() *Iter { return new(Iter) })

// IterOptions configure a cursor of [Reader.NewIter]. The zero value adds
// each block that the cursor reads to the cache of the Reader, and reads
// each block with a read of its own.
type IterOptions struct {
	// NoFill makes the cursor take the blocks that the cache contains, and
	// read every other block into a buffer of the cursor without adding it,
	// as a scan of a compaction needs. The scan then keeps the blocks of the
	// reads in the cache.
	NoFill bool

	// ReadAhead is the number of bytes of a read of the cursor from the
	// File. A read of a data block that the cache does not contain reads
	// ReadAhead bytes from the start of the block, or up to the end of the
	// block when the block is before the bytes of the last read. A later
	// move takes a block inside those bytes without a read. A ReadAhead of
	// the size of a block or less reads the block alone, and so does 0. A
	// seek outside the bytes of the last read reads ReadAhead bytes again,
	// so ReadAhead suits a scan.
	ReadAhead int
}

// Iter is a cursor over the entries of a table, in the order of [Compare].
// A move positions the cursor on an entry or exhausts it. A seek searches
// the index for its data block, reads the block and searches the restart
// points of the block. [Iter.SeekGE] of a cursor on an entry at or before
// its target searches the current block alone when the target is at or
// before the last entry of the block. [Iter.Prev] decodes the entries of the
// block from the restart point before the current entry, so it decodes at
// most RestartInterval entries. At the end of a block, Next and Prev read
// the block after it or before it. A cursor with the ReadAhead of
// [IterOptions] reads the blocks of ReadAhead bytes with one read of the
// File.
//
// A move that needs a block first checks its context. A context that ended
// exhausts the cursor, also when the cache of the Reader contains the block.
// The move then looks the block up in the cache and reads it under the same
// context. Next, Prev and SeekGE ignore the context when they move within
// the current block.
//
// The key and the value of the current entry are valid until the next move
// of the cursor. A cursor that reads through a [LendingCache] keeps a
// reference to the [Block] of its current entry. It releases the reference
// when it moves to another block or closes.
//
// A cursor of a table in partitions searches the top-level index of
// the index for the index partition of its block, and reads the partition
// through the cache as it reads a block. It keeps the partition, and a
// reference to it when a LendingCache lent it. At the end of a partition,
// Next and Prev read the partition after it or before it.
//
// # Concurrency
//
// A cursor is for one goroutine. Cursors of one [Reader] can move at once.
//
// # Allocation contract
//
// The moves of a cursor do not allocate once its buffers have grown to the
// largest block and the largest partition. A move through a [BlockCache]
// allocates the copy of each block or partition that it adds. A move through
// a LendingCache copies the block into a Block of [NewBlock] instead, which
// allocates only when the pool of its class is empty. A cursor with NoFill
// does not add a block or a partition. A cursor with ReadAhead allocates the
// buffer of its reads ahead at its first read of more than one block, and
// Close releases it. Key, Seq, Value, Err and Close do not allocate.
type Iter struct {
	// err is the error that exhausted the cursor.
	err error

	// r is the Reader of the cursor, or nil after Close.
	r *Reader

	// lent is the block of data when a LendingCache lent it or received it,
	// and nil otherwise. The cursor keeps a reference to it.
	lent *Block

	// part is the index partition of index when a LendingCache lent it or
	// received it, and nil otherwise. The cursor keeps a reference to it.
	part *Block

	// raw receives a stored block and its trailer. buf receives the decoding
	// of a compressed block.
	raw, buf []byte

	// partRaw and partBuf receive a stored partition and its trailer, and the
	// decoding of a compressed partition.
	partRaw, partBuf []byte

	// ahead contains the bytes of the last read ahead, from the offset
	// aheadOff of the table on. It is empty before the first one.
	ahead []byte

	// top is on the entry of the top-level index of the index for the index
	// partition of a table in partitions.
	top blockCursor

	// index is on the entry of the index or of the index partition for the
	// block of data.
	index blockCursor

	// data is on the current entry, or on no entry.
	data blockCursor

	// aheadOff is the offset of the bytes of ahead in the table.
	aheadOff uint64

	// partOff is the offset of the index partition of index, when index has
	// a partition.
	partOff uint64

	// readAhead is the ReadAhead of [IterOptions].
	readAhead int

	// noFill reports whether the cursor reads blocks without adding them to
	// the cache, as [IterOptions] states.
	noFill bool
}

var (
	_ Source    = (*Iter)(nil)
	_ io.Closer = (*Iter)(nil)
)

// First moves to the first entry and reports whether it exists. It reads the
// first data block under ctx.
//
// Error modes:
//
//   - A data block that fails its checksum, its decompression or its layout
//     exhausts the cursor, and Err returns [ErrCorrupt].
//   - A ctx that ended exhausts the cursor, and Err returns the error of ctx.
//   - An error of the File, such as the error of ctx at the end of a wait,
//     exhausts the cursor, and Err returns an error that wraps it.
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the block.
func (it *Iter) First(ctx context.Context) bool {
	it.err, it.top.err, it.index.err, it.data.err, it.data.valid = nil, nil, nil, nil, false
	ok := it.indexFirst(ctx) && it.load(ctx) && it.data.restart(0)

	return it.forward(ctx, ok)
}

// Last moves to the last entry and reports whether it exists. It reads the
// last data block under ctx.
//
// Error modes:
//
//   - A data block that fails its checksum, its decompression or its layout
//     exhausts the cursor, and Err returns [ErrCorrupt].
//   - A ctx that ended exhausts the cursor, and Err returns the error of ctx.
//   - An error of the File, such as the error of ctx at the end of a wait,
//     exhausts the cursor, and Err returns an error that wraps it.
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the block.
func (it *Iter) Last(ctx context.Context) bool {
	it.err, it.top.err, it.index.err, it.data.err, it.data.valid = nil, nil, nil, nil, false
	ok := it.indexLast(ctx) && it.load(ctx) && it.data.last()

	return it.backward(ctx, ok)
}

// SeekGE moves to the first entry at or after the entry (key, seq) and
// reports whether it exists. For a snapshot seq, an entry with key is the
// newest version of key at or below seq. SeekGE reads the first data block
// whose last entry is at or after (key, seq), under ctx. A cursor on an
// entry at or before (key, seq), in a block whose last entry is at or after
// (key, seq), searches the restart points of that block and reads no block.
//
// Error modes:
//
//   - A data block that fails its checksum, its decompression or its layout
//     exhausts the cursor, and Err returns [ErrCorrupt].
//   - A ctx that ended exhausts the cursor when SeekGE needs a block, and Err
//     returns the error of ctx.
//   - An error of the File, such as the error of ctx at the end of a wait,
//     exhausts the cursor, and Err returns an error that wraps it.
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the block.
func (it *Iter) SeekGE(ctx context.Context, key []byte, seq uint64) bool {
	// The key of the entry of the index is the last key of its block, so an
	// entry from the current one to that key is in the block.
	if it.data.valid && it.data.compare(key, seq) <= 0 && it.index.compare(key, seq) >= 0 {
		return it.forward(ctx, it.data.seekGE(key, seq))
	}
	it.err, it.top.err, it.index.err, it.data.err, it.data.valid = nil, nil, nil, nil, false
	ok := it.indexSeekGE(ctx, key, seq) && it.load(ctx) && it.data.seekGE(key, seq)

	return it.forward(ctx, ok)
}

// SeekLT moves to the last entry before the entry (key, seq) and reports
// whether it exists. SeekLT reads the first data block whose last entry is
// at or after (key, seq), and the block before it when every entry of the
// block is at or after (key, seq). When every entry is before (key, seq),
// SeekLT moves to the last entry. It reads each block under ctx.
//
// Error modes:
//
//   - A data block that fails its checksum, its decompression or its layout
//     exhausts the cursor, and Err returns [ErrCorrupt].
//   - A ctx that ended exhausts the cursor, and Err returns the error of ctx.
//   - An error of the File, such as the error of ctx at the end of a wait,
//     exhausts the cursor, and Err returns an error that wraps it.
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the block.
func (it *Iter) SeekLT(ctx context.Context, key []byte, seq uint64) bool {
	it.err, it.top.err, it.index.err, it.data.err, it.data.valid = nil, nil, nil, nil, false
	ok := false
	if it.indexSeekGE(ctx, key, seq) {
		ok = it.load(ctx) && it.data.seekLT(key, seq)
	} else if it.err == nil && it.top.err == nil && it.index.err == nil {
		ok = it.indexLast(ctx) && it.load(ctx) && it.data.last()
	}

	return it.backward(ctx, ok)
}

// Next moves to the next entry and reports whether it exists. It reads the
// next data block under ctx at the end of a block. Next of a cursor on no
// entry reports false.
//
// Error modes:
//
//   - A data block that fails its checksum, its decompression or its layout
//     exhausts the cursor, and Err returns [ErrCorrupt].
//   - A ctx that ended exhausts the cursor at the end of a block, and Err
//     returns the error of ctx.
//   - An error of the File, such as the error of ctx at the end of a wait,
//     exhausts the cursor, and Err returns an error that wraps it.
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the block.
func (it *Iter) Next(ctx context.Context) bool {
	// A cursor on no entry has an index on no entry or an error, so forward
	// reports false for it.
	return it.forward(ctx, it.data.next())
}

// Prev moves to the entry before the current one and reports whether it
// exists. It reads the data block before under ctx at the first entry of a
// block. Prev of a cursor on no entry reports false.
//
// Error modes:
//
//   - A data block that fails its checksum, its decompression or its layout
//     exhausts the cursor, and Err returns [ErrCorrupt]. So do entries of
//     the current block that do not end at the current entry.
//   - A ctx that ended exhausts the cursor at the first entry of a block, and
//     Err returns the error of ctx.
//   - An error of the File, such as the error of ctx at the end of a wait,
//     exhausts the cursor, and Err returns an error that wraps it.
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the block.
func (it *Iter) Prev(ctx context.Context) bool {
	// A cursor on no entry has an index on no entry or an error, so backward
	// reports false for it.
	return it.backward(ctx, it.data.prev())
}

// Key returns the key of the current entry, with a capacity of its length,
// or nil for a cursor on no entry.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Key() []byte {
	if !it.data.valid {
		return nil
	}
	n := len(it.data.key) - seqLen

	return it.data.key[:n:n]
}

// Seq returns the sequence of the current entry, or 0 for a cursor on no
// entry.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Seq() uint64 {
	if !it.data.valid {
		return 0
	}

	return binary.BigEndian.Uint64(it.data.key[len(it.data.key)-seqLen:])
}

// Value returns the value of the current entry, with a capacity of its
// length, or nil for a cursor on no entry.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Value() []byte {
	if !it.data.valid {
		return nil
	}

	return it.data.value
}

// Err returns the error that exhausted the cursor, or nil. First, Last,
// SeekGE and SeekLT clear it.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Err() error {
	return it.err
}

// Close releases the references of the cursor to a block and a partition of
// a [LendingCache], returns the cursor and its buffers to the pool of
// cursors, and returns the error of Err. The cursor is unusable after Close,
// because a later [Reader.NewIter] can take it from the pool. A second Close
// before such a NewIter returns nil and leaves the pool unchanged.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Close() error {
	if it.r == nil {
		return nil
	}
	if it.lent != nil {
		it.lent.Release()
	}
	if it.part != nil {
		it.part.Release()
	}
	err := it.err
	*it = Iter{
		top:     blockCursor{key: it.top.key[:0]},
		index:   blockCursor{key: it.index.key[:0]},
		data:    blockCursor{key: it.data.key[:0]},
		raw:     it.raw[:0],
		buf:     it.buf[:0],
		partRaw: it.partRaw[:0],
		partBuf: it.partBuf[:0],
	}
	iters.Put(it)

	return err
}

// forward ends a move whose last step in the data block reported ok. When
// the block has no entry for the move, forward moves to the first entry of
// the next data blocks, which it reads under ctx. It exhausts the cursor at
// the end of the index or at an error, and reports whether the cursor is on
// an entry. A step that reports ok leaves every error nil, so forward joins
// the errors only after a step that failed.
func (it *Iter) forward(ctx context.Context, ok bool) bool {
	for !ok && it.err == nil && it.data.err == nil && it.indexNext(ctx) {
		ok = it.load(ctx) && it.data.restart(0)
	}
	if !ok {
		it.err = cmp.Or(it.err, it.top.err, it.index.err, it.data.err)
	}

	return ok
}

// backward ends a move whose last step in the data block reported ok. When
// the block has no entry for the move, backward moves to the last entry of
// the data blocks before it, which it reads under ctx. It exhausts the
// cursor at the start of the index or at an error, and reports whether the
// cursor is on an entry. It joins the errors only after a step that failed,
// as forward does.
func (it *Iter) backward(ctx context.Context, ok bool) bool {
	for !ok && it.err == nil && it.data.err == nil && it.indexPrev(ctx) {
		ok = it.load(ctx) && it.data.last()
	}
	if !ok {
		it.err = cmp.Or(it.err, it.top.err, it.index.err, it.data.err)
	}

	return ok
}

// indexFirst moves the index cursor to the entry of the first data block. Of
// a table in partitions it moves the top-level cursor to its first
// entry and loads that partition. A move of the top-level cursor that fails
// leaves the index cursor on no entry, so the next index move fails too.
func (it *Iter) indexFirst(ctx context.Context) bool {
	if !it.r.partitioned {
		return it.index.restart(0)
	}
	it.index.valid = false

	return it.top.restart(0) && it.loadPart(ctx, it.top.value) && it.index.restart(0)
}

// indexLast moves the index cursor to the entry of the last data block,
// through the last partition of a table in partitions.
func (it *Iter) indexLast(ctx context.Context) bool {
	if !it.r.partitioned {
		return it.index.last()
	}
	it.index.valid = false

	return it.top.last() && it.loadPart(ctx, it.top.value) && it.index.last()
}

// indexSeekGE moves the index cursor to the entry of the first data block
// whose last entry is at or after the entry (key, seq), through the first
// partition whose last entry is at or after it.
func (it *Iter) indexSeekGE(ctx context.Context, key []byte, seq uint64) bool {
	if !it.r.partitioned {
		return it.index.seekGE(key, seq)
	}
	it.index.valid = false

	return it.top.seekGE(key, seq) && it.loadPart(ctx, it.top.value) && it.index.seekGE(key, seq)
}

// indexNext moves the index cursor to the entry of the next data block. At
// the end of an index partition it moves the top-level cursor to the next
// partition, loads it and moves to its first entry.
func (it *Iter) indexNext(ctx context.Context) bool {
	if it.index.next() {
		return true
	}

	return it.r.partitioned && it.index.err == nil && it.top.next() && it.loadPart(ctx, it.top.value) &&
		it.index.restart(0)
}

// indexPrev moves the index cursor to the entry of the data block before the
// current one. At the start of an index partition it moves the top-level
// cursor to the partition before, loads it and moves to its last entry.
func (it *Iter) indexPrev(ctx context.Context) bool {
	if it.index.prev() {
		return true
	}

	return it.r.partitioned && it.index.err == nil && it.top.prev() && it.loadPart(ctx, it.top.value) &&
		it.index.last()
}

// loadPart sets the index cursor on the index partition of the top-level
// value v: the place of the partition, its count of data blocks and its
// CRC32C. A partition at the offset of the partition that index has stays,
// and the cursor moves to no entry of it. Otherwise loadPart releases the
// reference to the partition before, checks ctx, and takes the partition as
// fetch takes an index partition. loadPart sets err and reports false for a
// ctx that ended, a value with other fields, a place outside the partitions
// and a read that fails. A load that fails leaves index without a partition.
func (it *Iter) loadPart(ctx context.Context, v []byte) bool {
	off, n, _, want, ok := it.r.partition(v, true)
	if !ok {
		it.err = ErrCorrupt

		return false
	}
	if it.index.block != nil && off == it.partOff {
		it.index.init(it.index.block)

		return true
	}
	if it.part != nil {
		it.part.Release()
		it.part = nil
	}
	it.index.block = nil
	if err := ctx.Err(); err != nil {
		it.err = err

		return false
	}
	b, blk, err := it.fetch(ctx, off, n, want, IndexBlock)
	if err != nil {
		it.err = err

		return false
	}
	it.part, it.partOff = blk, off
	it.index.init(b)

	return true
}

// load sets the data cursor on the data block of the current entry of the
// index. The value of the entry is the place of the block and its CRC32C.
// load takes the block as fetch takes a data block, and keeps the reference
// of a LendingCache to the block. load sets err and reports false for a ctx
// that ended, a value with other fields, a place outside the data blocks and
// a read that fails. Every caller calls load with the data cursor on no
// entry, and a load that fails leaves it there. load first releases the
// reference of the cursor to the block of its last load.
func (it *Iter) load(ctx context.Context) bool {
	// The data cursor is on no entry, so it does not read the block of the
	// last load again.
	if it.lent != nil {
		it.lent.Release()
		it.lent = nil
	}
	if err := ctx.Err(); err != nil {
		it.err = err

		return false
	}
	v := it.index.value
	off, n1 := binary.Uvarint(v)
	// When the first varint fails, Uvarint decodes the same bytes for the
	// second and fails again, so n2 reports either failure.
	n, n2 := binary.Uvarint(v[max(n1, 0):])
	end := it.r.dataEnd
	if n2 <= 0 || len(v) != n1+n2+crcLen ||
		//dokimi:mutate-skip ror-boundary: a block of end bytes leaves no room for its trailer, which the next check refuses
		n > end ||
		end-n < trailerLen || off > end-n-trailerLen {

		it.err = ErrCorrupt

		return false
	}
	want := binary.BigEndian.Uint32(v[n1+n2:])

	b, blk, err := it.fetch(ctx, off, n, want, DataBlock)
	if err != nil {
		it.err = err

		return false
	}
	it.lent = blk
	it.data.init(b)

	return true
}

// fetch returns the decoded block of n stored bytes at off whose CRC32C is
// want. role is the role of the block in the table. fetch passes it to Lend
// and AddBlock of a LendingCache. It lends the block from a LendingCache,
// and returns the lent Block with the reference for the cursor. It takes the block from another BlockCache. Otherwise it reads the
// block under ctx and compares its CRC32C with want. A data block takes the
// read ahead of the cursor and the buffers raw and buf, and a partition
// takes partRaw and partBuf. fetch adds the block that it read to the cache,
// unless the cursor has noFill. It copies the block into a Block of NewBlock
// for a LendingCache, and returns that Block with the reference for the
// cursor after AddBlock, or into a copy for another BlockCache. fetch
// returns the error of a read that fails and ErrCorrupt for a block that
// does not check.
func (it *Iter) fetch(ctx context.Context, off, n uint64, want uint32, role BlockRole) ([]byte, *Block, error) {
	c, lc := it.r.cache, it.r.lending
	if lc != nil {
		if blk, ok := lc.Lend(ctx, it.r.cacheID, off, role); ok {
			b := blk.Bytes()

			return b, blk, nil
		}
	} else if c != nil {
		if b, ok := c.Get(ctx, it.r.cacheID, off); ok {
			return b, nil, nil
		}
	}

	//nolint:gosec // G115: the block lies inside a table whose size is an int64
	size := int(n) + trailerLen
	buf := &it.partBuf
	var raw []byte
	var err error
	if role == DataBlock {
		buf = &it.buf
		raw, err = it.read(ctx, off, size)
	} else {
		it.partRaw = slices.Grow(it.partRaw[:0], size)[:size]
		//nolint:gosec // G115: the partition lies before partEnd, inside the table
		raw, err = it.partRaw, readAt(ctx, it.r.f, it.partRaw, int64(off))
	}
	if err != nil {
		return nil, nil, err
	}
	b, err := unseal(raw, want, buf)
	if err != nil {
		return nil, nil, err
	}
	if c == nil || it.noFill {
		return b, nil, nil
	}
	if lc != nil {
		blk := NewBlock(len(b))
		copy(blk.buf, b)
		lc.AddBlock(ctx, it.r.cacheID, off, role, blk)
		b = blk.Bytes()

		return b, blk, nil
	}
	b = bytes.Clone(b)
	c.Add(ctx, it.r.cacheID, off, b)

	return b, nil, nil
}

// read returns the stored block of size bytes at off and its trailer, which
// it reads from the File under ctx. A block inside the bytes of the last read
// ahead is a part of them, and read does not read. With readAhead above size
// read reads readAhead bytes from off into ahead, or up to the end of the
// block when the block is before the bytes of the last read, within the data
// blocks. Otherwise it reads the block alone into raw. A read that fails
// returns the error of readAt, and leaves ahead empty at the offset 0, as a
// cursor without a read ahead has it.
func (it *Iter) read(ctx context.Context, off uint64, size int) ([]byte, error) {
	//nolint:gosec // G115: size is the positive length of a block and its trailer
	end := off + uint64(size)
	if off >= it.aheadOff && end <= it.aheadOff+uint64(len(it.ahead)) {
		return it.ahead[off-it.aheadOff : end-it.aheadOff], nil
	}
	//dokimi:mutate-skip ror-boundary: a read ahead of the size of the block reads the bytes of the block alone
	if it.readAhead <= size {
		it.raw = slices.Grow(it.raw[:0], size)[:size]

		//nolint:gosec // G115: the block lies before dataEnd, inside the table
		return it.raw, readAt(ctx, it.r.f, it.raw, int64(off))
	}

	//nolint:gosec // G115: readAhead is above size, which is positive
	n := uint64(it.readAhead)
	start := off
	// A block at aheadOff reaches this check only with ahead empty at the
	// offset 0, where a read in either direction starts at 0.
	//dokimi:mutate-skip ror-boundary: a block at aheadOff outside the bytes ahead is at the offset 0, where both reads start
	if off < it.aheadOff {
		start = end - min(end, n)
	}
	//nolint:gosec // G115: the bytes are at most readAhead, an int
	w := int(min(start+n, it.r.dataEnd) - start)
	it.ahead = slices.Grow(it.ahead[:0], w)[:w]
	//nolint:gosec // G115: the bytes lie before dataEnd, inside the table
	if err := readAt(ctx, it.r.f, it.ahead, int64(start)); err != nil {
		it.ahead, it.aheadOff = it.ahead[:0], 0

		return nil, err
	}
	it.aheadOff = start

	return it.ahead[off-start : end-start], nil
}

// mayContain reports whether a table in partitions may contain key by the
// filter whose top-level index is index, for [Reader.MayContain],
// [Reader.MayContainPrefix] and [Reader.Get]. It checks ctx, searches the
// top-level index for the first partition whose last key is at or after
// key, and takes the partition as fetch takes a filter partition. A key
// after the last key of the filter has no partition, and the table does not
// contain it. mayContain releases a partition that a LendingCache lent. It
// returns the error of ctx and of a read, and ErrCorrupt for an entry of the
// top-level index or a partition that does not check. Open checks the
// restart offsets of index.
func (it *Iter) mayContain(ctx context.Context, index, key []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var top blockCursor
	top.init(index)
	i, ok := top.search(0, top.count, key, math.MaxUint64)
	if !ok {
		return false, ErrCorrupt
	}
	if i == top.count {
		return false, nil
	}
	off, n, _, want, ok := it.r.partition(top.restartValue(i), false)
	if !ok {
		return false, ErrCorrupt
	}
	b, blk, err := it.fetch(ctx, off, n, want, FilterBlock)
	if err != nil {
		return false, err
	}
	f, err := filter.ParseFuse(b)
	may := err == nil && f.MayContain(xxh3.Hash(key))
	if blk != nil {
		blk.Release()
	}
	if err != nil {
		return false, ErrCorrupt
	}

	return may, nil
}

// span returns the [Span] of the range from lower to upper of a table in
// partitions, for [Reader.Span]. It searches the top-level index of the
// index in halves for the partition of lower, and from there in steps that
// double for the partition of upper. It then reads the two partitions under
// ctx and searches them for the first block and the last block. The
// top-level entry before a partition counts the data blocks before the
// partition. span returns the error of ctx and of a read, and ErrCorrupt for
// an entry or a partition that does not check.
func (it *Iter) span(ctx context.Context, lower, upper []byte) (Span, error) {
	top := &it.top
	first, ok := 0, true
	if len(lower) != 0 {
		first, ok = top.search(0, top.count, lower, math.MaxUint64)
	}
	after := top.count
	if ok && upper != nil {
		after, ok = top.gallop(first, upper, math.MaxUint64)
	}
	if !ok {
		return Span{}, ErrCorrupt
	}
	if first == top.count {
		return Span{}, nil
	}

	lo, start, _, err := it.partBlock(ctx, first, lower)
	if err != nil {
		return Span{}, err
	}
	var last int
	var end uint64
	if after < top.count {
		var off, n uint64
		last, off, n, err = it.partBlock(ctx, after, upper)
		end = off + n + trailerLen
	} else {
		last, end = it.blocks(top.count-1)-1, it.r.dataEnd
	}
	if err != nil {
		return Span{}, err
	}
	if last < lo || end <= start {
		return Span{}, ErrCorrupt
	}

	//nolint:gosec // G115: end is at most dataEnd, inside a table whose size is an int64
	return Span{Blocks: last - lo + 1, Bytes: int64(end - start)}, nil
}

// partBlock returns the number in the table of the first data block whose
// last key is at or after key, among the blocks of the partition of the
// top-level entry p, and the offset and the length of the block. An empty
// key takes the first block of the partition. partBlock loads the partition
// under ctx, as loadPart loads it. It returns the error of the load, and
// ErrCorrupt for an entry or a partition that does not check and for a key
// after the last block of the partition.
func (it *Iter) partBlock(ctx context.Context, p int, key []byte) (int, uint64, uint64, error) {
	before := 0
	if p > 0 {
		before = it.blocks(p - 1)
	}
	if !it.loadPart(ctx, it.top.restartValue(p)) {
		return 0, 0, 0, it.err
	}
	i, ok := 0, true
	if len(key) != 0 {
		i, ok = it.index.search(0, it.index.count, key, math.MaxUint64)
	}
	if !ok || i == it.index.count {
		return 0, 0, 0, ErrCorrupt
	}
	off, n, ok := it.r.place(it.index.restartValue(i))
	if !ok || before < 0 {
		return 0, 0, 0, ErrCorrupt
	}

	return before + i, off, n, nil
}

// blocks returns the number of data blocks of the table up to the end of the
// partition of the top-level entry p, from the value of the entry, or -1 for
// an entry or a value that does not decode.
func (it *Iter) blocks(p int) int {
	_, _, n, _, ok := it.r.partition(it.top.restartValue(p), true)
	if !ok {
		return -1
	}

	//nolint:gosec // G115: a count above MaxInt converts to a negative count, which the callers refuse
	return int(n)
}

// blockCursor is a cursor over the entries of a decoded block. The block
// contains the entries, then the offset of each restart point as a uint32,
// then their count as a uint32. The key at a restart point is whole. Every
// other key shares a prefix with the key before it.
type blockCursor struct {
	// err is ErrCorrupt once the cursor found a malformed block or entry.
	err error

	// block contains the bytes of the block.
	block []byte

	// key is the internal key of the current entry. It is the key followed
	// by the sequence in 8 bytes.
	key []byte

	// value is the value of the current entry, with a capacity of its
	// length.
	value []byte

	// end is the offset of the first restart offset. The entries end there.
	end int

	// count is the number of restart points.
	count int

	// at is the offset of the current entry.
	at int

	// after is the offset of the entry after the current one.
	after int

	// valid reports whether the cursor is on an entry.
	valid bool
}

// init sets the cursor on the block b and clears err. The cursor is on no
// entry until a move. A block without room for its count and its restart
// offsets leaves the cursor without entries and sets err to ErrCorrupt.
func (c *blockCursor) init(b []byte) {
	n := len(b)
	c.block, c.end, c.count, c.valid, c.err = b, 0, 0, false, nil
	if n < 4 || uint64(binary.BigEndian.Uint32(b[n-4:])) > uint64(n-4)/4 {
		c.err = ErrCorrupt

		return
	}
	c.count = int(binary.BigEndian.Uint32(b[n-4:]))
	c.end = n - 4 - 4*c.count
}

// restart moves to the entry at the restart point i. For an i outside the
// restart points it reports false and leaves err unchanged. For a restart
// offset outside the entries it sets err to ErrCorrupt and reports false.
func (c *blockCursor) restart(i int) bool {
	c.valid = false
	if uint(i) >= uint(c.count) {
		return false
	}
	off := binary.BigEndian.Uint32(c.block[c.end+4*i:])
	//dokimi:mutate-skip ror-boundary: an entry at the end of the entries has no bytes, which decode refuses
	if int64(off) >= int64(c.end) {
		c.err = ErrCorrupt

		return false
	}
	c.key = c.key[:0]

	return c.decode(int(off))
}

// last moves to the last entry. It decodes the entries from the last restart
// point to the end of the entries. For a block without restart points it
// reports false and leaves err unchanged.
func (c *blockCursor) last() bool {
	ok := c.restart(c.count - 1)
	for ok && c.after != c.end {
		ok = c.next()
	}

	return ok
}

// next moves to the entry after the current one. After the last entry and
// for a cursor on no entry, it reports false and leaves err unchanged.
func (c *blockCursor) next() bool {
	if !c.valid || c.after == c.end {
		c.valid = false

		return false
	}

	return c.decode(c.after)
}

// prev moves to the entry before the current one. It searches the restart
// points for the last one before the current entry, and decodes the entries
// from there to the entry that ends at the current entry. At the first entry
// and for a cursor on no entry, it reports false and leaves err unchanged.
// When no entry ends at the current entry, it sets err to ErrCorrupt and
// reports false.
func (c *blockCursor) prev() bool {
	if !c.valid {
		return false
	}
	at := c.at
	lo, hi := 0, c.count
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if int64(binary.BigEndian.Uint32(c.block[c.end+4*mid:])) < int64(at) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}

	// At the first entry no restart point is before the current entry, and
	// restart reports false for the restart point -1.
	ok := c.restart(lo - 1)
	for ok && c.after < at {
		ok = c.next()
	}
	if ok && c.after != at {
		c.valid, c.err = false, ErrCorrupt

		return false
	}

	return ok
}

// seekGE moves to the first entry at or after the entry (key, seq). It
// searches the restart points for the first one at or after the entry, and
// moves along the entries from the restart point before it. When every
// entry is before (key, seq), it reports false and leaves err unchanged.
func (c *blockCursor) seekGE(key []byte, seq uint64) bool {
	lo, hi := 0, c.count
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		order, ok := c.restartCompare(mid, key, seq)
		if !ok {
			return false
		}
		if order < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}

	ok := c.restart(max(lo-1, 0))
	for ok && c.compare(key, seq) < 0 {
		ok = c.next()
	}

	return ok
}

// seekLT moves to the last entry before the entry (key, seq). It moves to
// the first entry at or after the entry and then back by one, or to the last
// entry when every entry is before (key, seq). When every entry is at or
// after (key, seq), it reports false and leaves err unchanged.
func (c *blockCursor) seekLT(key []byte, seq uint64) bool {
	if c.seekGE(key, seq) {
		return c.prev()
	}
	if c.err != nil {
		return false
	}

	return c.last()
}

// search returns the number of the first restart point at or after the
// entry (key, seq) among the restart points lo to hi-1, or hi when each of
// them is before the entry. It does not move the cursor. It reports false
// for a restart point that restartCompare refuses. restartCompare sets err
// for that restart point.
func (c *blockCursor) search(lo, hi int, key []byte, seq uint64) (int, bool) {
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		order, ok := c.restartCompare(mid, key, seq)
		if !ok {
			return 0, false
		}
		if order < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}

	return lo, true
}

// gallop returns what search returns for the restart points from lo to the
// last one. It compares the restart points lo, lo+1, lo+3, lo+7 and on, with
// a step that doubles, until one is at or after the entry (key, seq). It then
// searches the gap before that restart point. A result d places after lo
// takes about 2·log2(d+2) comparisons.
func (c *blockCursor) gallop(lo int, key []byte, seq uint64) (int, bool) {
	hi, step := lo, 1
	for hi < c.count {
		order, ok := c.restartCompare(hi, key, seq)
		if !ok {
			return 0, false
		}
		if order >= 0 {
			return c.search(lo, hi, key, seq)
		}
		//dokimi:mutate-skip aor,sbr-delete: a gap that starts lower adds comparisons and keeps the result
		lo = hi + 1
		hi += step
		//dokimi:mutate-skip aor,sbr-delete: another growth of the step moves the comparisons and keeps the result
		step *= 2
	}

	return c.search(lo, c.count, key, seq)
}

// restartCompare returns the order of the entry at the restart point i and
// the entry (key, seq) under Compare. It compares the whole key of the
// restart point where the block stores it, without a copy into c.key. It
// sets err to ErrCorrupt and reports false for a restart offset outside the
// entries, and for an entry at the offset whose header does not decode,
// whose internal key does not fit the entries or is shorter than a sequence,
// or that shares a prefix with a key before it.
func (c *blockCursor) restartCompare(i int, key []byte, seq uint64) (int, bool) {
	off := binary.BigEndian.Uint32(c.block[c.end+4*i:])
	//dokimi:mutate-skip ror-boundary: an entry at the end of the entries has no bytes, which uvarints refuses
	if int64(off) >= int64(c.end) {
		c.valid, c.err = false, ErrCorrupt

		return 0, false
	}
	p := c.block[off:c.end]
	shared, unshared, _, short := shortHeader(p)
	n := 3
	if !short {
		shared, unshared, _, n = uvarints(p)
	}
	// A header that does not decode has a key of no bytes, which is shorter
	// than a sequence. The search reads the key alone, so it does not check
	// the length of the value.
	p = p[n:]
	if shared != 0 || unshared > uint64(len(p)) || unshared < seqLen {
		c.valid, c.err = false, ErrCorrupt

		return 0, false
	}
	//nolint:gosec // G115: unshared is at most len(p)
	k := int(unshared) - seqLen

	return Compare(p[:k], binary.BigEndian.Uint64(p[k:]), key, seq), true
}

// restartValue returns the value of the entry at the restart point i as a
// part of the block, or nil for a value that does not fit the entries. A
// restart offset or a key past the entries leaves no bytes for the value,
// and a header that does not decode has a value of no bytes. i is a restart
// point of the block.
func (c *blockCursor) restartValue(i int) []byte {
	p := c.block[min(int64(binary.BigEndian.Uint32(c.block[c.end+4*i:])), int64(c.end)):c.end]
	_, unshared, vlen, short := shortHeader(p)
	n := 3
	if !short {
		_, unshared, vlen, n = uvarints(p)
	}
	p = p[n:]
	p = p[min(unshared, uint64(len(p))):]
	if vlen > uint64(len(p)) {
		return nil
	}

	return p[:vlen:vlen]
}

// decode moves to the entry at off. The key of the entry shares its first
// bytes with c.key. decode sets err to ErrCorrupt and reports false for a
// varint that does not end, a shared prefix longer than the key before it,
// an entry past the end of the entries, or an internal key shorter than a
// sequence.
func (c *blockCursor) decode(off int) bool {
	p := c.block[off:c.end]
	shared, unshared, vlen, short := shortHeader(p)
	n := 3
	if !short {
		shared, unshared, vlen, n = uvarints(p)
	}
	// A header that does not decode has an internal key of no bytes, which
	// is shorter than a sequence.
	p = p[n:]
	if shared > uint64(len(c.key)) || unshared > uint64(len(p)) ||
		vlen > uint64(len(p))-unshared || shared+unshared < seqLen {

		c.valid, c.err = false, ErrCorrupt

		return false
	}

	//nolint:gosec // G115: both lengths are at most len(p)
	k, v := int(unshared), int(vlen)
	c.key = append(c.key[:shared], p[:k]...)
	c.value = p[k : k+v : k+v]
	c.at, c.after = off, c.end-len(p)+k+v
	c.valid = true

	return true
}

// compare returns the order of the current entry and the entry (key, seq)
// under Compare.
func (c *blockCursor) compare(key []byte, seq uint64) int {
	n := len(c.key) - seqLen

	return Compare(c.key[:n], binary.BigEndian.Uint64(c.key[n:]), key, seq)
}

// shortHeader decodes the header of the entry at the start of p when the
// header has three bytes: the length of the prefix that the key shares with
// the key before it, the length of the rest of the key and the length of the
// value, each a varint of one byte below 0x80. It reports false for a longer
// header, which uvarints decodes. shortHeader has no loop and no call, so
// the compiler inlines it into the decode of each entry.
func shortHeader(p []byte) (shared, unshared, vlen uint64, ok bool) {
	//dokimi:mutate-skip ror-boundary,ror-true,lcr-true: uvarints decodes a header of three bytes below 0x80 to the same lengths, so the long path changes only the time
	if len(p) < 3 || (p[0]|p[1]|p[2])&0x80 != 0 {
		return 0, 0, 0, false
	}

	//dokimi:mutate-skip sbr-zero: uvarints decodes the header to the same lengths, so the long path changes only the time
	return uint64(p[0]), uint64(p[1]), uint64(p[2]), true
}

// uvarints decodes the header of the entry at the start of p as three
// varints of any length. It returns the three lengths that shortHeader
// returns, and the length of the header, or 0 for a varint that does not end.
func uvarints(p []byte) (shared, unshared, vlen uint64, n int) {
	shared, n1 := binary.Uvarint(p)
	// The second varint starts at the first one when the first one fails,
	// and fails too, and so does the third. n3 reports a failure of any of
	// the three.
	unshared, n2 := binary.Uvarint(p[max(n1, 0):])
	vlen, n3 := binary.Uvarint(p[max(n1, 0)+max(n2, 0):])
	if n3 <= 0 {
		return 0, 0, 0, 0
	}

	return shared, unshared, vlen, n1 + n2 + n3
}
