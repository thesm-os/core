// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"bytes"
	"cmp"
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
// Under a width of versions, the start of a tombstone ends with its version.
// The Fragmenter then splits the tombstones at their starts without their
// versions and at their ends. A fragment contains each version and sequence
// of the tombstones that cover it once, by version and then by sequence from
// the highest to the lowest. Each start that it passes ends with the version
// of its position.
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

	// start is the start without its version of the fragment that the
	// Fragmenter passes next: the start of the pending tombstones, or after
	// Flush the end of the last fragment. It is empty before the first
	// tombstone, and no key is before the empty key.
	start []byte

	// key receives the start and the version of each call of the
	// RangeDeleter.
	key []byte

	// pending are the ends, the versions and the sequences of the tombstones
	// that cover start, from the largest end to the smallest. A slot beyond
	// the length keeps the buffers of its end and its version for a later
	// tombstone.
	pending []pendingDelete

	// positions receives the versions and the sequences of a fragment for
	// their sort.
	positions []fragmentPosition

	// suffix is the width of the versions at the end of the starts.
	suffix int
}

// pendingDelete is a range tombstone that a [Fragmenter] keeps: its end, its
// version and its sequence.
type pendingDelete struct {
	end, version []byte
	seq          uint64
}

// fragmentPosition is a position of a fragment that a [Fragmenter] passes:
// a version and a sequence.
type fragmentPosition struct {
	version []byte
	seq     uint64
}

// NewFragmenter returns a Fragmenter that passes its fragments to dst, such as
// the Writer of a table. The starts of the tombstones of the Fragmenter end
// with a version of suffixBytes bytes, as the starts of the fragments of a
// Writer with the SuffixBytes of suffixBytes do. A suffixBytes of 0 splits the
// tombstones at their whole starts.
//
// Error modes: a nil dst and a negative suffixBytes return [ErrOptions].
//
// # Allocation contract
//
// One allocation, the Fragmenter.
func NewFragmenter(dst RangeDeleter, suffixBytes int) (*Fragmenter, error) {
	if dst == nil || suffixBytes < 0 {
		return nil, ErrOptions
	}

	return &Fragmenter{dst: dst, suffix: suffixBytes}, nil
}

// Add adds the range tombstone of the keys from start up to end, which the
// tombstone excludes, at seq. Under a width of versions start ends with the
// version of the tombstone. Add copies start and end. The start without its
// version must be at or after the start of the tombstone before it, and
// after Flush at or after the end of every tombstone before it. Add first
// passes every fragment before start to the RangeDeleter under ctx.
//
// Error modes:
//
//   - A start without its version at or after its end returns [ErrRange],
//     and the Fragmenter is unchanged.
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
	k := max(len(start)-f.suffix, 0)
	user, version := start[:k], start[k:]
	if bytes.Compare(user, end) >= 0 {
		return ErrRange
	}
	if bytes.Compare(user, f.start) < 0 {
		return ErrOrder
	}
	if err := f.pass(ctx, user); err != nil {
		return err
	}
	f.start = append(f.start[:0], user...)

	// The new tombstone takes the slot after the last one, with the buffers
	// that the slot kept, and moves down to the place of its end.
	n := len(f.pending)
	f.pending = slices.Grow(f.pending, 1)[:n+1]
	p := &f.pending[n]
	p.end, p.version, p.seq = append(p.end[:0], end...), append(p.version[:0], version...), seq
	for i := n; i > 0; i-- {
		//dokimi:mutate-skip ror-boundary: tombstones of one end leave together and emit sorts their positions, so their order does not change a fragment
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

// Reset discards the tombstones of f, and keeps its RangeDeleter, its width
// of versions and its buffers for the tombstones of the next table.
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
// the RangeDeleter, once for each distinct version and sequence of them, by
// version and then by sequence from the highest to the lowest. The start of
// each call is start followed by the version of the position. emit returns
// the first error of the RangeDeleter.
func (f *Fragmenter) emit(ctx context.Context, end []byte) error {
	f.positions = f.positions[:0]
	for _, p := range f.pending {
		f.positions = append(f.positions, fragmentPosition{version: p.version, seq: p.seq})
	}
	slices.SortFunc(f.positions, func(a, b fragmentPosition) int {
		if c := bytes.Compare(b.version, a.version); c != 0 {
			return c
		}

		return cmp.Compare(b.seq, a.seq)
	})
	f.positions = slices.CompactFunc(f.positions, func(a, b fragmentPosition) bool {
		return a.seq == b.seq && bytes.Equal(a.version, b.version)
	})
	for _, p := range f.positions {
		f.key = append(append(f.key[:0], f.start...), p.version...)
		if err := f.dst.AddRangeDelete(ctx, f.key, end, p.seq); err != nil {
			return err
		}
	}

	return nil
}
