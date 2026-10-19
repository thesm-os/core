// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"bytes"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/sstable"
)

// seekKeys generates the keys of the seeks of a cursor of fragments: the keys
// that the generators of fragments draw most often, and random bytes.
var seekKeys = prop.OneOf(prop.SampledFrom(keys...), prop.Bytes(prop.MaxSize(20)))

func TestRangeDeleteIter(t *testing.T) {
	t.Parallel()

	t.Run("First", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the first position", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, goldenDeletes)
			defer it.Close()
			assert.True(t, it.First(), "First finds a fragment")
			assert.Equal(t, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, goldenDeletes[0],
				"First moves to the first fragment at its highest sequence")
		})
	})

	t.Run("Last", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the last position", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, goldenDeletes)
			defer it.Close()
			assert.True(t, it.Last(), "Last finds a fragment")
			assert.Equal(t, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()},
				goldenDeletes[len(goldenDeletes)-1], "Last moves to the last fragment at its lowest sequence")
		})
	})

	t.Run("SeekGE", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the first position of the first fragment whose end is after key", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "SeekGE finds what a sorted model of the fragments finds", func(c *prop.Case) {
				dels := c.Draw(fragments, "fragments")
				it := fragmentCursor(c, dels)
				defer it.Close()
				for range 10 {
					key := c.Draw(seekKeys, "key")
					i := slices.IndexFunc(dels, func(d rangeDelete) bool { return bytes.Compare(d.end, key) > 0 })
					if i < 0 {
						assert.False(c, it.SeekGE(key), "SeekGE finds no fragment that ends after key")

						continue
					}
					assert.True(c, it.SeekGE(key), "SeekGE finds a fragment that ends after key")
					assert.Equal(c, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, dels[i],
						"SeekGE moves to the first position of the fragment")
					if i+1 == len(dels) {
						assert.False(c, it.Next(), "Next of the last position after SeekGE moves off the end")

						continue
					}
					assert.True(c, it.Next(), "Next after SeekGE moves to the position after it")
					assert.Equal(c, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, dels[i+1],
						"Next after SeekGE moves to the next position")
				}
			})
		})
	})

	t.Run("SeekLT", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the last position of the last fragment whose start is before key", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "SeekLT finds what a sorted model of the fragments finds", func(c *prop.Case) {
				dels := c.Draw(fragments, "fragments")
				it := fragmentCursor(c, dels)
				defer it.Close()
				for range 10 {
					key := c.Draw(seekKeys, "key")
					i := len(dels) - 1
					for i >= 0 && bytes.Compare(dels[i].start, key) >= 0 {
						i--
					}
					if i < 0 {
						assert.False(c, it.SeekLT(key), "SeekLT finds no fragment that starts before key")

						continue
					}
					assert.True(c, it.SeekLT(key), "SeekLT finds a fragment that starts before key")
					assert.Equal(c, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, dels[i],
						"SeekLT moves to the last position of the fragment")
					if i == 0 {
						assert.False(c, it.Prev(), "Prev of the first position after SeekLT moves off the start")

						continue
					}
					assert.True(c, it.Prev(), "Prev after SeekLT moves to the position before it")
					assert.Equal(c, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, dels[i-1],
						"Prev after SeekLT moves to the position before")
				}
			})
		})
	})

	t.Run("Next", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to each position in the order of the fragments", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Next yields the fragments in their order", func(c *prop.Case) {
				dels := c.Draw(fragments, "fragments")
				it := fragmentCursor(c, dels)
				defer it.Close()
				var got []rangeDelete
				for ok := it.First(); ok && len(got) <= len(dels); ok = it.Next() {
					got = append(got,
						rangeDelete{start: bytes.Clone(it.Start()), end: bytes.Clone(it.End()), seq: it.Seq()})
				}
				assert.Equal(c, got, dels, "the cursor yields each fragment once", assert.EquateEmpty())
			})
		})

		t.Run("reports false for a cursor on no position", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, goldenDeletes)
			defer it.Close()
			assert.False(t, it.Next(), "a new cursor has no next position")
			assert.True(t, it.Last(), "Last finds a fragment")
			assert.False(t, it.Next(), "Next of the last position moves off the end")
			expect.False(t, it.Next(), "Next of a cursor off the end reports false")
			expect.False(t, it.Prev(), "Prev of a cursor off the end reports false")
		})
	})

	t.Run("Prev", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to each position in the reverse order of the fragments", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Prev yields the fragments in their reverse order", func(c *prop.Case) {
				dels := c.Draw(fragments, "fragments")
				it := fragmentCursor(c, dels)
				defer it.Close()
				var got []rangeDelete
				for ok := it.Last(); ok && len(got) <= len(dels); ok = it.Prev() {
					got = append(got,
						rangeDelete{start: bytes.Clone(it.Start()), end: bytes.Clone(it.End()), seq: it.Seq()})
				}
				slices.Reverse(got)
				assert.Equal(c, got, dels, "the cursor yields each fragment once", assert.EquateEmpty())
			})
		})

		t.Run("reports false for a cursor on no position", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, goldenDeletes)
			defer it.Close()
			assert.False(t, it.Prev(), "a new cursor has no position before")
			assert.True(t, it.First(), "First finds a fragment")
			assert.False(t, it.Prev(), "Prev of the first position moves off the start")
			expect.False(t, it.Prev(), "Prev of a cursor off the start reports false")
			expect.False(t, it.Next(), "Next of a cursor off the start reports false")
		})
	})

	t.Run("Start", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a start with a capacity of its length", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, goldenDeletes)
			defer it.Close()
			assert.True(t, it.First(), "First finds a fragment")
			assert.Equal(t, cap(it.Start()), len(it.Start()), "an append to the start does not write into the block")
		})

		t.Run("returns nil for a cursor on no position", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, goldenDeletes)
			defer it.Close()
			assert.Nil(t, it.Start(), "a new cursor has no start")
		})
	})

	t.Run("End", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an end with a capacity of its length", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, goldenDeletes)
			defer it.Close()
			assert.True(t, it.First(), "First finds a fragment")
			assert.Equal(t, cap(it.End()), len(it.End()), "an append to the end does not write into the block")
		})

		t.Run("returns nil for a cursor on no position", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, goldenDeletes)
			defer it.Close()
			assert.True(t, it.Last(), "Last finds a fragment")
			assert.False(t, it.Next(), "Next of the last position moves off the end")
			assert.Nil(t, it.End(), "a cursor off the end has no end")
		})
	})

	t.Run("Seq", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0 for a cursor on no position", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, []rangeDelete{{start: []byte("a"), end: []byte("b"), seq: 7}})
			defer it.Close()
			assert.Equal(t, it.Seq(), uint64(0), "a new cursor has no sequence")
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the cursor to the pool once for a second Close", func(t *testing.T) {
			t.Parallel()
			r := openTable(t, writeTable(t, settings[0].opts, nil, goldenDeletes...), sstable.ReaderOptions{})
			it := r.RangeDeletes()
			it.Close()
			it.Close()
			first, second := r.RangeDeletes(), r.RangeDeletes()
			defer first.Close()
			defer second.Close()
			assert.NotEqual(t, first, second, "the pool hands out the closed cursor once", assert.ByIdentity())
		})
	})
}

// TestRangeDeleteIterAllocs checks the allocation contract of the moves of a
// cursor of fragments. MaxAllocs counts the allocations of the whole process,
// so the test does not run in parallel.
func TestRangeDeleteIterAllocs(t *testing.T) {
	it := fragmentCursor(t, goldenDeletes)
	defer it.Close()
	key := []byte("key 017")

	t.Run("SeekGE", func(t *testing.T) {
		ok := false
		expect.MaxAllocs(t, func() { ok = it.SeekGE(key) }, 0, "SeekGE does not allocate")
		expect.True(t, ok, "the test measures a seek to a fragment")
	})

	t.Run("SeekLT", func(t *testing.T) {
		ok := false
		expect.MaxAllocs(t, func() { ok = it.SeekLT(key) }, 0, "SeekLT does not allocate")
		expect.True(t, ok, "the test measures a seek to a fragment")
	})

	t.Run("Next", func(t *testing.T) {
		ok := false
		expect.MaxAllocs(t, func() { ok = it.First() && it.Next() }, 0, "First and Next do not allocate")
		expect.True(t, ok, "the test measures moves to fragments")
	})

	t.Run("Prev", func(t *testing.T) {
		ok := false
		expect.MaxAllocs(t, func() { ok = it.Last() && it.Prev() }, 0, "Last and Prev do not allocate")
		expect.True(t, ok, "the test measures moves to fragments")
	})
}

// BenchmarkRangeDeleteIter reports the cost of the seeks of a cursor of
// fragments, and fails when they allocate.
func BenchmarkRangeDeleteIter(b *testing.B) {
	it := fragmentCursor(b, goldenDeletes)
	defer it.Close()
	key := []byte("key 017")

	for _, tt := range []struct {
		name string
		seek func([]byte) bool
	}{
		{name: "SeekGE", seek: it.SeekGE},
		{name: "SeekLT", seek: it.SeekLT},
	} {
		b.Run(tt.name, func(b *testing.B) {
			ok := false

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				ok = tt.seek(key)
			}

			assert.True(b, ok, "the benchmark measures a seek to a fragment")
		})
	}
}

// fragmentCursor writes a table of the fragments dels without entries, opens
// it and returns a cursor over its fragments, which the caller closes.
func fragmentCursor(tb assert.TB, dels []rangeDelete) *sstable.RangeDeleteIter {
	tb.Helper()

	data := writeTable(tb, settings[0].opts, nil, dels...)

	return openTable(tb, data, sstable.ReaderOptions{}).RangeDeletes()
}
