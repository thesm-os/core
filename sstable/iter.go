// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"slices"

	"go.thesmos.sh/core/pool"
)

// iters are the cursors that [Iter.Close] returned, with their buffers.
var iters = pool.NewPool(func() *Iter { return new(Iter) })

// Iter is a cursor over the entries of a table, in the order of [Compare].
// A move positions the cursor on an entry or exhausts it. A move searches
// the index for its data block, reads the block and searches the restart
// points of the block.
//
// The key and the value of the current entry are valid until the next move
// of the cursor.
//
// # Concurrency
//
// A cursor is for one goroutine. Cursors of one [Reader] can move at once.
//
// # Allocation contract
//
// The moves of a cursor do not allocate once its buffers have grown to the
// largest block. A move through the cache of the Reader allocates the copy
// of each block that it adds. Key, Seq, Value, Err and Close do not
// allocate.
type Iter struct {
	// err is the error that exhausted the cursor.
	err error

	// r is the Reader of the cursor, or nil after Close.
	r *Reader

	// raw receives a stored block and its trailer. buf receives the decoding
	// of a compressed block.
	raw, buf []byte

	// index is on the entry of the index for the block of data.
	index blockCursor

	// data is on the current entry, or on no entry.
	data blockCursor
}

// First moves to the first entry and reports whether it exists.
//
// Error modes:
//
//   - A data block that fails its checksum, its decompression or its layout
//     exhausts the cursor, and Err returns [ErrCorrupt].
//   - An error of the io.ReaderAt exhausts the cursor, and Err returns an
//     error that wraps it.
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the block.
func (it *Iter) First() bool {
	it.err, it.index.err, it.data.err, it.data.valid = nil, nil, nil, false
	ok := it.index.restart(0) && it.load() && it.data.restart(0)

	return it.forward(ok)
}

// SeekGE moves to the first entry at or after the entry (key, seq) and
// reports whether it exists. For a snapshot seq, an entry with key is the
// newest version of key at or below seq. SeekGE reads the first data block
// whose last entry is at or after (key, seq).
//
// Error modes:
//
//   - A data block that fails its checksum, its decompression or its layout
//     exhausts the cursor, and Err returns [ErrCorrupt].
//   - An error of the io.ReaderAt exhausts the cursor, and Err returns an
//     error that wraps it.
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the block.
func (it *Iter) SeekGE(key []byte, seq uint64) bool {
	it.err, it.index.err, it.data.err, it.data.valid = nil, nil, nil, false
	ok := it.index.seekGE(key, seq) && it.load() && it.data.seekGE(key, seq)

	return it.forward(ok)
}

// Next moves to the next entry and reports whether it exists. It reads the
// next data block at the end of a block. Next of a cursor on no entry
// reports false.
//
// Error modes:
//
//   - A data block that fails its checksum, its decompression or its layout
//     exhausts the cursor, and Err returns [ErrCorrupt].
//   - An error of the io.ReaderAt exhausts the cursor, and Err returns an
//     error that wraps it.
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the block.
func (it *Iter) Next() bool {
	// A cursor on no entry has an index on no entry or an error, so forward
	// reports false for it.
	return it.forward(it.data.next())
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

// Err returns the error that exhausted the cursor, or nil. First and SeekGE
// clear it.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Err() error {
	return it.err
}

// Close returns the cursor and its buffers to the pool of cursors, and
// returns the error of Err. The cursor is unusable after Close.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Close() error {
	err := it.err
	*it = Iter{
		index: blockCursor{key: it.index.key[:0]},
		data:  blockCursor{key: it.data.key[:0]},
		raw:   it.raw[:0],
		buf:   it.buf[:0],
	}
	iters.Put(it)

	return err
}

// forward ends a move whose last step in the data block reported ok. When
// the block has no entry for the move, forward moves to the first entry of
// the next data blocks. It exhausts the cursor at the end of the index or at
// an error, and reports whether the cursor is on an entry.
func (it *Iter) forward(ok bool) bool {
	for !ok && it.err == nil && it.data.err == nil && it.index.next() {
		ok = it.load() && it.data.restart(0)
	}
	it.err = cmp.Or(it.err, it.index.err, it.data.err)

	return ok
}

// load sets the data cursor on the data block of the current entry of the
// index. It takes the block from the cache of the Reader, or reads it and
// adds a copy to the cache. It sets err and reports false for a place
// outside the data blocks and for a read that fails. Every caller calls load
// with the data cursor on no entry, and a load that fails leaves it there.
func (it *Iter) load() bool {
	v := it.index.value
	off, n1 := binary.Uvarint(v)
	// When the first varint fails, Uvarint decodes the same bytes for the
	// second and fails again, so n2 reports either failure.
	n, n2 := binary.Uvarint(v[max(n1, 0):])
	end := it.r.dataEnd
	if n2 <= 0 ||
		//dokimi:mutate-skip ror-boundary: a block of end bytes leaves no room for its trailer, which the next check refuses
		n > end ||
		end-n < trailerLen || off > end-n-trailerLen {

		it.err = ErrCorrupt

		return false
	}

	c := it.r.cache
	if c != nil {
		if b, ok := c.Get(it.r.cacheID, off); ok {
			it.data.init(b)

			return true
		}
	}

	//nolint:gosec // G115: the block lies before dataEnd, inside a table whose size is an int64
	size := int(n) + trailerLen
	it.raw = slices.Grow(it.raw[:0], size)[:size]
	//nolint:gosec // G115: the block lies before dataEnd, inside the table
	if err := readAt(it.r.r, it.raw, int64(off)); err != nil {
		it.err = err

		return false
	}
	b, err := unseal(it.raw, &it.buf)
	if err != nil {
		it.err = err

		return false
	}
	if c != nil {
		b = bytes.Clone(b)
		c.Add(it.r.cacheID, off, b)
	}
	it.data.init(b)

	return true
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

// next moves to the entry after the current one. After the last entry and
// for a cursor on no entry, it reports false and leaves err unchanged.
func (c *blockCursor) next() bool {
	if !c.valid || c.after == c.end {
		c.valid = false

		return false
	}

	return c.decode(c.after)
}

// seekGE moves to the first entry at or after the entry (key, seq). It
// searches the restart points for the first one at or after the entry, and
// moves along the entries from the restart point before it. When every
// entry is before (key, seq), it reports false and leaves err unchanged.
func (c *blockCursor) seekGE(key []byte, seq uint64) bool {
	lo, hi := 0, c.count
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if !c.restart(mid) {
			return false
		}
		if c.compare(key, seq) < 0 {
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

// decode moves to the entry at off. The key of the entry shares its first
// bytes with c.key. decode sets err to ErrCorrupt and reports false for a
// varint that does not end, a shared prefix longer than the key before it,
// an entry past the end of the entries, or an internal key shorter than a
// sequence.
func (c *blockCursor) decode(off int) bool {
	p := c.block[off:c.end]
	shared, n1 := binary.Uvarint(p)
	p = p[max(n1, 0):]
	unshared, n2 := binary.Uvarint(p)
	p = p[max(n2, 0):]
	vlen, n3 := binary.Uvarint(p)
	p = p[max(n3, 0):]
	// p does not advance past a varint that fails, so every varint after it
	// fails too. n3 reports a failure of any of the three.
	if n3 <= 0 || shared > uint64(len(c.key)) || unshared > uint64(len(p)) ||
		vlen > uint64(len(p))-unshared || shared+unshared < seqLen {

		c.valid, c.err = false, ErrCorrupt

		return false
	}

	//nolint:gosec // G115: both lengths are at most len(p)
	k, v := int(unshared), int(vlen)
	c.key = append(c.key[:shared], p[:k]...)
	c.value = p[k : k+v : k+v]
	c.after = c.end - len(p) + k + v
	c.valid = true

	return true
}

// compare returns the order of the current entry and the entry (key, seq)
// under Compare.
func (c *blockCursor) compare(key []byte, seq uint64) int {
	n := len(c.key) - seqLen

	return Compare(c.key[:n], binary.BigEndian.Uint64(c.key[n:]), key, seq)
}
