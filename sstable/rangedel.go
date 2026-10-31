// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"slices"

	"go.thesmos.sh/core/pool"
)

// rangeIters are the cursors of fragments that [RangeDeleteIter.Close]
// returned, with their buffers.
var rangeIters = pool.NewPool(func() *RangeDeleteIter { return new(RangeDeleteIter) })

// RangeDeleter receives the fragments of range tombstones in the order of a
// table. A fragment covers the keys from its start up to its end, which the
// fragment excludes, at a sequence. A call follows the call before it when it
// has the same start and the same end with a lower sequence, or when its
// start is at or after the end before it. The fragments of a table then do
// not overlap. The slices of a call are valid until the call returns.
//
// Under a width of versions, the start of a call ends with the version of
// the fragment and the end has no version. A call then follows the call
// before it when it has the same start without its version and the same
// end, and a lower version or the same version with a lower sequence. It
// also follows when its start without its version is at or after the end
// before it.
//
// A [*Writer] is a RangeDeleter, and a [Fragmenter] passes its fragments to
// one. The sequence and the version are opaque to the package, as the
// sequence of an entry is. The engine applies each fragment to the entries
// that it covers.
type RangeDeleter interface {
	// AddRangeDelete adds the fragment of the keys from start up to end,
	// which the fragment excludes, at the sequence seq.
	AddRangeDelete(ctx context.Context, start, end []byte, seq uint64) error
}

// RangeDeleteIter is a cursor over the fragments of the range tombstones of
// a table, which [Reader.RangeDeletes] returns. It yields the fragments in
// the order of their starts. Each sequence of a fragment has a position of its
// own, from the highest sequence to the lowest. The positions of a table with
// versions follow one another by version and then by sequence, from the
// highest to the lowest. The fragments do not overlap, so the positions of
// one fragment have one end. No other fragment has that end.
//
// A Reader of a table without fragment partitions keeps the block of
// fragments in memory, and [Open] checks every entry of the block. A move of
// its cursor does not read a block, ignores its context and cannot fail. A
// cursor of a table with fragment partitions searches the top-level index of
// the fragments in memory, and reads the partition of a position through the
// cache of the Reader under [FragmentBlock]. A move that needs a partition
// first checks its context, as a move of an [Iter] does. It checks every
// entry of a partition that it reads from the File before it adds the
// partition to the cache. A move that fails reports false, and
// [RangeDeleteIter.Err] returns its error. The positions of one fragment are
// in one partition.
//
// A move off either end leaves the cursor on no position, and Next and Prev
// report false there. The start and the end of the current position are
// valid until the next move.
//
// # Concurrency
//
// A cursor is for one goroutine. Cursors of one [Reader] can move at once.
//
// # Allocation contract
//
// The moves, Start, End, Seq, Err and Close do not allocate once the buffers
// of the cursor have grown to the largest fragment partition.
// Reader.RangeDeletes takes the cursor from a pool, which allocates one when
// it is empty. A move through a [BlockCache] allocates the copy of a
// partition that the cache does not contain, and a move through a
// [LendingCache] takes a [Block] from a pool in place of that copy.
type RangeDeleteIter struct {
	// err is the error that exhausted the cursor.
	err error

	// r is the Reader of the cursor, or nil after Close.
	r *Reader

	// lent is the fragment partition of c when a LendingCache lent it or
	// received it, and nil otherwise. The cursor keeps a reference to it.
	lent *Block

	// raw receives a stored fragment partition and its trailer, and buf the
	// decoding of a compressed one.
	raw, buf []byte

	// key receives the key of the search of SeekLT: a key without its
	// version, followed by the highest version.
	key []byte

	// last receives the internal key of the entry before the current one
	// while checkPartition checks a partition that the cursor read.
	last []byte

	// top is on the top-level index of the fragments of a table with
	// fragment partitions.
	top blockCursor

	// c is on the entry of the current position, in the block of fragments
	// or in the partition of the top-level entry part. Every entry is a
	// restart point.
	c blockCursor

	// check decodes the entries of a partition that the cursor read, before
	// the cursor uses the partition.
	check blockCursor

	// part is the top-level entry of the partition of c, or -1 when c has no
	// partition.
	part int

	// i is the restart point of the current position in c.
	i int
}

// First moves to the first position and reports whether it exists. It reads
// the first fragment partition under ctx.
//
// Error modes:
//
//   - A ctx that ended before the read of a partition exhausts the cursor,
//     and Err returns the error of ctx.
//   - A fragment partition that fails its checksum or its layout exhausts
//     the cursor, and Err returns [ErrCorrupt].
//   - An error of the File exhausts the cursor, and Err returns an error that
//     wraps it.
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the partition.
func (it *RangeDeleteIter) First(ctx context.Context) bool {
	it.err = nil

	return it.moveTo(ctx, 0, 0)
}

// Last moves to the last position and reports whether it exists. It reads
// the last fragment partition under ctx. Its error modes are those of
// [RangeDeleteIter.First].
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the partition.
func (it *RangeDeleteIter) Last(ctx context.Context) bool {
	it.err = nil

	return it.moveTo(ctx, it.top.count-1, -1)
}

// SeekGE moves to the first position of the first fragment whose end is
// after key: the fragment that covers key, or the first fragment after key.
// It reports whether such a fragment exists. The ends of the fragments rise
// with their starts, so SeekGE searches the ends in halves. Of a table with
// versions it compares the ends with key without its version. Of a table
// with fragment partitions it searches the top-level index for the first
// partition whose last end is after key, and reads that partition under ctx.
//
// Error modes: an entry of the top-level index that does not decode
// exhausts the cursor, and Err returns [ErrCorrupt]. The other error modes
// are those of [RangeDeleteIter.First].
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the partition.
func (it *RangeDeleteIter) SeekGE(ctx context.Context, key []byte) bool {
	it.err, it.c.valid = nil, false
	user := key[:max(len(key)-it.c.suffix, 0)]
	if it.r.fragTop != nil {
		lo, hi := 0, it.top.count
		for lo < hi {
			mid := int(uint(lo+hi) >> 1)
			order, ok := it.top.restartCompare(mid, user, 0)
			if !ok {
				it.err = ErrCorrupt

				return false
			}
			if order <= 0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo == it.top.count || !it.load(ctx, lo) {
			return false
		}
	}
	lo, hi := 0, it.c.count
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if bytes.Compare(it.c.restartValue(mid), user) <= 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	it.i = lo

	return it.c.restart(lo)
}

// SeekLT moves to the last position of the last fragment whose start is
// before key, and reports whether such a fragment exists. Of a table with
// versions it compares the starts and key without their versions. Of a table
// with fragment partitions it searches the top-level index for the first
// partition whose last end is at or after key, or takes the last partition.
// The fragment is in that partition, or it is the last fragment of the
// partition before it, so SeekLT reads at most both under ctx. Its error
// modes are those of [RangeDeleteIter.SeekGE].
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the partitions and
// for key.
func (it *RangeDeleteIter) SeekLT(ctx context.Context, key []byte) bool {
	it.err, it.c.valid = nil, false
	w := it.c.suffix
	user := key[:max(len(key)-w, 0)]
	p := 0
	if it.r.fragTop != nil {
		var ok bool
		if p, ok = it.top.search(0, it.top.count, user, math.MaxUint64); !ok {
			it.err = ErrCorrupt

			return false
		}
		if p = min(p, it.top.count-1); !it.load(ctx, p) {
			return false
		}
	}

	// The key without its version at the highest version and the highest
	// sequence is at or before every position whose start without its
	// version is at or after it. A version of w bytes of 0xff is the highest
	// version. load and Open check every entry of the block, so the search
	// decodes each entry that it compares.
	it.key = append(it.key[:0], user...)
	for range w {
		it.key = append(it.key, math.MaxUint8)
	}
	i, _ := it.c.search(0, it.c.count, it.key, math.MaxUint64)
	if i == 0 && p > 0 {
		// Every fragment of the partition starts at or after key, so the last
		// fragment before key is the last fragment of the partition before.
		return it.moveTo(ctx, p-1, -1)
	}
	it.i = i - 1

	return it.c.restart(it.i)
}

// Next moves to the next position and reports whether it exists. At the end
// of a fragment partition it reads the next partition under ctx. Next of a
// cursor on no position reports false. Its error modes are those of
// [RangeDeleteIter.First].
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the partition.
func (it *RangeDeleteIter) Next(ctx context.Context) bool {
	if !it.c.valid {
		return false
	}
	it.i++
	if it.c.restart(it.i) {
		return true
	}
	if it.part+1 >= it.top.count {
		return false
	}

	return it.moveTo(ctx, it.part+1, 0)
}

// Prev moves to the position before the current one and reports whether it
// exists. At the start of a fragment partition it reads the partition before
// under ctx. Prev of a cursor on no position reports false. Its error modes
// are those of [RangeDeleteIter.First].
//
// # Allocation contract
//
// Zero-alloc once the buffers of the cursor have room for the partition.
func (it *RangeDeleteIter) Prev(ctx context.Context) bool {
	if !it.c.valid {
		return false
	}
	it.i--
	if it.c.restart(it.i) {
		return true
	}
	if it.part <= 0 {
		return false
	}

	return it.moveTo(ctx, it.part-1, -1)
}

// Start returns the start of the fragment of the current position, with a
// capacity of its length, or nil for a cursor on no position. The start of a
// table with versions ends with the version of the position.
//
// # Allocation contract
//
// Zero-alloc.
func (it *RangeDeleteIter) Start() []byte {
	if !it.c.valid {
		return nil
	}
	n := len(it.c.key) - seqLen

	return it.c.key[:n:n]
}

// End returns the end of the fragment of the current position, which the
// fragment excludes, with a capacity of its length, or nil for a cursor on no
// position.
//
// # Allocation contract
//
// Zero-alloc.
func (it *RangeDeleteIter) End() []byte {
	if !it.c.valid {
		return nil
	}

	return it.c.value
}

// Seq returns the sequence of the current position, or 0 for a cursor on no
// position.
//
// # Allocation contract
//
// Zero-alloc.
func (it *RangeDeleteIter) Seq() uint64 {
	if !it.c.valid {
		return 0
	}

	return binary.BigEndian.Uint64(it.c.key[len(it.c.key)-seqLen:])
}

// Err returns the error that exhausted the cursor, or nil. First, Last,
// SeekGE and SeekLT clear it. A cursor of a table without fragment
// partitions has no error.
//
// # Allocation contract
//
// Zero-alloc.
func (it *RangeDeleteIter) Err() error {
	return it.err
}

// Close releases the reference of the cursor to a fragment partition of a
// [LendingCache], and returns the cursor and its buffers to the pool of
// cursors. The cursor is unusable after Close, because a later
// [Reader.RangeDeletes] can take it from the pool. A second Close before
// such a RangeDeletes leaves the pool unchanged.
//
// # Allocation contract
//
// Zero-alloc.
func (it *RangeDeleteIter) Close() {
	if it.r == nil {
		return
	}
	if it.lent != nil {
		it.lent.Release()
	}
	*it = RangeDeleteIter{
		raw:   it.raw[:0],
		buf:   it.buf[:0],
		key:   it.key[:0],
		last:  it.last[:0],
		top:   blockCursor{key: it.top.key[:0]},
		c:     blockCursor{key: it.c.key[:0]},
		check: blockCursor{key: it.check.key[:0]},
	}
	rangeIters.Put(it)
}

// moveTo moves to the position i of the fragment partition of the top-level
// entry p, and to the last position of the partition for an i of -1. It
// loads the partition as load loads it. A cursor of a table without fragment
// partitions moves to the position i of the block of fragments, and ignores
// p. moveTo reports false for a position outside the partition, and for a
// load that fails.
func (it *RangeDeleteIter) moveTo(ctx context.Context, p, i int) bool {
	if it.r.fragTop != nil && !it.load(ctx, p) {
		return false
	}
	if i < 0 {
		i = it.c.count - 1
	}
	it.i = i

	return it.c.restart(i)
}

// load sets c on the fragment partition of the top-level entry p. It returns
// at once when c is on that partition. Otherwise load releases the reference
// to the partition before and checks ctx. It then takes the partition from
// the cache of the Reader under FragmentBlock, and keeps the reference of a
// Block that a LendingCache lends. On a miss it reads the partition from the
// File under ctx and checks it with checkPartition before it adds the
// partition to the cache. It copies the partition into a Block of NewBlock
// for a LendingCache, and keeps the reference of that Block after AddBlock.
// It adds a copy to another BlockCache. A load that fails sets err and
// reports false. It leaves c without a partition.
//
// Error modes:
//
//   - A ctx that ended sets the error of ctx.
//   - An entry of the top-level index or a partition that does not check sets
//     ErrCorrupt.
//   - A read that fails sets the error of the read.
func (it *RangeDeleteIter) load(ctx context.Context, p int) bool {
	if p == it.part {
		return true
	}
	if it.lent != nil {
		it.lent.Release()
		it.lent = nil
	}
	it.part, it.c.block, it.c.count, it.c.valid = -1, nil, 0, false
	if err := ctx.Err(); err != nil {
		it.err = err

		return false
	}
	off, n, _, want, ok := it.r.partition(it.top.restartValue(p), true)
	if !ok {
		it.err = ErrCorrupt

		return false
	}
	c, lc := it.r.cache, it.r.lending
	var b []byte
	var blk *Block
	hit := false
	if lc != nil {
		if blk, hit = lc.Lend(ctx, it.r.cacheID, off, FragmentBlock); hit {
			b = blk.Bytes()
		}
	} else if c != nil {
		b, hit = c.Get(ctx, it.r.cacheID, off)
	}
	if !hit {
		//nolint:gosec // G115: the partition lies inside a table whose size is an int64
		size := int(n) + trailerLen
		it.raw = slices.Grow(it.raw[:0], size)[:size]
		//nolint:gosec // G115: the partition lies before partEnd, inside the table
		if it.err = readAt(ctx, it.r.f, it.raw, int64(off)); it.err != nil {
			return false
		}
		var err error
		it.check.suffix = it.c.suffix
		b, err = unseal(it.raw, want, &it.buf)
		if err != nil || !checkPartition(it.r, &it.top, &it.check, &it.last, b, p) {
			it.err = ErrCorrupt

			return false
		}
		if lc != nil {
			blk = NewBlock(len(b))
			copy(blk.buf, b)
			lc.AddBlock(ctx, it.r.cacheID, off, FragmentBlock, blk)
			b = blk.Bytes()
		} else if c != nil {
			b = bytes.Clone(b)
			c.Add(ctx, it.r.cacheID, off, b)
		}
	}
	it.lent, it.part = blk, p
	it.c.init(b)

	return true
}

// checkPartition reports whether the fragment partition b of the entry p of
// the top-level index of the fragments of r passes checkFragments, with the
// cursor c and the buffer last. top is on that top-level index. The width of
// the versions of c is the width of r. The first fragment of b starts at or
// after the end of the last fragment of the partition before it, which the
// key of the entry before p states. The last fragment of b ends at the key
// of the entry p. The count of the entries of b is the count of the entry p
// less the count of the entry before it. checkPartition reports false for an
// entry of top that does not decode.
func checkPartition(r *Reader, top, c *blockCursor, last *[]byte, b []byte, p int) bool {
	k := top.restartKey(p)
	_, _, count, _, ok := r.partition(top.restartValue(p), true)
	if k == nil || !ok {
		return false
	}
	var after []byte
	before := uint64(0)
	if p > 0 {
		pk := top.restartKey(p - 1)
		if _, _, before, _, ok = r.partition(top.restartValue(p-1), true); pk == nil || !ok {
			return false
		}
		after = pk[:len(pk)-seqLen]
	}
	got, end, err := checkFragments(c, last, b, after)

	//nolint:gosec // G115: a block has fewer entries than bytes
	return err == nil && count >= before && uint64(got) == count-before && bytes.Equal(end, k[:len(k)-seqLen])
}

// checkFragments checks the block of fragments or the fragment partition b,
// with the cursor c and the buffer last for the internal key of the entry
// before. The width of the versions of c is the width of the table. Every
// entry of the block is a restart point with the internal key of the start
// and the sequence of a fragment, and the end of the fragment as its value.
// The first start without its version is at or after after, the end of the
// last fragment before b. checkFragments returns the count of the entries
// and the end of the last fragment, a part of b.
//
// Error modes, each ErrCorrupt:
//
//   - A block without room for its restart offsets, or without entries.
//   - An entry that does not decode.
//   - A start without its version that is not before its end.
//   - An entry that does not follow the entry before it as [RangeDeleter]
//     states.
func checkFragments(c *blockCursor, last *[]byte, b, after []byte) (int, []byte, error) {
	c.init(b)
	// A table without fragments has no block of fragments.
	if c.count == 0 {
		return 0, nil, ErrCorrupt
	}
	w := c.suffix
	var lastEnd []byte
	for i := range c.count {
		if !c.restart(i) {
			return 0, nil, ErrCorrupt
		}
		n := len(c.key) - seqLen
		start, seq := c.key[:n], binary.BigEndian.Uint64(c.key[n:])
		user := start[:max(n-w, 0)]
		if bytes.Compare(user, c.value) >= 0 {
			return 0, nil, ErrCorrupt
		}
		if i == 0 {
			if bytes.Compare(user, after) < 0 {
				return 0, nil, ErrCorrupt
			}
		} else {
			ln := len(*last) - seqLen
			prev := (*last)[:ln]
			if bytes.Equal(user, prev[:max(ln-w, 0)]) && bytes.Equal(c.value, lastEnd) {
				if CompareWithSuffix(start, seq, prev, binary.BigEndian.Uint64((*last)[ln:]), w) <= 0 {
					return 0, nil, ErrCorrupt
				}
			} else if bytes.Compare(user, lastEnd) < 0 {
				return 0, nil, ErrCorrupt
			}
		}
		*last, lastEnd = append((*last)[:0], c.key...), c.value
	}

	return c.count, lastEnd, nil
}
