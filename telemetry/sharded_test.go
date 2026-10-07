// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"context"
	"math"
	"math/bits"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// within bounds the concurrent cases of ShardedCounter.
const within = 10 * time.Second

// summingCounter is a Counter that sums its adds and counts its calls. Its
// methods are safe for concurrent use.
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
func (c *summingCounter) With([]telemetry.Attr) telemetry.Counter { return c }

// Release does nothing.
func (*summingCounter) Release() {}

func TestShardedCounter(t *testing.T) {
	t.Parallel()

	t.Run("NewShardedCounter", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrConfig for a nil Counter", func(t *testing.T) {
			t.Parallel()
			_, err := telemetry.NewShardedCounter(nil, 1)
			expect.ErrorIs(t, err, telemetry.ErrConfig, "NewShardedCounter must refuse a nil Counter")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
		})

		t.Run("returns ErrConfig for fewer than one cell", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(cells int) error {
				_, err := telemetry.NewShardedCounter(&summingCounter{}, cells)

				return err
			}, telemetry.ErrConfig, "NewShardedCounter must refuse fewer than one cell",
				prop.Using(prop.Integer(math.MinInt, 0)), prop.Example(0))
		})

		t.Run("returns ErrConfig for more than 65536 cells", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(cells int) error {
				_, err := telemetry.NewShardedCounter(&summingCounter{}, cells)

				return err
			}, telemetry.ErrConfig, "NewShardedCounter must refuse more than 65536 cells",
				prop.Using(prop.Integer(65537, math.MaxInt)), prop.Example(65537))
		})

		tests := []struct {
			name string
			give int
			want int
		}{
			{name: "returns one cell for one cell", give: 1, want: 1},
			{name: "rounds three cells up to four", give: 3, want: 4},
			{name: "returns four cells for four cells", give: 4, want: 4},
			{name: "returns the largest number of cells", give: 65536, want: 65536},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, newSharded(t, &summingCounter{}, tt.give).Cells(), tt.want,
					"Cells must return the cells rounded up to a power of two")
			})
		}

		t.Run("rounds the cells up to the next power of two", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Cells must return the smallest power of two at or above the cells", func(c *prop.Case) {
				cells := c.Draw(prop.Integer(1, 65536), "cells")
				got := newSharded(c, &summingCounter{}, cells).Cells()
				assert.Equal(c, bits.OnesCount(uint(got)), 1, "Cells must return a power of two")
				assert.InRange(c, got, float64(cells), float64(2*cells-1),
					"Cells must return the smallest power of two at or above the cells")
			})
		})
	})

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		t.Run("adds every value at any index", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Flush must add every value of Add to the Counter", func(c *prop.Case) {
				counter := &summingCounter{}
				s := newSharded(c, counter, c.Draw(prop.SampledFrom(1, 2, 4, 64), "cells"))
				values := c.Draw(prop.List(prop.Integer[int64](0, 1000)), "values")

				var want int64
				for _, v := range values {
					s.Add(c.Draw(prop.Integer(math.MinInt, math.MaxInt), "index"), v)
					want += v
				}

				s.Flush(c.Context())
				assert.Equal(c, counter.sum.Load(), want, "every add must reach the Counter")
			})
		})

		t.Run("adds nothing for a negative value", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Add must not add a negative value", func(c *prop.Case) {
				counter := &summingCounter{}
				s := newSharded(c, counter, 2)
				s.Add(0, c.Draw(prop.Integer[int64](math.MinInt64, -1), "value"))
				s.Add(1, 3)

				s.Flush(c.Context())
				assert.Equal(c, counter.sum.Load(), int64(3), "a negative add must change nothing")
			})
		})
	})

	t.Run("Flush", func(t *testing.T) {
		t.Parallel()

		t.Run("adds the sum since the previous Flush in one call", func(t *testing.T) {
			t.Parallel()
			c := &summingCounter{}
			s := newSharded(t, c, 2)

			s.Add(0, 3)
			s.Flush(t.Context())
			s.Add(1, 4)
			s.Flush(t.Context())

			expect.Equal(t, c.sum.Load(), int64(7), "the Counter must receive each add once")
			expect.Equal(t, c.calls.Load(), int64(2), "each Flush must add its change in one call")
		})

		t.Run("calls the Counter not at all when the sum has not changed", func(t *testing.T) {
			t.Parallel()
			c := &summingCounter{}
			s := newSharded(t, c, 2)

			s.Add(0, 1)
			s.Flush(t.Context())
			s.Flush(t.Context())
			assert.Equal(t, c.calls.Load(), int64(1), "a Flush without a change must not call the Counter")
		})

		t.Run("adds every add once beside concurrent flushes", func(t *testing.T) {
			t.Parallel()
			c := &summingCounter{}
			s := newSharded(t, c, 4)

			outcomes := history.Concurrently(5, within, func(client int) (any, error) {
				if client == 4 {
					for range 100 {
						s.Flush(t.Context())
					}

					return client, nil
				}

				for range 1000 {
					s.Add(client, 1)
				}

				return client, nil
			})
			for _, o := range outcomes {
				expect.True(t, o.Finished, "every client must finish")
			}

			s.Flush(t.Context())
			assert.Equal(t, c.sum.Load(), int64(4000), "the Counter must receive every add exactly once")
		})
	})
}

// TestShardedCounterAllocs checks the allocation contract of Add and Flush
// over the no-op counter. MaxAllocs counts the allocations of the whole
// process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestShardedCounterAllocs(t *testing.T) {
	ctx := t.Context()
	s := newSharded(t, noop.Reporter{}.Counter(telemetry.InstrumentSpec{Name: "bench"}), 64)

	t.Run("Add", func(t *testing.T) {
		expect.MaxAllocs(t, func() { s.Add(7, 1) }, 0, "Add must not allocate")
	})

	t.Run("Flush", func(t *testing.T) {
		expect.MaxAllocs(t, func() {
			s.Add(3, 1)
			s.Flush(ctx)
		}, 0, "Flush must not allocate")
	})
}

// BenchmarkShardedCounter reports the cost of Add and Flush over the no-op
// counter, and fails when one allocates.
func BenchmarkShardedCounter(b *testing.B) {
	ctx := b.Context()
	s := newSharded(b, noop.Reporter{}.Counter(telemetry.InstrumentSpec{Name: "bench"}), 64)

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
			s.Flush(ctx)
		}
	})
}

// newSharded returns the ShardedCounter of c and cells, and fails tb when
// NewShardedCounter refuses them.
func newSharded(tb assert.TB, c telemetry.Counter, cells int) *telemetry.ShardedCounter {
	tb.Helper()

	s, err := telemetry.NewShardedCounter(c, cells)
	assert.NoError(tb, err, "NewShardedCounter must accept the arguments")

	return s
}
