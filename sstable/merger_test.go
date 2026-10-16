// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/skiplist"
	"go.thesmos.sh/core/sstable"
)

// The workload of the benchmark of a Merger.
const (
	mergeSources = 4
	mergeEntries = 1000
)

// listBytes is the size of each arena of a List that a merge reads. It has
// room for the entries of a run.
const listBytes = 1 << 16

// The kinds of the sources of the machine of a merge.
const (
	sliceKind = 0
	tableKind = 1
	listKind  = 2
)

// errSource is the error of a source that fails.
var errSource = errors.New("source failed")

// mergeMoves generates the moves of a merge, each with the probe of a seek.
var mergeMoves = prop.Composite(func(c *prop.Case) mergeMove {
	return mergeMove{
		name: c.Draw(prop.SampledFrom(moveFirst, moveLast, moveSeekGE, moveSeekLT, moveNext, movePrev), "move"),
		key:  c.Draw(prop.SampledFrom(keys...), "key"),
		seq:  c.Draw(prop.Integer[uint64](0, 6), "seq"),
	}
})

// sourced is an entry of a source of a merge, with the number of the
// source.
type sourced struct {
	entry
	source int
}

// mergeMove is a move of a Merger: the name of one of the six moves of a
// Source, and the probe of a seek.
type mergeMove struct {
	name string
	key  []byte
	seq  uint64
}

// sliceSource is a Source over entries in the order of Compare. It is on no
// entry when i is outside the entries or after an error. The move of the
// number failAt fails with errSource, and a failAt of 0 never fails. A seek
// clears the error, as the seeks of an Iter do, and a step keeps it.
type sliceSource struct {
	es     []entry
	i      int
	moves  int
	failAt int
	on     bool
	err    error
}

var _ sstable.Source = (*sliceSource)(nil)

// First moves to the first entry.
func (s *sliceSource) First() bool {
	s.i, s.err = 0, nil

	return s.moved()
}

// Last moves to the last entry.
func (s *sliceSource) Last() bool {
	s.i, s.err = len(s.es)-1, nil

	return s.moved()
}

// SeekGE moves to the first entry at or after (key, seq).
func (s *sliceSource) SeekGE(key []byte, seq uint64) bool {
	s.i, _ = slices.BinarySearchFunc(s.es, entry{key: key, seq: seq}, func(a, b entry) int {
		return sstable.Compare(a.key, a.seq, b.key, b.seq)
	})
	s.err = nil

	return s.moved()
}

// SeekLT moves to the last entry before (key, seq).
func (s *sliceSource) SeekLT(key []byte, seq uint64) bool {
	i, _ := slices.BinarySearchFunc(s.es, entry{key: key, seq: seq}, func(a, b entry) int {
		return sstable.Compare(a.key, a.seq, b.key, b.seq)
	})
	s.i, s.err = i-1, nil

	return s.moved()
}

// Next moves to the next entry. Next of a source on no entry reports false
// and leaves it there.
func (s *sliceSource) Next() bool {
	if s.on {
		s.i++
	}

	return s.moved()
}

// Prev moves to the entry before the current one. Prev of a source on no
// entry reports false and leaves it there.
func (s *sliceSource) Prev() bool {
	if s.on {
		s.i--
	}

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

// Err returns errSource from the move that failed until the next seek.
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
	s.on = s.err == nil && s.i >= 0 && s.i < len(s.es)

	return s.on
}

func TestMerger(t *testing.T) {
	t.Parallel()

	t.Run("returns what the sorted union returns under any sequence of moves", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "every move must put the merge on the entry of the sorted union of its runs", mergeSteps)
	})

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

	t.Run("Last", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for the zero Merger", func(t *testing.T) {
			t.Parallel()
			var m sstable.Merger
			assert.False(t, m.Last(), "a merge of no sources has no entry")
		})

		t.Run("returns the error of a source that fails", func(t *testing.T) {
			t.Parallel()
			var m sstable.Merger
			m.Reset([]sstable.Source{&sliceSource{es: goldenTable()}, &sliceSource{es: goldenTable(), failAt: 1}})
			expect.False(t, m.Last(), "the merge is exhausted")
			expect.ErrorIs(t, m.Err(), errSource, "the merge returns the error of the source")
			expect.Nil(t, m.Key(), "the exhausted merge has no key")
		})

		t.Run("starts the union from its last entry after a move", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			var m sstable.Merger
			m.Reset([]sstable.Source{&sliceSource{es: es[:goldenEntries/2]}, &sliceSource{es: es[goldenEntries/2:]}})
			assert.True(t, m.Last(), "Last finds an entry")
			assert.True(t, m.Prev(), "Prev finds an entry")
			got, err := scanBack(&m)
			assert.NoError(t, err, "the merge reads its sources")
			want := slices.Clone(es)
			slices.Reverse(want)
			assert.Equal(t, got, want, "the second Last yields the union from its last entry")
		})
	})

	t.Run("SeekGE", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the first entry not before the probe in the union", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "SeekGE of a merge agrees with the sorted union of its sources", func(c *prop.Case) {
				runs, want := drawRuns(c, 0)
				var m sstable.Merger
				m.Reset(sliceSources(runs))
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
				assert.Equal(c, m.Value(), want[i].value, "SeekGE moves to the entry of the first source")
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

	t.Run("SeekLT", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the last entry before the probe in the union", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "SeekLT of a merge agrees with the sorted union of its sources", func(c *prop.Case) {
				runs, want := drawRuns(c, 0)
				var m sstable.Merger
				m.Reset(sliceSources(runs))
				probe := entry{
					key: c.Draw(prop.SampledFrom(keys...), "key"),
					seq: c.Draw(prop.Integer[uint64](0, 6), "seq"),
				}
				i, _ := slices.BinarySearchFunc(want, probe, func(a sourced, b entry) int {
					return sstable.Compare(a.key, a.seq, b.key, b.seq)
				})
				ok := m.SeekLT(probe.key, probe.seq)
				assert.NoError(c, m.Err(), "the merge reads its sources")
				if i == 0 {
					assert.False(c, ok, "SeekLT finds no entry before the union")

					return
				}
				assert.True(c, ok, "SeekLT finds an entry")
				assert.Equal(c, m.Key(), want[i-1].key, "SeekLT moves to the key of the union", assert.EquateEmpty())
				assert.Equal(c, m.Seq(), want[i-1].seq, "SeekLT moves to the sequence of the union")
				assert.Equal(c, m.Value(), want[i-1].value, "SeekLT moves to the entry of the last source")
			})
		})

		t.Run("returns the error of a source that fails", func(t *testing.T) {
			t.Parallel()
			var m sstable.Merger
			m.Reset([]sstable.Source{&sliceSource{es: goldenTable()}, &sliceSource{es: goldenTable(), failAt: 1}})
			expect.False(t, m.SeekLT(goldenKeys[30], 1), "the merge is exhausted")
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
					runs, want := drawRuns(c, 0)
					var m sstable.Merger
					m.Reset(sliceSources(runs))
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
			l, err := skiplist.New(skiplist.Config{DataBytes: listBytes, NodeBytes: listBytes, Seed: 1})
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

	t.Run("Prev", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the union of the sources in reverse order", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "a merge yields its entries back in the reverse order of the union", func(c *prop.Case) {
				runs, want := drawRuns(c, 0)
				var m sstable.Merger
				m.Reset(sliceSources(runs))
				got, err := scanBack(&m)
				assert.NoError(c, err, "the merge reads its sources")
				wantEntries := make([]entry, 0, len(want))
				for _, s := range slices.Backward(want) {
					wantEntries = append(wantEntries, s.entry)
				}
				assert.Equal(c, got, wantEntries, "the merge yields the sorted union back", assert.EquateEmpty())
			})
		})

		t.Run("interleaves a List with a table of the same entries", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			l, err := skiplist.New(skiplist.Config{DataBytes: listBytes, NodeBytes: listBytes, Seed: 1})
			assert.NoError(t, err, "New returns a List")
			for _, e := range es {
				assert.NoError(t, l.Add(e.key, e.seq, e.value), "Add adds the entry")
			}
			it := openTable(t, writeTable(t, settings[3].opts, es), sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			var m sstable.Merger
			m.Reset([]sstable.Source{l.NewIter(), it})
			got, err := scanBack(&m)
			assert.NoError(t, err, "the merge reads both sources")
			want := make([]entry, 0, 2*len(es))
			for _, e := range slices.Backward(es) {
				want = append(want, e, e)
			}
			assert.Equal(t, got, want, "the List and the table yield each entry back in the same place")
		})

		t.Run("returns the error of a source that fails during the merge", func(t *testing.T) {
			t.Parallel()
			var m sstable.Merger
			m.Reset([]sstable.Source{&sliceSource{es: goldenTable()}, &sliceSource{es: goldenTable(), failAt: 6}})
			_, err := scanBack(&m)
			assert.ErrorIs(t, err, errSource, "the merge stops with the error of the source")
			expect.False(t, m.Prev(), "the merge is exhausted")
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

	t.Run("Err", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the error of a source from the move that fails to the next seek", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "a merge must return the error of a source from its failed move to the next seek",
				mergeFails)
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
	expect.MaxAllocs(t, func() { ok = m.Last() }, 0, "Last does not allocate")
	expect.True(t, ok, "the test measures a merge on the last entry")
	expect.MaxAllocs(t, func() { ok = m.SeekGE(goldenKeys[30], 1) }, 0, "SeekGE does not allocate")
	expect.True(t, ok, "the test measures a seek to an entry")
	expect.MaxAllocs(t, func() { ok = m.SeekLT(goldenKeys[30], 1) }, 0, "SeekLT does not allocate")
	expect.True(t, ok, "the test measures a seek back to an entry")
	expect.MaxAllocs(t, func() {
		if !m.Next() {
			m.First()
		}
	}, 0, "Next does not allocate")
	expect.MaxAllocs(t, func() {
		if !m.Prev() {
			m.Last()
		}
	}, 0, "Prev does not allocate")
	expect.MaxAllocs(t, func() {
		if !m.Next() {
			m.First()
		}
		if !m.Prev() {
			m.Last()
		}
	}, 0, "a move against the direction of the last move does not allocate")
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

	b.Run("Prev", func(b *testing.B) {
		var m sstable.Merger
		m.Reset(sources)
		assert.True(b, m.Last(), "the merge has an entry")
		n := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			if !m.Prev() {
				m.Last()
			}
			n += len(m.Key())
		}

		assert.NotEqual(b, n, 0, "the benchmark measures moves to entries")
	})
}

// mergeSteps draws up to four runs as sources of drawn kinds: a sliceSource,
// a cursor of a table or a cursor of a List. It moves one Merger of them
// through a machine of its six moves. The model of the merge is a place in
// the sorted union of the runs. The value of each entry starts with the
// number of its run, so the model also checks the order of entries that
// compare equal.
func mergeSteps(c *prop.Case) {
	runs, union := drawRuns(c, 0)
	sources := make([]sstable.Source, 0, len(runs))
	var iters []*sstable.Iter
	for _, es := range runs {
		switch c.Draw(prop.SampledFrom(sliceKind, tableKind, listKind), "kind") {
		case tableKind:
			opts := settings[c.Draw(prop.Integer(0, len(settings)-1), "setting")].opts
			it := openTable(c, writeTable(c, opts, es), sstable.ReaderOptions{}).NewIter()
			iters = append(iters, it)
			sources = append(sources, it)
		case listKind:
			l, err := skiplist.New(skiplist.Config{
				DataBytes: listBytes,
				NodeBytes: listBytes,
				Seed:      c.Draw(prop.Integer[uint64](0, math.MaxUint64), "seed"),
			})
			assert.NoError(c, err, "New returns a List")
			for _, e := range es {
				assert.NoError(c, l.Add(e.key, e.seq, e.value), "Add adds the entry of the run")
			}
			sources = append(sources, l.NewIter())
		default:
			sources = append(sources, &sliceSource{es: es})
		}
	}
	var m sstable.Merger
	m.Reset(sources)

	at, on, ok := 0, false, false
	probe := func(c *prop.Case, _ struct{}) any {
		return entry{key: c.Draw(prop.SampledFrom(keys...), "key"), seq: c.Draw(prop.Integer[uint64](0, 6), "seq")}
	}

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name: moveFirst,
				Run: func(*prop.Case, int, any) {
					ok = m.First()
					at, on = 0, len(union) != 0
				},
			},
			{
				Name: moveLast,
				Run: func(*prop.Case, int, any) {
					ok = m.Last()
					at, on = len(union)-1, len(union) != 0
				},
			},
			{
				Name:  moveSeekGE,
				Input: probe,
				Run: func(_ *prop.Case, _ int, in any) {
					p := in.(entry)
					ok = m.SeekGE(p.key, p.seq)
					at, _ = slices.BinarySearchFunc(union, p, func(a sourced, b entry) int {
						return sstable.Compare(a.key, a.seq, b.key, b.seq)
					})
					on = at < len(union)
				},
			},
			{
				Name:  moveSeekLT,
				Input: probe,
				Run: func(_ *prop.Case, _ int, in any) {
					p := in.(entry)
					ok = m.SeekLT(p.key, p.seq)
					i, _ := slices.BinarySearchFunc(union, p, func(a sourced, b entry) int {
						return sstable.Compare(a.key, a.seq, b.key, b.seq)
					})
					at, on = i-1, i > 0
				},
			},
			{
				Name:   moveNext,
				Weight: 3,
				Run: func(*prop.Case, int, any) {
					ok = m.Next()
					at++
					on = on && at < len(union)
				},
			},
			{
				Name:   movePrev,
				Weight: 3,
				Run: func(*prop.Case, int, any) {
					ok = m.Prev()
					at--
					on = on && at >= 0
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			assert.NoError(c, m.Err(), "the merge reads its sources")
			assert.Equal(c, ok, on, "a move reports whether the model has an entry at its place")
			if !on {
				assert.Nil(c, m.Key(), "a merge on no entry has no key")

				return
			}
			assert.Equal(c, entry{key: m.Key(), seq: m.Seq(), value: m.Value()}, union[at].entry,
				"the merge is on the entry of the model", assert.EquateEmpty())
		},
	})

	for _, it := range iters {
		assert.NoError(c, it.Close(), "a cursor of a table closes without an error")
	}
}

// mergeFails runs a drawn script of moves on a merge of drawn runs twice:
// first without a failure, then with one drawn source that fails at a drawn
// move of the source. The second run returns what the first returns before
// the move of the merge that fails. From that move to the next seek,
// every move reports false and Err returns errSource. From the next seek on,
// the second run returns what the first returns again.
func mergeFails(c *prop.Case) {
	runs, _ := drawRuns(c, 1)
	script := c.Draw(prop.List(mergeMoves, prop.MaxSize(16)), "moves")
	failing := c.Draw(prop.Integer(0, len(runs)-1), "failing source")
	apply := func(m *sstable.Merger, mv mergeMove) bool {
		switch mv.name {
		case moveFirst:
			return m.First()
		case moveLast:
			return m.Last()
		case moveSeekGE:
			return m.SeekGE(mv.key, mv.seq)
		case moveSeekLT:
			return m.SeekLT(mv.key, mv.seq)
		case moveNext:
			return m.Next()
		default:
			return m.Prev()
		}
	}

	type outcome struct {
		ok bool
		at entry
	}
	sources := sliceSources(runs)
	var m sstable.Merger
	m.Reset(sources)
	clean := make([]outcome, 0, len(script))
	moved := make([]int, 0, len(script))
	for _, mv := range script {
		ok := apply(&m, mv)
		at := entry{key: bytes.Clone(m.Key()), seq: m.Seq(), value: bytes.Clone(m.Value())}
		clean = append(clean, outcome{ok: ok, at: at})
		moved = append(moved, sources[failing].(*sliceSource).moves)
	}
	if len(moved) == 0 || moved[len(moved)-1] == 0 {
		return
	}

	n := c.Draw(prop.Integer(1, moved[len(moved)-1]), "failing move")
	sources = sliceSources(runs)
	sources[failing].(*sliceSource).failAt = n
	m.Reset(sources)
	failed, sought := false, false
	for k, mv := range script {
		ok := apply(&m, mv)
		if failed && mv.name != moveNext && mv.name != movePrev {
			sought = true
		}
		failed = failed || moved[k] >= n
		if !failed || sought {
			assert.Equal(c, ok, clean[k].ok, "a move without a failure finds what it finds in the first run")
			assert.Equal(c, entry{key: m.Key(), seq: m.Seq(), value: m.Value()}, clean[k].at,
				"a move without a failure puts the merge where it is in the first run", assert.EquateEmpty())

			continue
		}
		assert.False(c, ok, "a move from the failed move to the next seek finds no entry")
		assert.ErrorIs(c, m.Err(), errSource, "the merge returns the error of the source")
		assert.Nil(c, m.Key(), "the exhausted merge has no key")
	}
}

// drawRuns draws from least to four runs of entries, and returns them with
// their union in the order of a merge: by Compare, then by the number of the
// run. The value of each entry starts with the number of its run.
func drawRuns(c *prop.Case, least int) ([][]entry, []sourced) {
	runs := make([][]entry, c.Draw(prop.Integer(least, 4), "sources"))
	total := 0
	for s := range runs {
		runs[s] = c.Draw(entries, "entries")
		total += len(runs[s])
	}

	union := make([]sourced, 0, total)
	for s, drawn := range runs {
		run := make([]entry, 0, len(drawn))
		for _, e := range drawn {
			e.value = append([]byte{byte(s)}, e.value...)
			run = append(run, e)
			union = append(union, sourced{entry: e, source: s})
		}
		runs[s] = run
	}
	slices.SortStableFunc(union, func(a, b sourced) int {
		if c := sstable.Compare(a.key, a.seq, b.key, b.seq); c != 0 {
			return c
		}

		return a.source - b.source
	})

	return runs, union
}

// sliceSources returns a sliceSource over each run.
func sliceSources(runs [][]entry) []sstable.Source {
	sources := make([]sstable.Source, 0, len(runs))
	for _, es := range runs {
		sources = append(sources, &sliceSource{es: es})
	}

	return sources
}
