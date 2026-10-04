// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// summingCounter is a Counter that sums its adds and counts its calls.
type summingCounter struct {
	sum, calls atomic.Int64
}

var _ telemetry.Counter = (*summingCounter)(nil)

// Add adds value to the sum and counts the call.
func (c *summingCounter) Add(_ context.Context, value int64) {
	c.sum.Add(value)
	c.calls.Add(1)
}

// With returns c.
func (c *summingCounter) With([]telemetry.Attr) telemetry.Counter {
	return c
}

// Release does nothing.
func (*summingCounter) Release() {}

func TestShardedCounter(t *testing.T) {
	t.Parallel()

	t.Run("NewShardedCounter", func(t *testing.T) {
		t.Parallel()

		refused := []struct {
			name    string
			counter telemetry.Counter
			cells   int
		}{
			{name: "returns ErrConfig for a nil Counter", cells: 1},
			{name: "returns ErrConfig for zero cells", counter: &summingCounter{}},
			{name: "returns ErrConfig for more than 65536 cells", counter: &summingCounter{}, cells: 65537},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := telemetry.NewShardedCounter(tt.counter, tt.cells)
				testkit.ErrorIs(t, err, telemetry.ErrConfig, "NewShardedCounter must refuse the arguments")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}

		cells := []struct {
			name string
			give int
			want int
		}{
			{name: "returns one cell for one cell", give: 1, want: 1},
			{name: "rounds three cells up to four", give: 3, want: 4},
			{name: "returns four cells for four cells", give: 4, want: 4},
			{name: "returns the largest number of cells", give: 65536, want: 65536},
		}
		for _, tt := range cells {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s, err := telemetry.NewShardedCounter(&summingCounter{}, tt.give)
				testkit.NoError(t, err, "NewShardedCounter must accept the cells")
				testkit.Equal(t, s.Cells(), tt.want, "Cells must round up to a power of two")
			})
		}
	})

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		t.Run("adds to the cells of every index modulo the cells", func(t *testing.T) {
			t.Parallel()
			c := &summingCounter{}
			s := newSharded(t, c, 4)

			for _, i := range []int{0, 3, 4, 5, -1, 1 << 40} {
				s.Add(i, 2)
			}

			s.Flush(t.Context())
			testkit.Equal(t, c.sum.Load(), int64(12), "every add must reach the Counter")
		})

		t.Run("adds nothing for a negative value", func(t *testing.T) {
			t.Parallel()
			c := &summingCounter{}
			s := newSharded(t, c, 2)

			s.Add(0, -5)
			s.Add(0, 3)
			s.Flush(t.Context())
			testkit.Equal(t, c.sum.Load(), int64(3), "a negative add must change nothing")
		})
	})

	t.Run("Flush", func(t *testing.T) {
		t.Parallel()

		t.Run("adds the sum since the previous Flush", func(t *testing.T) {
			t.Parallel()
			c := &summingCounter{}
			s := newSharded(t, c, 2)

			s.Add(0, 3)
			s.Flush(t.Context())
			s.Add(1, 4)
			s.Flush(t.Context())

			testkit.Equal(t, c.sum.Load(), int64(7), "the Counter must receive each add once")
			testkit.Equal(t, c.calls.Load(), int64(2), "each Flush must add its change in one call")
		})

		t.Run("calls the Counter not at all when the sum has not changed", func(t *testing.T) {
			t.Parallel()
			c := &summingCounter{}
			s := newSharded(t, c, 2)

			s.Add(0, 1)
			s.Flush(t.Context())
			s.Flush(t.Context())
			testkit.Equal(t, c.calls.Load(), int64(1), "a Flush without a change must not call the Counter")
		})

		t.Run("adds every add once beside concurrent flushes", func(t *testing.T) {
			t.Parallel()
			c := &summingCounter{}
			s := newSharded(t, c, 4)

			var wg sync.WaitGroup
			for worker := range 4 {
				wg.Go(func() {
					for range 1000 {
						s.Add(worker, 1)
					}
				})
			}

			wg.Go(func() {
				for range 100 {
					s.Flush(t.Context())
				}
			})
			wg.Wait()

			s.Flush(t.Context())
			testkit.Equal(t, c.sum.Load(), int64(4000), "the Counter must receive every add exactly once")
		})
	})
}

func BenchmarkShardedCounter(b *testing.B) {
	s, err := telemetry.NewShardedCounter(noop.Reporter{}.Counter(telemetry.InstrumentSpec{Name: "bench"}), 64)
	testkit.NoError(b, err, "NewShardedCounter must accept the arguments")

	b.Run("Add", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			s.Add(7, 1)
		}
	})

	b.Run("Flush", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			s.Add(3, 1)
			s.Flush(b.Context())
		}
	})
}

// newSharded returns the ShardedCounter of c and cells, and fails tb when
// NewShardedCounter refuses them.
func newSharded(tb testing.TB, c telemetry.Counter, cells int) *telemetry.ShardedCounter {
	tb.Helper()

	s, err := telemetry.NewShardedCounter(c, cells)
	testkit.NoError(tb, err, "NewShardedCounter must accept the arguments")

	return s
}
