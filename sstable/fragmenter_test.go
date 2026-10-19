// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"bytes"
	"cmp"
	"context"
	"slices"
	"strconv"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/sstable"
)

// fragmentContract is the contract of the property of the fragments of a
// Fragmenter.
const fragmentContract = "the fragments of a Fragmenter cover each key with the sequences of the tombstones that cover it"

// tombstones generates runs of up to 20 range tombstones in the order of
// their starts. Their bounds come from keys, so they overlap, nest and share
// their bounds, and their sequences repeat.
var tombstones = prop.Composite(func(c *prop.Case) []rangeDelete {
	bound := prop.SampledFrom(keys...)
	n := c.Draw(prop.Integer(0, 20), "tombstones")
	ts := make([]rangeDelete, 0, n)
	for range n {
		start, end := c.Draw(bound, "start"), c.Draw(bound, "end")
		if bytes.Compare(start, end) > 0 {
			start, end = end, start
		}
		if bytes.Equal(start, end) {
			continue
		}
		ts = append(ts, rangeDelete{start: start, end: end, seq: c.Draw(prop.Integer[uint64](0, 5), "seq")})
	}
	slices.SortStableFunc(ts, func(a, b rangeDelete) int { return bytes.Compare(a.start, b.start) })

	return ts
})

// example are the tombstones of the example of the package: from a up to e
// at 9, from c up to g at 7 and from e up to i at 5.
var example = []rangeDelete{
	{start: []byte("a"), end: []byte("e"), seq: 9},
	{start: []byte("c"), end: []byte("g"), seq: 7},
	{start: []byte("e"), end: []byte("i"), seq: 5},
}

// fragmentRecorder is a RangeDeleter that keeps a copy of each fragment of
// its calls, in their order, and the value of callKey in the context of the
// last call. With fail set, it fails each call after its first ok calls with
// errWrite.
type fragmentRecorder struct {
	call      any
	fragments []rangeDelete
	ok        int
	fail      bool
}

var _ sstable.RangeDeleter = (*fragmentRecorder)(nil)

// AddRangeDelete keeps a copy of the fragment, or fails once the recorder
// has made ok calls and fail is set.
func (r *fragmentRecorder) AddRangeDelete(ctx context.Context, start, end []byte, seq uint64) error {
	r.call = ctx.Value(callKey{})
	if r.fail && len(r.fragments) == r.ok {
		return errWrite
	}
	r.fragments = append(r.fragments, rangeDelete{start: bytes.Clone(start), end: bytes.Clone(end), seq: seq})

	return nil
}

// discardDeleter is a RangeDeleter that accepts every fragment and drops it.
type discardDeleter struct{}

var _ sstable.RangeDeleter = discardDeleter{}

// AddRangeDelete accepts the fragment.
func (discardDeleter) AddRangeDelete(context.Context, []byte, []byte, uint64) error {
	return nil
}

func TestFragmenter(t *testing.T) {
	t.Parallel()

	t.Run("NewFragmenter", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrOptions for a nil RangeDeleter", func(t *testing.T) {
			t.Parallel()
			f, err := sstable.NewFragmenter(nil)
			expect.ErrorIs(t, err, sstable.ErrOptions, "NewFragmenter refuses a nil RangeDeleter")
			expect.Nil(t, f, "NewFragmenter returns no Fragmenter")
		})
	})

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		t.Run("passes fragments that cover each key with the sequences of its tombstones", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, fragmentContract, fragmentSteps)
		})

		t.Run("passes the fragments of the example", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, fragmentsOf(t, example), []rangeDelete{
				{start: []byte("a"), end: []byte("c"), seq: 9},
				{start: []byte("c"), end: []byte("e"), seq: 9},
				{start: []byte("c"), end: []byte("e"), seq: 7},
				{start: []byte("e"), end: []byte("g"), seq: 7},
				{start: []byte("e"), end: []byte("g"), seq: 5},
				{start: []byte("g"), end: []byte("i"), seq: 5},
			}, "the Fragmenter splits the tombstones at every start and every end")
		})

		t.Run("passes one fragment for two tombstones of one sequence over the same keys", func(t *testing.T) {
			t.Parallel()
			twice := []rangeDelete{
				{start: []byte("a"), end: []byte("c"), seq: 5}, {start: []byte("a"), end: []byte("c"), seq: 5},
			}
			assert.Equal(t, fragmentsOf(t, twice), twice[:1], "a fragment contains each sequence once")
		})

		t.Run("copies its start and its end", func(t *testing.T) {
			t.Parallel()
			var rec fragmentRecorder
			f, err := sstable.NewFragmenter(&rec)
			assert.NoError(t, err, "NewFragmenter accepts the recorder")
			start, end := []byte("a"), []byte("c")
			assert.NoError(t, f.Add(t.Context(), start, end, 1), "Add adds the tombstone")
			start[0], end[0] = 'x', 'y'
			assert.NoError(t, f.Flush(t.Context()), "Flush passes the fragment")
			assert.Equal(t, rec.fragments, []rangeDelete{{start: []byte("a"), end: []byte("c"), seq: 1}},
				"the fragment has the bytes of the tombstone at its Add")
		})

		t.Run("passes its context to the RangeDeleter", func(t *testing.T) {
			t.Parallel()
			var rec fragmentRecorder
			f, err := sstable.NewFragmenter(&rec)
			assert.NoError(t, err, "NewFragmenter accepts the recorder")
			assert.NoError(t, f.Add(t.Context(), []byte("a"), []byte("c"), 1), "Add adds the first tombstone")
			ctx := context.WithValue(t.Context(), callKey{}, "Add")
			assert.NoError(t, f.Add(ctx, []byte("d"), []byte("e"), 1), "Add passes the fragment before its start")
			assert.Equal(t, rec.call, any("Add"), "the RangeDeleter receives the context of Add")
		})

		// Each case adds its tombstone after the tombstone from b up to d at 1,
		// and then the tombstone from c up to e at 2.
		tests := []struct {
			name    string
			give    rangeDelete
			wantErr error
		}{
			{
				name:    "returns ErrRange for a start at its end",
				give:    rangeDelete{start: []byte("c"), end: []byte("c"), seq: 1},
				wantErr: sstable.ErrRange,
			},
			{
				name:    "returns ErrRange for a start after its end",
				give:    rangeDelete{start: []byte("d"), end: []byte("c"), seq: 1},
				wantErr: sstable.ErrRange,
			},
			{
				name:    "returns ErrOrder for a start before the start of the tombstone before it",
				give:    rangeDelete{start: []byte("a"), end: []byte("c"), seq: 3},
				wantErr: sstable.ErrOrder,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				first, next := rangeDelete{start: []byte("b"), end: []byte("d"), seq: 1},
					rangeDelete{start: []byte("c"), end: []byte("e"), seq: 2}
				var rec fragmentRecorder
				f, err := sstable.NewFragmenter(&rec)
				assert.NoError(t, err, "NewFragmenter accepts the recorder")
				assert.NoError(t, f.Add(t.Context(), first.start, first.end, first.seq), "Add adds the first tombstone")
				assert.ErrorIs(t, f.Add(t.Context(), tt.give.start, tt.give.end, tt.give.seq), tt.wantErr,
					"Add refuses the tombstone")
				assert.NoError(t, f.Add(t.Context(), next.start, next.end, next.seq),
					"Add adds a tombstone after the refusal")
				assert.NoError(t, f.Flush(t.Context()), "Flush passes the fragments")
				assert.Equal(t, rec.fragments, fragmentsOf(t, []rangeDelete{first, next}),
					"the refusal leaves the Fragmenter unchanged")
			})
		}

		t.Run("returns ErrOrder for a start before the end of the last fragment after Flush", func(t *testing.T) {
			t.Parallel()
			f, err := sstable.NewFragmenter(discardDeleter{})
			assert.NoError(t, err, "NewFragmenter accepts the RangeDeleter")
			assert.NoError(t, f.Add(t.Context(), []byte("a"), []byte("c"), 1), "Add adds the tombstone")
			assert.NoError(t, f.Flush(t.Context()), "Flush passes the fragment")
			assert.ErrorIs(t, f.Add(t.Context(), []byte("b"), []byte("d"), 2), sstable.ErrOrder,
				"Add refuses a tombstone that overlaps the fragments that Flush passed")
		})

		t.Run("returns the error of the RangeDeleter at the call that passes the fragment", func(t *testing.T) {
			t.Parallel()
			// The example passes six fragments. The second Add passes the first
			// fragment, the third Add the next two, and Flush the last three.
			// The call after the three Add calls is Flush.
			wantCalls := []int{1, 2, 2, 3, 3, 3}
			for ok, want := range wantCalls {
				rec := fragmentRecorder{ok: ok, fail: true}
				f, err := sstable.NewFragmenter(&rec)
				assert.NoError(t, err, "NewFragmenter accepts the recorder")
				got := -1
				for call := range len(example) + 1 {
					if call < len(example) {
						err = f.Add(t.Context(), example[call].start, example[call].end, example[call].seq)
					} else {
						err = f.Flush(t.Context())
					}
					if err != nil {
						got = call

						break
					}
				}
				expect.ErrorIs(t, err, errWrite, "the call returns the error of the RangeDeleter")
				expect.Equal(t, got, want, "the call that passes fragment "+strconv.Itoa(ok+1)+" returns the error")
			}
		})
	})

	t.Run("Flush", func(t *testing.T) {
		t.Parallel()

		t.Run("takes a tombstone at the end of the last fragment after Flush", func(t *testing.T) {
			t.Parallel()
			var rec fragmentRecorder
			f, err := sstable.NewFragmenter(&rec)
			assert.NoError(t, err, "NewFragmenter accepts the recorder")
			assert.NoError(t, f.Add(t.Context(), []byte("a"), []byte("c"), 1), "Add adds the first tombstone")
			assert.NoError(t, f.Flush(t.Context()), "Flush passes the first fragment")
			assert.NoError(t, f.Add(t.Context(), []byte("c"), []byte("d"), 2),
				"Add adds a tombstone at the end of the fragment")
			assert.NoError(t, f.Flush(t.Context()), "Flush passes the second fragment")
			assert.Equal(t, rec.fragments, []rangeDelete{
				{start: []byte("a"), end: []byte("c"), seq: 1}, {start: []byte("c"), end: []byte("d"), seq: 2},
			}, "each Flush passes the fragments of the tombstones before it")
		})

		t.Run("returns the error of the RangeDeleter", func(t *testing.T) {
			t.Parallel()
			rec := fragmentRecorder{fail: true}
			f, err := sstable.NewFragmenter(&rec)
			assert.NoError(t, err, "NewFragmenter accepts the recorder")
			assert.NoError(t, f.Add(t.Context(), []byte("a"), []byte("c"), 1), "Add keeps the tombstone")
			assert.ErrorIs(t, f.Flush(t.Context()), errWrite, "Flush returns the error of the RangeDeleter")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("discards the tombstones", func(t *testing.T) {
			t.Parallel()
			var rec fragmentRecorder
			f, err := sstable.NewFragmenter(&rec)
			assert.NoError(t, err, "NewFragmenter accepts the recorder")
			assert.NoError(t, f.Add(t.Context(), []byte("c"), []byte("e"), 1),
				"Add adds the tombstone of the first table")
			f.Reset()
			assert.NoError(t, f.Add(t.Context(), []byte("a"), []byte("b"), 2),
				"Add adds a tombstone before the tombstone of the first table")
			assert.NoError(t, f.Flush(t.Context()), "Flush passes the fragments of the second table")
			assert.Equal(t, rec.fragments, []rangeDelete{{start: []byte("a"), end: []byte("b"), seq: 2}},
				"the RangeDeleter receives the fragments after Reset alone")
		})
	})
}

// TestFragmenterAllocs checks the allocation contract of Add and Flush.
// MaxAllocs counts the allocations of the whole process, so the test does not
// run in parallel.
func TestFragmenterAllocs(t *testing.T) {
	t.Run("Add", func(t *testing.T) {
		f, err := sstable.NewFragmenter(discardDeleter{})
		assert.NoError(t, err, "NewFragmenter accepts the RangeDeleter")
		expect.MaxAllocs(t, func() {
			f.Reset()
			for _, d := range example {
				err = cmp.Or(err, f.Add(t.Context(), d.start, d.end, d.seq))
			}
			err = cmp.Or(err, f.Flush(t.Context()))
		}, 0, "Add and Flush do not allocate once the buffers of the tombstones have room")
		assert.NoError(t, err, "the test measures calls that succeed")
	})
}

// BenchmarkFragmenter reports the cost of Add and Flush for the tombstones of
// the example, and fails when they allocate.
func BenchmarkFragmenter(b *testing.B) {
	b.Run("Add", func(b *testing.B) {
		f, err := sstable.NewFragmenter(discardDeleter{})
		assert.NoError(b, err, "NewFragmenter accepts the RangeDeleter")

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			f.Reset()
			for _, d := range example {
				err = cmp.Or(err, f.Add(b.Context(), d.start, d.end, d.seq))
			}
			err = cmp.Or(err, f.Flush(b.Context()))
		}

		assert.NoError(b, err, "the benchmark measures calls that succeed")
	})
}

// fragmentSteps passes drawn tombstones through a Fragmenter into a recorder
// and checks the fragments. Each Add passes only fragments that end at or
// before the start of its tombstone. The fragments follow the order of
// RangeDeleter, and a fragment of n tombstones that overlap has at most 2n-1
// pairs of a start and an end. Each key of keys, which contains every bound,
// has the distinct sequences of the tombstones that cover it in the
// fragments that cover it, from the highest to the lowest.
func fragmentSteps(c *prop.Case) {
	ts := c.Draw(tombstones, "tombstones")
	var rec fragmentRecorder
	f, err := sstable.NewFragmenter(&rec)
	assert.NoError(c, err, "NewFragmenter accepts the recorder")
	for _, d := range ts {
		assert.NoError(c, f.Add(c.Context(), d.start, d.end, d.seq),
			"Add takes the tombstones in the order of their starts")
		for _, g := range rec.fragments {
			assert.InRange(c, bytes.Compare(g.end, d.start), -1, 0, "Add passes the fragments before its start alone")
		}
	}
	assert.NoError(c, f.Flush(c.Context()), "Flush passes the rest of the fragments")

	fs := rec.fragments
	assert.Pairwise(c, fs, func(earlier, later rangeDelete) bool {
		if bytes.Equal(earlier.start, later.start) && bytes.Equal(earlier.end, later.end) {
			return later.seq < earlier.seq
		}

		return bytes.Compare(later.start, earlier.end) >= 0
	}, "each fragment follows the fragment before it in the order of RangeDeleter")
	pairs := 0
	for i, g := range fs {
		assert.Equal(c, bytes.Compare(g.start, g.end), -1, "a fragment starts before its end")
		if i == 0 || !bytes.Equal(g.start, fs[i-1].start) || !bytes.Equal(g.end, fs[i-1].end) {
			pairs++
		}
	}
	if len(ts) != 0 {
		assert.InRange(c, pairs, 0, float64(2*len(ts)-1), "n tombstones take at most 2n-1 fragments")
	}
	for _, key := range keys {
		var want, got []uint64
		for _, d := range ts {
			if bytes.Compare(d.start, key) <= 0 && bytes.Compare(key, d.end) < 0 {
				want = append(want, d.seq)
			}
		}
		slices.Sort(want)
		slices.Reverse(want)
		want = slices.Compact(want)
		for _, g := range fs {
			if bytes.Compare(g.start, key) <= 0 && bytes.Compare(key, g.end) < 0 {
				got = append(got, g.seq)
			}
		}
		assert.Equal(c, got, want, "the fragments of a key have the sequences of its tombstones", assert.EquateEmpty())
	}
}

// fragmentsOf returns the fragments that a Fragmenter passes for the
// tombstones ts.
func fragmentsOf(tb assert.TB, ts []rangeDelete) []rangeDelete {
	tb.Helper()

	ctx := assert.Context(tb)
	var rec fragmentRecorder
	f, err := sstable.NewFragmenter(&rec)
	assert.NoError(tb, err, "NewFragmenter accepts the recorder")
	for _, d := range ts {
		assert.NoError(tb, f.Add(ctx, d.start, d.end, d.seq), "Add takes the tombstones in the order of their starts")
	}
	assert.NoError(tb, f.Flush(ctx), "Flush passes the fragments")

	return rec.fragments
}
