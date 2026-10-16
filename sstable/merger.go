// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

// Merger is a [Source] over the merge of other sources. It yields the union
// of their entries in the order of [Compare], and entries that compare equal
// in the order of their sources. The zero Merger has no sources.
//
// A Merger keeps the sources that are on an entry in a binary heap. The heap
// orders them by their entries, and sources on equal entries by their index.
// Each step moves the source at the top of the heap and restores the heap
// with O(log k) comparisons for k sources. When a source returns an error
// from Err, the merge is exhausted and [Merger.Err] returns that error.
//
// # Concurrency
//
// A Merger is for one goroutine, and so are its sources while it moves them.
//
// # Allocation contract
//
// The first move after [Merger.Reset] grows the heap to the number of
// sources once. After that the moves do not allocate beyond what the moves
// of the sources allocate.
type Merger struct {
	// err is the error of the source that exhausted the merge.
	err error

	// sources are the sources of the merge.
	sources []Source

	// heap is a binary heap of the indexes of the sources that are on an
	// entry. Its top is the source of the current entry.
	heap []int
}

// Reset replaces the sources of m with sources and leaves m on no entry. m
// keeps sources and the memory of its heap.
//
// # Allocation contract
//
// Zero-alloc.
func (m *Merger) Reset(sources []Source) {
	m.sources, m.heap, m.err = sources, m.heap[:0], nil
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
func (m *Merger) First() bool {
	m.heap, m.err = m.heap[:0], nil
	for i, s := range m.sources {
		if s.First() {
			m.heap = append(m.heap, i)
		} else if m.err = s.Err(); m.err != nil {
			m.heap = m.heap[:0]

			return false
		}
	}

	return m.heapify()
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
func (m *Merger) SeekGE(key []byte, seq uint64) bool {
	m.heap, m.err = m.heap[:0], nil
	for i, s := range m.sources {
		if s.SeekGE(key, seq) {
			m.heap = append(m.heap, i)
		} else if m.err = s.Err(); m.err != nil {
			m.heap = m.heap[:0]

			return false
		}
	}

	return m.heapify()
}

// Next moves the source of the current entry to its next entry, and moves
// the merge to the next entry of the union. It reports whether the entry
// exists. Next of a merge on no entry reports false.
//
// Error modes:
//
//   - A source whose Err is not nil after its Next exhausts the merge, and
//     Err returns that error.
//
// # Allocation contract
//
// Zero-alloc.
func (m *Merger) Next() bool {
	if len(m.heap) == 0 {
		return false
	}

	if s := m.sources[m.heap[0]]; !s.Next() {
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

// Key returns the key of the current entry, or nil for a merge on no entry.
// It is valid until the next move of the merge.
//
// # Allocation contract
//
// Zero-alloc when the Key of the source does not allocate.
func (m *Merger) Key() []byte {
	if len(m.heap) == 0 {
		return nil
	}

	return m.sources[m.heap[0]].Key()
}

// Seq returns the sequence of the current entry, or 0 for a merge on no
// entry.
//
// # Allocation contract
//
// Zero-alloc when the Seq of the source does not allocate.
func (m *Merger) Seq() uint64 {
	if len(m.heap) == 0 {
		return 0
	}

	return m.sources[m.heap[0]].Seq()
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

	return m.sources[m.heap[0]].Value()
}

// Err returns the error of the source that exhausted the merge, or nil.
// First and SeekGE clear it.
//
// # Allocation contract
//
// Zero-alloc.
func (m *Merger) Err() error {
	return m.err
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
		if r := j + 1; r < len(h) && m.less(h[r], h[j]) {
			j = r
		}
		if !m.less(h[j], h[i]) {
			return
		}
		h[i], h[j] = h[j], h[i]
		i = j
	}
}

// less reports whether the entry of the source a is before the entry of the
// source b: by Compare, then by the index of the source.
func (m *Merger) less(a, b int) bool {
	sa, sb := m.sources[a], m.sources[b]
	if c := Compare(sa.Key(), sa.Seq(), sb.Key(), sb.Seq()); c != 0 {
		//dokimi:mutate-skip ror-boundary: c is not 0 inside the branch
		return c < 0
	}

	//dokimi:mutate-skip ror-boundary: the heap never compares a source with itself
	return a < b
}
