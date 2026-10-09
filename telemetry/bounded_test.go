// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"context"
	"math"
	"math/bits"
	"sync"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// perSecond is the rate of the BoundedHistograms of the cases.
const perSecond = 10

// interval is the time between two flushes of the cases.
const interval = 2 * time.Second

// origin is the time of the fake clocks of the cases.
var origin = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// factors generates the sampling factors N to which the cases of Record
// bring a BoundedHistogram.
var factors = prop.SampledFrom[uint64](1, 2, 4, 8, 16, 32, 64)

// recordingHistogram is a Histogram that keeps the values that it records.
// Its methods are safe for concurrent use.
type recordingHistogram struct {
	mu     sync.Mutex
	values []float64
}

var _ telemetry.Histogram = (*recordingHistogram)(nil)

// Record appends value to the recorded values.
func (h *recordingHistogram) Record(_ context.Context, value float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.values = append(h.values, value)
}

// With returns h.
func (h *recordingHistogram) With([]telemetry.Attr) telemetry.Histogram { return h }

// Release does nothing.
func (*recordingHistogram) Release() {}

// recorded returns the number of recorded values.
func (h *recordingHistogram) recorded() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return len(h.values)
}

func TestOutcome(t *testing.T) {
	t.Parallel()

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give telemetry.Outcome
		}{
			{name: "reports true for OutcomeSuccess", give: telemetry.OutcomeSuccess},
			{name: "reports true for OutcomeFailure", give: telemetry.OutcomeFailure},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.True(t, tt.give.Valid(), "Valid must report true for a constant of the package")
			})
		}

		t.Run("reports false for every Outcome after OutcomeFailure", func(t *testing.T) {
			t.Parallel()
			prop.False(t, telemetry.Outcome.Valid, "Valid must report false for an Outcome that is not a constant",
				prop.Using(prop.Integer(telemetry.OutcomeFailure+1, math.MaxUint8)))
		})
	})
}

func TestBoundedHistogram(t *testing.T) {
	t.Parallel()

	t.Run("NewBoundedHistogram", func(t *testing.T) {
		t.Parallel()

		refused := []struct {
			histogram telemetry.Histogram
			clock     clock.Clock
			name      string
		}{
			{name: "returns ErrConfig for a nil Histogram", clock: fake.New(origin)},
			{name: "returns ErrConfig for a nil clock", histogram: &recordingHistogram{}},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := telemetry.NewBoundedHistogram(tt.histogram, 1, tt.clock)
				expect.ErrorIs(t, err, telemetry.ErrConfig, "NewBoundedHistogram must refuse the arguments")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}

		t.Run("returns ErrConfig for a rate below 1", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(rate int) error {
				_, err := telemetry.NewBoundedHistogram(&recordingHistogram{}, rate, fake.New(origin))

				return err
			}, telemetry.ErrConfig, "NewBoundedHistogram must refuse a rate below 1",
				prop.Using(prop.Integer(math.MinInt, 0)), prop.Example(0))
		})

		t.Run("returns a BoundedHistogram of a rate of one value per second", func(t *testing.T) {
			t.Parallel()
			_, err := telemetry.NewBoundedHistogram(&recordingHistogram{}, 1, fake.New(origin))
			assert.NoError(t, err, "NewBoundedHistogram must accept a rate of 1")
		})
	})

	t.Run("Record", func(t *testing.T) {
		t.Parallel()

		t.Run("records every successful value before the first Flush", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(calls int) int {
				h := &recordingHistogram{}
				b := newBounded(t, h, fake.New(origin))
				for v := range calls {
					b.Record(t.Context(), float64(v), telemetry.OutcomeSuccess)
				}

				return h.recorded()
			}, func(calls int) int { return calls }, "N must be 1 until the first Flush",
				prop.Using(prop.Integer(0, 200)))
		})

		t.Run("records one successful value in N", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Record must record one successful value in N", func(c *prop.Case) {
				n := c.Draw(factors, "N")
				calls := c.Draw(prop.Integer(0, 200), "calls")
				h, clk := &recordingHistogram{}, fake.New(origin)
				b := newBounded(c, h, clk)
				sampleAt(c.Context(), c, b, clk, n)

				before := h.recorded()
				for v := range calls {
					b.Record(c.Context(), float64(v), telemetry.OutcomeSuccess)
				}

				assert.Equal(c, uint64(h.recorded()-before), uint64(calls)/n, "Record must record every Nth success")
			})
		})

		t.Run("records every value of an Outcome other than OutcomeSuccess", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Record must record every value of a call that did not succeed", func(c *prop.Case) {
				outcome := c.Draw(prop.Integer(telemetry.OutcomeFailure, math.MaxUint8), "outcome")
				n := c.Draw(factors, "N")
				calls := c.Draw(prop.Integer(0, 50), "calls")
				h, clk := &recordingHistogram{}, fake.New(origin)
				b := newBounded(c, h, clk)
				sampleAt(c.Context(), c, b, clk, n)

				before := h.recorded()
				for v := range calls {
					b.Record(c.Context(), float64(v), outcome)
				}

				assert.Equal(c, h.recorded()-before, calls, "Record must not sample the values of failures")
			})
		})
	})

	t.Run("Flush", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the smallest power of two that keeps the rate within the configured rate", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(successes int) uint64 {
				clk := fake.New(origin)
				b := newBounded(t, &recordingHistogram{}, clk)
				for range successes {
					b.Record(t.Context(), 1, telemetry.OutcomeSuccess)
				}

				clk.Advance(interval)

				return b.Flush()
			}, func(successes int) uint64 {
				ratio := math.Ceil(float64(successes) / interval.Seconds() / perSecond)
				if ratio <= 1 {
					return 1
				}

				return 1 << bits.Len64(uint64(ratio)-1)
			}, "Flush must return the smallest power of two that bounds the rate",
				prop.Using(prop.Integer(0, 10_000)), prop.Example(0), prop.Example(perSecond),
				prop.Example(2*perSecond), prop.Example(4*perSecond), prop.Example(4*perSecond+1))
		})

		t.Run("measures the rate since the previous Flush", func(t *testing.T) {
			t.Parallel()
			clk := fake.New(origin)
			b := newBounded(t, &recordingHistogram{}, clk)
			sampleAt(t.Context(), t, b, clk, 4)

			for range 2 * perSecond {
				b.Record(t.Context(), 1, telemetry.OutcomeSuccess)
			}

			clk.Advance(interval)
			assert.Equal(t, b.Flush(), uint64(1), "the calls before the previous Flush must not count")
		})

		t.Run("returns the current N when the clock has not advanced", func(t *testing.T) {
			t.Parallel()
			clk := fake.New(origin)
			b := newBounded(t, &recordingHistogram{}, clk)
			sampleAt(t.Context(), t, b, clk, 4)

			assert.Equal(t, b.Flush(), uint64(4), "Flush must keep N without elapsed time")
		})
	})
}

// TestBoundedHistogramAllocs checks the allocation contract of Record and
// Flush over the no-op histogram. MaxAllocs counts the allocations of the
// whole process, so the test does not run in parallel.
func TestBoundedHistogramAllocs(t *testing.T) {
	ctx := t.Context()
	clk := fake.New(origin)
	h := newBounded(t, noop.Reporter{}.Histogram(telemetry.InstrumentSpec{Name: "bench"}), clk)
	sampleAt(ctx, t, h, clk, 64)

	t.Run("Record", func(t *testing.T) {
		t.Run("of a successful call", func(t *testing.T) {
			expect.MaxAllocs(t, func() { h.Record(ctx, 1, telemetry.OutcomeSuccess) }, 0, "Record must not allocate")
		})

		t.Run("of a failed call", func(t *testing.T) {
			expect.MaxAllocs(t, func() { h.Record(ctx, 1, telemetry.OutcomeFailure) }, 0, "Record must not allocate")
		})
	})

	t.Run("Flush", func(t *testing.T) {
		var got uint64
		expect.MaxAllocs(t, func() {
			clk.Advance(time.Millisecond)
			got = h.Flush()
		}, 0, "Flush must not allocate")
		assert.NotEqual(t, got, 0, "the test must measure a Flush that returns N")
	})
}

// BenchmarkBoundedHistogram reports the cost of Record and Flush over the
// no-op histogram, and fails when one allocates.
func BenchmarkBoundedHistogram(b *testing.B) {
	ctx := b.Context()
	clk := fake.New(origin)
	h := newBounded(b, noop.Reporter{}.Histogram(telemetry.InstrumentSpec{Name: "bench"}), clk)
	sampleAt(ctx, b, h, clk, 64)

	b.Run("Record", func(b *testing.B) {
		b.Run("of a successful call", func(b *testing.B) {
			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				h.Record(ctx, 1, telemetry.OutcomeSuccess)
			}
		})

		b.Run("of a failed call", func(b *testing.B) {
			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				h.Record(ctx, 1, telemetry.OutcomeFailure)
			}
		})
	})

	b.Run("Flush", func(b *testing.B) {
		var got uint64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			clk.Advance(time.Millisecond)
			got = h.Flush()
		}

		assert.NotEqual(b, got, 0, "the benchmark must measure a Flush that returns N")
	})
}

// newBounded returns a BoundedHistogram of h at perSecond on clk, and fails
// tb when NewBoundedHistogram refuses them.
func newBounded(tb assert.TB, h telemetry.Histogram, clk *fake.Clock) *telemetry.BoundedHistogram {
	tb.Helper()

	b, err := telemetry.NewBoundedHistogram(h, perSecond, clk)
	assert.NoError(tb, err, "NewBoundedHistogram must accept the arguments")

	return b
}

// sampleAt brings the N of b to n, a power of two, with successful calls
// at n times the rate over one interval of clk, and fails tb when Flush
// returns another N.
func sampleAt(ctx context.Context, tb assert.TB, b *telemetry.BoundedHistogram, clk *fake.Clock, n uint64) {
	tb.Helper()

	for range 2 * perSecond * n {
		b.Record(ctx, 1, telemetry.OutcomeSuccess)
	}

	clk.Advance(interval)
	assert.Equal(tb, b.Flush(), n, "Flush must set the N of the case")
}
