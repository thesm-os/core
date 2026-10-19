// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"bytes"
	"context"
	"slices"
)

// Fragmenter splits range tombstones that overlap at their overlap points,
// and passes the fragments to a [RangeDeleter] in the order of a table. A
// flush of a memtable or a compaction passes its tombstones through a
// Fragmenter into a [Writer].
//
// The Fragmenter takes tombstones in the order of their starts. It keeps the
// tombstones that cover the start of the next fragment. Each [Fragmenter.Add]
// of a later start passes the fragments before that start, which it splits
// at every end before it, and [Fragmenter.Flush] passes the rest. A fragment
// contains each sequence of the tombstones that cover it once, from the
// highest to the lowest. Of n tombstones that overlap, the Fragmenter passes
// at most 2n-1 fragments.
//
// The zero Fragmenter is not usable. [NewFragmenter] returns a Fragmenter.
//
// # Concurrency
//
// A Fragmenter is not safe for concurrent use.
//
// # Allocation contract
//
// Add and Flush do not allocate once the buffers of the tombstones that
// overlap have room, apart from what the RangeDeleter allocates.
type Fragmenter struct {
	// dst receives the fragments.
	dst RangeDeleter

	// start is the start of the fragment that the Fragmenter passes next: the
	// start of the pending tombstones, or after Flush the end of the last
	// fragment. It is empty before the first tombstone, and no key is before
	// the empty key.
	start []byte

	// pending are the ends and the sequences of the tombstones that cover
	// start, from the largest end to the smallest. A slot beyond the length
	// keeps the buffer of its end for a later tombstone.
	pending []pendingDelete

	// seqs receives the sequences of a fragment for their sort.
	seqs []uint64
}

// pendingDelete is a range tombstone that a [Fragmenter] keeps: its end and
// its sequence.
type pendingDelete struct {
	end []byte
	seq uint64
}

// NewFragmenter returns a Fragmenter that passes its fragments to dst, such as
// the Writer of a table.
//
// Error modes: a nil dst returns [ErrOptions].
//
// # Allocation contract
//
// One allocation, the Fragmenter.
func NewFragmenter(dst RangeDeleter) (*Fragmenter, error) {
	if dst == nil {
		return nil, ErrOptions
	}

	return &Fragmenter{dst: dst}, nil
}

// Add adds the range tombstone of the keys from start up to end, which the
// tombstone excludes, at seq. It copies start and end. Its start must be at
// or after the start of the tombstone before it, and after Flush at or after
// the end of every tombstone before it. Add first passes every fragment
// before start to the RangeDeleter under ctx.
//
// Error modes:
//
//   - A start at or after its end returns [ErrRange], and the Fragmenter is
//     unchanged.
//   - A start before the start of the fragment that the Fragmenter passes
//     next returns [ErrOrder], and the Fragmenter is unchanged.
//   - An error of the RangeDeleter returns that error. The tombstones of the
//     Fragmenter are then undefined until Reset.
//
// # Allocation contract
//
// Zero-alloc once the buffers of the tombstones have room, apart from what
// the RangeDeleter allocates.
func (f *Fragmenter) Add(ctx context.Context, start, end []byte, seq uint64) error {
	if bytes.Compare(start, end) >= 0 {
		return ErrRange
	}
	if bytes.Compare(start, f.start) < 0 {
		return ErrOrder
	}
	if err := f.pass(ctx, start); err != nil {
		return err
	}
	f.start = append(f.start[:0], start...)

	// The new tombstone takes the slot after the last one, with the buffer
	// that the slot kept, and moves down to the place of its end.
	n := len(f.pending)
	f.pending = slices.Grow(f.pending, 1)[:n+1]
	f.pending[n].end, f.pending[n].seq = append(f.pending[n].end[:0], end...), seq
	for i := n; i > 0; i-- {
		//dokimi:mutate-skip ror-boundary: tombstones of one end leave together, and emit sorts their sequences, so their order among themselves changes nothing
		if bytes.Compare(f.pending[i-1].end, f.pending[i].end) >= 0 {
			break
		}
		f.pending[i-1], f.pending[i] = f.pending[i], f.pending[i-1]
	}

	return nil
}

// Flush passes every fragment that remains to the RangeDeleter under ctx. A
// tombstone that Add adds after Flush starts at or after the end of the last
// fragment.
//
// Error modes: an error of the RangeDeleter returns that error. The
// tombstones of the Fragmenter are then undefined until Reset.
//
// # Allocation contract
//
// Zero-alloc, apart from what the RangeDeleter allocates.
func (f *Fragmenter) Flush(ctx context.Context) error {
	return f.pass(ctx, nil)
}

// Reset discards the tombstones of f, and keeps its RangeDeleter and its
// buffers for the tombstones of the next table.
//
// # Allocation contract
//
// Zero-alloc.
func (f *Fragmenter) Reset() {
	f.start, f.pending = f.start[:0], f.pending[:0]
}

// pass passes the fragments of the pending tombstones before key, and drops
// the tombstones that end at or before key. Add then moves the start to key.
// A nil key passes every fragment, and the next fragment then starts at the
// end of the last one. pass returns the first error of the RangeDeleter.
func (f *Fragmenter) pass(ctx context.Context, key []byte) error {
	for n := len(f.pending); n != 0; n = len(f.pending) {
		low := f.pending[n-1].end
		if key != nil && bytes.Compare(key, low) < 0 {
			// Every pending tombstone covers the keys from start up to key.
			if bytes.Compare(f.start, key) < 0 {
				return f.emit(ctx, key)
			}

			return nil
		}
		if err := f.emit(ctx, low); err != nil {
			return err
		}
		// The next fragment starts at low, and the tombstones that end there
		// leave. start receives a copy before their slots take other ends.
		f.start = append(f.start[:0], low...)
		for n = len(f.pending); n != 0 && bytes.Equal(f.pending[n-1].end, f.start); n = len(f.pending) {
			f.pending = f.pending[:n-1]
		}
	}

	return nil
}

// emit passes the fragment of the pending tombstones from start up to end to
// the RangeDeleter, once for each distinct sequence of them, from the highest
// to the lowest. It returns the first error of the RangeDeleter.
func (f *Fragmenter) emit(ctx context.Context, end []byte) error {
	f.seqs = f.seqs[:0]
	for _, p := range f.pending {
		f.seqs = append(f.seqs, p.seq)
	}
	slices.Sort(f.seqs)
	f.seqs = slices.Compact(f.seqs)
	for _, seq := range slices.Backward(f.seqs) {
		if err := f.dst.AddRangeDelete(ctx, f.start, end, seq); err != nil {
			return err
		}
	}

	return nil
}
