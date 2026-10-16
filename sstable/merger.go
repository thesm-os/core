// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"context"
	"errors"
	"io"
	"slices"
)

// seekKind is a move of a [Merger] that moves every source of the merge.
type seekKind uint8

const (
	// seekFirst moves every source to its first entry.
	seekFirst seekKind = 0
	// seekLast moves every source to its last entry.
	seekLast seekKind = 1
	// seekGE moves every source to its first entry at or after an entry.
	seekGE seekKind = 2
	// seekLT moves every source to its last entry before an entry.
	seekLT seekKind = 3
)

// Merger is a [Source] over the merge of other sources. It yields the union
// of their entries in the order of [Compare], and entries that compare equal
// in the order of their sources. A move back yields them in the reverse
// order. The zero Merger has no sources.
//
// A Merger keeps the sources that are on an entry in a binary heap, with the
// key and the sequence of the entry of each source. The heap orders them by
// their entries, and sources on equal entries by their index, in the
// direction of the last move. A move in that direction moves the source at
// the top of the heap and restores the heap with O(log k) comparisons for k
// sources. A comparison reads the keys of the heap and does not call a
// source. A move in the other direction first seeks every
// other source to the current entry, moves each one to its side of the
// current entry, and builds the heap in the new direction. When a source
// returns an error from Err, the merge is exhausted and [Merger.Err] returns
// that error. Each move passes its context to the moves of the sources.
//
// [Merger.Close] closes every source of the merge that implements
// io.Closer, such as an [Iter] of a table. [Merger.Reset] replaces the
// sources and does not close them. A caller closes each such source once,
// either through Close or after a Reset replaced the source.
//
// # Concurrency
//
// A Merger is for one goroutine, and so are its sources while it moves them.
//
// # Allocation contract
//
// The first seek after [Merger.Reset] grows the heap to the number of
// sources once. After that the moves do not allocate beyond what the moves
// of the sources allocate.
type Merger struct {
	// err is the error of the source that exhausted the merge.
	err error

	// sources are the sources of the merge.
	sources []Source

	// heap is a binary heap of the sources that are on an entry. Its top is
	// the source of the current entry.
	heap []mergeItem

	// reverse reports whether the last move went back. The heap then orders
	// the sources from the last entry of the union.
	reverse bool
}

var _ Source = (*Merger)(nil)

// mergeItem is a source of a Merger in its heap, with the key and the
// sequence of the entry of the source. key is valid until the source moves,
// and the Merger reads it only before the next move of the source.
type mergeItem struct {
	// key is the key of the entry of the source.
	key []byte

	// seq is the sequence of the entry of the source.
	seq uint64

	// src is the index of the source in the sources of the Merger.
	src int
}

// Reset replaces the sources of m with sources and leaves m on no entry. m
// keeps sources and the memory of its heap. Reset does not close the sources
// that it replaces.
//
// # Allocation contract
//
// Zero-alloc.
func (m *Merger) Reset(sources []Source) {
	m.sources, m.heap, m.err = sources, m.heap[:0], nil
}

// Close closes every source of m that implements io.Closer, in the order of
// the sources, and returns their errors joined with errors.Join. A source
// without a Close method has nothing to release, such as the cursor of a
// memtable. Close then removes the sources from m and leaves m on no entry.
// m keeps the memory of its heap, so a Reset gives it new sources.
//
// # Allocation contract
//
// Zero-alloc when every source closes without an error and without an
// allocation. Each error allocates the error that joins it.
func (m *Merger) Close() error {
	var err error
	for _, s := range m.sources {
		if c, ok := s.(io.Closer); ok {
			err = errors.Join(err, c.Close())
		}
	}
	m.sources, m.heap, m.err = nil, m.heap[:0], nil

	return err
}

// First moves every source to its first entry, and moves the merge to the
// first entry of the union. It reports whether the entry exists.
//
// Error modes:
//
//   - A source whose Err is not nil after its First exhausts the merge, and
//     Err returns that error.
//
// # Allocation contract
//
// Zero-alloc once the heap has grown to the number of sources.
func (m *Merger) First(ctx context.Context) bool {
	return m.seek(ctx, seekFirst, nil, 0)
}

// Last moves every source to its last entry, and moves the merge to the
// last entry of the union. It reports whether the entry exists.
//
// Error modes:
//
//   - A source whose Err is not nil after its Last exhausts the merge, and
//     Err returns that error.
//
// # Allocation contract
//
// Zero-alloc once the heap has grown to the number of sources.
func (m *Merger) Last(ctx context.Context) bool {
	return m.seek(ctx, seekLast, nil, 0)
}

// SeekGE moves every source to its first entry at or after the entry (key,
// seq), and moves the merge to the first such entry of the union. It reports
// whether the entry exists.
//
// Error modes:
//
//   - A source whose Err is not nil after its SeekGE exhausts the merge, and
//     Err returns that error.
//
// # Allocation contract
//
// Zero-alloc once the heap has grown to the number of sources.
func (m *Merger) SeekGE(ctx context.Context, key []byte, seq uint64) bool {
	return m.seek(ctx, seekGE, key, seq)
}

// SeekLT moves every source to its last entry before the entry (key, seq),
// and moves the merge to the last such entry of the union. It reports
// whether the entry exists.
//
// Error modes:
//
//   - A source whose Err is not nil after its SeekLT exhausts the merge, and
//     Err returns that error.
//
// # Allocation contract
//
// Zero-alloc once the heap has grown to the number of sources.
func (m *Merger) SeekLT(ctx context.Context, key []byte, seq uint64) bool {
	return m.seek(ctx, seekLT, key, seq)
}

// Next moves the merge to the next entry of the union and reports whether it
// exists. After a move forward it moves the source of the current entry to
// its next entry. After a move back it first moves every other source to its
// first entry after the current entry. Next of a merge on no entry reports
// false.
//
// Error modes:
//
//   - A source whose Err is not nil after a move exhausts the merge, and
//     Err returns that error.
//
// # Allocation contract
//
// Zero-alloc.
func (m *Merger) Next(ctx context.Context) bool {
	return m.move(ctx, false)
}

// Prev moves the merge to the entry of the union before the current one and
// reports whether it exists. After a move back it moves the source of the
// current entry to its entry before. After a move forward it first moves
// every other source to its last entry before the current entry. Prev of a
// merge on no entry reports false.
//
// Error modes:
//
//   - A source whose Err is not nil after a move exhausts the merge, and
//     Err returns that error.
//
// # Allocation contract
//
// Zero-alloc.
func (m *Merger) Prev(ctx context.Context) bool {
	return m.move(ctx, true)
}

// Key returns the key of the current entry, or nil for a merge on no entry.
// It is the key that the source of the entry returned after its last move,
// and it is valid until the next move of the merge.
//
// # Allocation contract
//
// Zero-alloc.
func (m *Merger) Key() []byte {
	if len(m.heap) == 0 {
		return nil
	}

	return m.heap[0].key
}

// Seq returns the sequence of the current entry, or 0 for a merge on no
// entry. It is the sequence that the source of the entry returned after its
// last move.
//
// # Allocation contract
//
// Zero-alloc.
func (m *Merger) Seq() uint64 {
	if len(m.heap) == 0 {
		return 0
	}

	return m.heap[0].seq
}

// Value returns the value of the current entry, or nil for a merge on no
// entry. It is valid until the next move of the merge.
//
// # Allocation contract
//
// Zero-alloc when the Value of the source does not allocate.
func (m *Merger) Value() []byte {
	if len(m.heap) == 0 {
		return nil
	}

	return m.sources[m.heap[0].src].Value()
}

// Err returns the error of the source that exhausted the merge, or nil.
// First, Last, SeekGE and SeekLT clear it.
//
// # Allocation contract
//
// Zero-alloc.
func (m *Merger) Err() error {
	return m.err
}

// seek moves every source with the move kind under ctx, and builds the heap
// in the direction of the move: forward for seekFirst and seekGE, back for
// seekLast and seekLT. It reports whether the merge is on an entry. A source
// whose Err is not nil after its move exhausts the merge.
func (m *Merger) seek(ctx context.Context, kind seekKind, key []byte, seq uint64) bool {
	m.heap, m.err, m.reverse = slices.Grow(m.heap[:0], len(m.sources)), nil, kind == seekLast || kind == seekLT
	for i, s := range m.sources {
		var ok bool
		switch kind {
		case seekFirst:
			ok = s.First(ctx)
		case seekLast:
			ok = s.Last(ctx)
		case seekGE:
			ok = s.SeekGE(ctx, key, seq)
		default:
			ok = s.SeekLT(ctx, key, seq)
		}
		if ok {
			m.heap = append(m.heap, mergeItem{key: s.Key(), seq: s.Seq(), src: i})
		} else if m.err = s.Err(); m.err != nil {
			m.heap = m.heap[:0]

			return false
		}
	}

	return m.heapify()
}

// move moves the merge back when back is true, and forward otherwise, under
// ctx. A move against the direction of the last move turns the merge first.
// The source at the top of the heap then moves, the top takes the key and the
// sequence of its new entry, and the heap is restored. It reports whether the
// merge is on an entry.
func (m *Merger) move(ctx context.Context, back bool) bool {
	if len(m.heap) == 0 {
		return false
	}
	if back != m.reverse && !m.turn(ctx) {
		return false
	}

	top := &m.heap[0]
	s := m.sources[top.src]
	var ok bool
	if back {
		ok = s.Prev(ctx)
	} else {
		ok = s.Next(ctx)
	}
	if ok {
		top.key, top.seq = s.Key(), s.Seq()
	} else {
		if m.err = s.Err(); m.err != nil {
			m.heap = m.heap[:0]

			return false
		}
		last := len(m.heap) - 1
		m.heap[0] = m.heap[last]
		m.heap = m.heap[:last]
	}
	m.down(0)

	return len(m.heap) != 0
}

// turn reverses the direction of the merge. The source i at the top of the
// heap is on the entry e. Every other source j seeks e with SeekGE, which
// also finds the entries that a List gained since its last move. In the
// order of the merge, an entry of j equal to e is before (e, i) when j is
// below i, and after it otherwise. Before a move back, j keeps an entry
// before (e, i), and otherwise moves to its previous entry, or to its last
// entry when the seek exhausted it. Before a move forward, j moves past an
// entry before (e, i), and otherwise keeps its place. turn then builds the
// heap in the new direction, with i at its top. The sources move under ctx.
// A source whose Err is not nil exhausts the merge, and turn reports false.
func (m *Merger) turn(ctx context.Context) bool {
	top := m.heap[0]
	key, seq := top.key, top.seq
	m.heap = append(m.heap[:0], top)
	for j, s := range m.sources {
		if j == top.src {
			continue
		}
		ok := s.SeekGE(ctx, key, seq)
		//dokimi:mutate-skip ror-boundary: the loop skips the top, so j never equals top
		before := ok && j < top.src && Compare(s.Key(), s.Seq(), key, seq) == 0
		if m.reverse && before {
			ok = s.Next(ctx)
		} else if !m.reverse && !before {
			if ok {
				ok = s.Prev(ctx)
			} else if s.Err() == nil {
				ok = s.Last(ctx)
			}
		}
		if ok {
			m.heap = append(m.heap, mergeItem{key: s.Key(), seq: s.Seq(), src: j})
		} else if m.err = s.Err(); m.err != nil {
			m.heap = m.heap[:0]

			return false
		}
	}
	m.reverse = !m.reverse
	m.heapify()

	return true
}

// heapify orders the heap from its last parent down, and reports whether
// the merge is on an entry.
func (m *Merger) heapify() bool {
	//dokimi:mutate-skip aor: a start past the last parent adds calls of down on leaves, which return at once
	for i := len(m.heap)/2 - 1; i >= 0; i-- {
		m.down(i)
	}

	return len(m.heap) != 0
}

// down moves the source at index i of the heap down until it is before both
// of its children.
func (m *Merger) down(i int) {
	h := m.heap
	for {
		j := 2*i + 1
		if j >= len(h) {
			return
		}
		if r := j + 1; r < len(h) && m.less(&h[r], &h[j]) {
			j = r
		}
		if !m.less(&h[j], &h[i]) {
			return
		}
		h[i], h[j] = h[j], h[i]
		i = j
	}
}

// less reports whether the source of a is before the source of b in the
// order of the heap. Forward, a is before b when its entry is before the
// entry of b by Compare, or equal to it with the source of a below the source
// of b. Back, the order is the reverse. less compares the keys and the
// sequences of the heap.
func (m *Merger) less(a, b *mergeItem) bool {
	if m.reverse {
		a, b = b, a
	}
	if c := Compare(a.key, a.seq, b.key, b.seq); c != 0 {
		//dokimi:mutate-skip ror-boundary: c is not 0 inside the branch
		return c < 0
	}

	//dokimi:mutate-skip ror-boundary: the heap never compares a source with itself
	return a.src < b.src
}
