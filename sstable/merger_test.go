// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"errors"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/skiplist"
	"go.thesmos.sh/core/sstable"
)

// The workload of the benchmark of a Merger.
const (
	mergeSources = 4
	mergeEntries = 1000
)

// errSource is the error of a source that fails.
var errSource = errors.New("source failed")

// sourced is an entry of a source of a merge, with the number of the
// source.
type sourced struct {
	entry
	source int
}

// sliceSource is a Source over entries in the order of Compare. The move of
// the number failAt fails with errSource, and a failAt of 0 never fails.
type sliceSource struct {
	es     []entry
	i      int
	moves  int
	failAt int
	err    error
}

var _ sstable.Source = (*sliceSource)(nil)

// First moves to the first entry.
func (s *sliceSource) First() bool {
	s.i = 0

	return s.moved()
}

// SeekGE moves to the first entry at or after (key, seq).
func (s *sliceSource) SeekGE(key []byte, seq uint64) bool {
	s.i, _ = slices.BinarySearchFunc(s.es, entry{key: key, seq: seq}, func(a, b entry) int {
		return sstable.Compare(a.key, a.seq, b.key, b.seq)
	})

	return s.moved()
}

// Next moves to the next entry.
func (s *sliceSource) Next() bool {
	s.i++

	return s.moved()
}

// Key returns the key of the current entry.
func (s *sliceSource) Key() []byte {
	return s.es[s.i].key
}

// Seq returns the sequence of the current entry.
func (s *sliceSource) Seq() uint64 {
	return s.es[s.i].seq
}

// Value returns the value of the current entry.
func (s *sliceSource) Value() []byte {
	return s.es[s.i].value
}

// Err returns errSource once the source failed.
func (s *sliceSource) Err() error {
	return s.err
}

// moved counts a move and reports whether the source is on an entry. The
// move of the number failAt fails the source.
func (s *sliceSource) moved() bool {
	s.moves++
	if s.moves == s.failAt {
		s.err = errSource
	}

	return s.err == nil && s.i < len(s.es)
}

func TestMerger(t *testing.T) {
	t.Parallel()

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("replaces the sources", func(t *testing.T) {
			t.Parallel()
			var m sstable.Merger
			m.Reset([]sstable.Source{&sliceSource{es: goldenTable()}})
			_, err := scanAll(&m)
			assert.NoError(t, err, "the first merge reads its source")
			want := []entry{{key: []byte("z"), seq: 1}}
			m.Reset([]sstable.Source{&sliceSource{es: want}})
			got, err := scanAll(&m)
			assert.NoError(t, err, "the second merge reads its source")
			assert.Equal(t, got, want, "the merge yields the entries of the new sources", assert.EquateEmpty())
		})
	})

	t.Run("First", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for the zero Merger", func(t *testing.T) {
			t.Parallel()
			var m sstable.Merger
			assert.False(t, m.First(), "a merge of no sources has no entry")
		})

		t.Run("returns the error of a source that fails", func(t *testing.T) {
			t.Parallel()
			var m sstable.Merger
			m.Reset([]sstable.Source{&sliceSource{es: goldenTable()}, &sliceSource{es: goldenTable(), failAt: 1}})
			expect.False(t, m.First(), "the merge is exhausted")
			expect.ErrorIs(t, m.Err(), errSource, "the merge returns the error of the source")
			expect.Nil(t, m.Key(), "the exhausted merge has no key")
		})

		t.Run("starts the union from its first entry after a move", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			var m sstable.Merger
			m.Reset([]sstable.Source{&sliceSource{es: es[:goldenEntries/2]}, &sliceSource{es: es[goldenEntries/2:]}})
			assert.True(t, m.First(), "First finds an entry")
			assert.True(t, m.Next(), "Next finds an entry")
			got, err := scanAll(&m)
			assert.NoError(t, err, "the merge reads its sources")
			assert.Equal(t, got, es, "the second First yields the union from its first entry")
		})
	})

	t.Run("SeekGE", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the first entry not before the probe in the union", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "SeekGE of a merge agrees with the sorted union of its sources", func(c *prop.Case) {
				sources, want := drawRuns(c)
				var m sstable.Merger
				m.Reset(sources)
				probe := entry{
					key: c.Draw(prop.SampledFrom(keys...), "key"),
					seq: c.Draw(prop.Integer[uint64](0, 6), "seq"),
				}
				i, _ := slices.BinarySearchFunc(want, probe, func(a sourced, b entry) int {
					return sstable.Compare(a.key, a.seq, b.key, b.seq)
				})
				ok := m.SeekGE(probe.key, probe.seq)
				assert.NoError(c, m.Err(), "the merge reads its sources")
				if i == len(want) {
					assert.False(c, ok, "SeekGE finds no entry past the union")

					return
				}
				assert.True(c, ok, "SeekGE finds an entry")
				assert.Equal(c, m.Key(), want[i].key, "SeekGE moves to the key of the union", assert.EquateEmpty())
				assert.Equal(c, m.Seq(), want[i].seq, "SeekGE moves to the sequence of the union")
				assert.Equal(c, m.Value(), want[i].value, "SeekGE moves to the entry of the first source",
					assert.EquateEmpty())
			})
		})

		t.Run("returns the error of a source that fails", func(t *testing.T) {
			t.Parallel()
			var m sstable.Merger
			m.Reset([]sstable.Source{&sliceSource{es: goldenTable()}, &sliceSource{es: goldenTable(), failAt: 1}})
			expect.False(t, m.SeekGE(goldenKeys[30], 1), "the merge is exhausted")
			expect.ErrorIs(t, m.Err(), errSource, "the merge returns the error of the source")
			expect.Nil(t, m.Key(), "the exhausted merge has no key")
		})
	})

	t.Run("Next", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the union of the sources in order", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "a merge yields its entries in order, and equal entries in the order of their sources",
				func(c *prop.Case) {
					sources, want := drawRuns(c)
					var m sstable.Merger
					m.Reset(sources)
					got, err := scanAll(&m)
					assert.NoError(c, err, "the merge reads its sources")
					wantEntries := make([]entry, 0, len(want))
					for _, s := range want {
						wantEntries = append(wantEntries, s.entry)
					}
					assert.Equal(c, got, wantEntries, "the merge yields the sorted union", assert.EquateEmpty())
				})
		})

		t.Run("interleaves a List with a table of the same entries", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			l, err := skiplist.New(1<<16, 1<<16, 1)
			assert.NoError(t, err, "New returns a List")
			for _, e := range es {
				assert.NoError(t, l.Add(e.key, e.seq, e.value), "Add adds the entry")
			}
			it := openTable(t, writeTable(t, settings[3].opts, es), sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			var m sstable.Merger
			m.Reset([]sstable.Source{l.NewIter(), it})
			got, err := scanAll(&m)
			assert.NoError(t, err, "the merge reads both sources")
			want := make([]entry, 0, 2*len(es))
			for _, e := range es {
				want = append(want, e, e)
			}
			assert.Equal(t, got, want, "the List and the table yield each entry in the same place")
		})

		t.Run("returns the error of a source that fails during the merge", func(t *testing.T) {
			t.Parallel()
			var m sstable.Merger
			m.Reset([]sstable.Source{&sliceSource{es: goldenTable()}, &sliceSource{es: goldenTable(), failAt: 6}})
			_, err := scanAll(&m)
			assert.ErrorIs(t, err, errSource, "the merge stops with the error of the source")
			expect.False(t, m.Next(), "the merge is exhausted")
			expect.Nil(t, m.Key(), "the exhausted merge has no key")
		})
	})

	t.Run("Key", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for a merge on no entry", func(t *testing.T) {
			t.Parallel()
			var m sstable.Merger
			assert.Nil(t, m.Key(), "the zero Merger has no key")
		})
	})

	t.Run("Seq", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0 for a merge on no entry", func(t *testing.T) {
			t.Parallel()
			var m sstable.Merger
			assert.Equal(t, m.Seq(), uint64(0), "the zero Merger has no sequence")
		})
	})

	t.Run("Value", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for a merge on no entry", func(t *testing.T) {
			t.Parallel()
			var m sstable.Merger
			assert.Nil(t, m.Value(), "the zero Merger has no value")
		})
	})
}

// TestMergerAllocs checks the allocation contract of the moves of a Merger.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestMergerAllocs(t *testing.T) {
	sources := make([]sstable.Source, 0, mergeSources)
	for range mergeSources {
		sources = append(sources, &sliceSource{es: goldenTable()})
	}
	var m sstable.Merger
	m.Reset(sources)

	var ok bool
	expect.MaxAllocs(t, func() { ok = m.First() }, 0, "First does not allocate once its heap has grown")
	expect.True(t, ok, "the test measures a merge on an entry")
	expect.MaxAllocs(t, func() { ok = m.SeekGE(goldenKeys[30], 1) }, 0, "SeekGE does not allocate")
	expect.True(t, ok, "the test measures a seek to an entry")
	expect.MaxAllocs(t, func() {
		if !m.Next() {
			m.First()
		}
	}, 0, "Next does not allocate")
}

// BenchmarkMerger reports the cost of a move of a Merger of mergeSources
// sources, and fails when a move allocates.
func BenchmarkMerger(b *testing.B) {
	sources := make([]sstable.Source, 0, mergeSources)
	for s := range mergeSources {
		es := make([]entry, 0, mergeEntries)
		for i := range mergeEntries {
			es = append(es, entry{key: []byte{byte(i >> 8), byte(i)}, seq: uint64(s)})
		}
		sources = append(sources, &sliceSource{es: es})
	}

	b.Run("Next", func(b *testing.B) {
		var m sstable.Merger
		m.Reset(sources)
		assert.True(b, m.First(), "the merge has an entry")
		n := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			if !m.Next() {
				m.First()
			}
			n += len(m.Key())
		}

		assert.NotEqual(b, n, 0, "the benchmark measures moves to entries")
	})
}

// drawRuns draws up to four runs of entries as sources, and returns them
// with their union in the order of a merge: by Compare, then by the number
// of the source.
func drawRuns(c *prop.Case) ([]sstable.Source, []sourced) {
	runs := make([][]entry, c.Draw(prop.Integer(0, 4), "sources"))
	total := 0
	for i := range runs {
		runs[i] = c.Draw(entries, "entries")
		total += len(runs[i])
	}

	sources := make([]sstable.Source, 0, len(runs))
	union := make([]sourced, 0, total)
	for s, es := range runs {
		sources = append(sources, &sliceSource{es: es})
		for _, e := range es {
			union = append(union, sourced{entry: e, source: s})
		}
	}
	slices.SortStableFunc(union, func(a, b sourced) int {
		if c := sstable.Compare(a.key, a.seq, b.key, b.seq); c != 0 {
			return c
		}

		return a.source - b.source
	})

	return sources, union
}
