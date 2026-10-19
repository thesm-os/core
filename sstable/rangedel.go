// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"

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
// A [*Writer] is a RangeDeleter, and a [Fragmenter] passes its fragments to
// one. The sequence is opaque to the package, as the sequence of an entry
// is. The engine applies each fragment to the entries that it covers.
type RangeDeleter interface {
	// AddRangeDelete adds the fragment of the keys from start up to end,
	// which the fragment excludes, at the sequence seq.
	AddRangeDelete(ctx context.Context, start, end []byte, seq uint64) error
}

// RangeDeleteIter is a cursor over the fragments of the range tombstones of
// a table, which [Reader.RangeDeletes] returns. It yields the fragments in
// the order of their starts, and each sequence of a fragment at a position of
// its own, from the highest sequence to the lowest. The fragments do not
// overlap, so the positions of one fragment have one end, and no other
// fragment has that end.
//
// The Reader keeps the block of fragments in memory, and [Open] checks every
// entry of the block. A move does not read a block, does not take a context
// and cannot fail. A move off either end leaves the cursor on no position, and
// Next and Prev report false there. The start and the end of the current
// position are valid until the next move.
//
// # Concurrency
//
// A cursor is for one goroutine. Cursors of one [Reader] can move at once.
//
// # Allocation contract
//
// The moves, Start, End, Seq and Close do not allocate. Reader.RangeDeletes
// takes the cursor from a pool, which allocates one when it is empty.
type RangeDeleteIter struct {
	// c is on the entry of the current position in the block of fragments.
	// Every entry of the block is a restart point.
	c blockCursor

	// i is the restart point of the current position.
	i int

	// open reports whether the cursor is out of the pool, so a second Close
	// does not return it twice.
	open bool
}

// First moves to the first position and reports whether it exists.
//
// # Allocation contract
//
// Zero-alloc.
func (it *RangeDeleteIter) First() bool {
	it.i = 0

	return it.c.restart(0)
}

// Last moves to the last position and reports whether it exists.
//
// # Allocation contract
//
// Zero-alloc.
func (it *RangeDeleteIter) Last() bool {
	it.i = it.c.count - 1

	return it.c.restart(it.i)
}

// SeekGE moves to the first position of the first fragment whose end is
// after key: the fragment that covers key, or the first fragment after key.
// It reports whether such a fragment exists. The ends of the fragments rise
// with their starts, so SeekGE searches the ends in halves.
//
// # Allocation contract
//
// Zero-alloc.
func (it *RangeDeleteIter) SeekGE(key []byte) bool {
	lo, hi := 0, it.c.count
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if bytes.Compare(it.c.restartValue(mid), key) <= 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	it.i = lo

	return it.c.restart(lo)
}

// SeekLT moves to the last position of the last fragment whose start is
// before key, and reports whether such a fragment exists.
//
// # Allocation contract
//
// Zero-alloc.
func (it *RangeDeleteIter) SeekLT(key []byte) bool {
	// Open decodes every entry of the block, so the search decodes each entry
	// that it compares.
	i, _ := it.c.search(0, it.c.count, key, math.MaxUint64)
	it.i = i - 1

	return it.c.restart(it.i)
}

// Next moves to the next position and reports whether it exists. Next of a
// cursor on no position reports false.
//
// # Allocation contract
//
// Zero-alloc.
func (it *RangeDeleteIter) Next() bool {
	if !it.c.valid {
		return false
	}
	it.i++

	return it.c.restart(it.i)
}

// Prev moves to the position before the current one and reports whether it
// exists. Prev of a cursor on no position reports false.
//
// # Allocation contract
//
// Zero-alloc.
func (it *RangeDeleteIter) Prev() bool {
	if !it.c.valid {
		return false
	}
	it.i--

	return it.c.restart(it.i)
}

// Start returns the start of the fragment of the current position, with a
// capacity of its length, or nil for a cursor on no position.
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

// Close returns the cursor and its buffers to the pool of cursors. The cursor
// is unusable after Close, because a later [Reader.RangeDeletes] can take it
// from the pool. A second Close before such a RangeDeletes leaves the pool
// unchanged.
//
// # Allocation contract
//
// Zero-alloc.
func (it *RangeDeleteIter) Close() {
	if !it.open {
		return
	}
	*it = RangeDeleteIter{c: blockCursor{key: it.c.key[:0]}}
	rangeIters.Put(it)
}

// checkFragments checks the block of fragments b of a table of format
// version 3, and sets the count of its entries and copies of the start of
// its first fragment and of the end of its last fragment in s. Every entry
// of the block is a restart point with the internal key of the start and the
// sequence of a fragment, and the end of the fragment as its value.
// checkFragments returns ErrCorrupt for a block without room for its restart
// offsets or without entries, for an entry that does not decode, for a start
// that is not before its end, and for an entry that does not follow the
// entry before it as [RangeDeleter] states.
func checkFragments(b []byte, s *Summary) error {
	var c blockCursor
	c.init(b)
	// A table without fragments has no block of fragments.
	if c.count == 0 {
		return ErrCorrupt
	}
	var last, lastEnd []byte
	for i := range c.count {
		if !c.restart(i) {
			return ErrCorrupt
		}
		n := len(c.key) - seqLen
		key, seq := c.key[:n], binary.BigEndian.Uint64(c.key[n:])
		if bytes.Compare(key, c.value) >= 0 {
			return ErrCorrupt
		}
		if i == 0 {
			s.RangeDeleteStart = append([]byte{}, key...)
		} else {
			ln := len(last) - seqLen
			if bytes.Equal(key, last[:ln]) && bytes.Equal(c.value, lastEnd) {
				if seq >= binary.BigEndian.Uint64(last[ln:]) {
					return ErrCorrupt
				}
			} else if bytes.Compare(key, lastEnd) < 0 {
				return ErrCorrupt
			}
		}
		last, lastEnd = append(last[:0], c.key...), c.value
	}
	s.RangeDeletes, s.RangeDeleteEnd = int64(c.count), append([]byte{}, lastEnd...)

	return nil
}
