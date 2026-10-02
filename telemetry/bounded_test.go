// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

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

// recordingHistogram is a Histogram that keeps the values that it records.
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
func (h *recordingHistogram) With([]telemetry.Attr) telemetry.Histogram {
	return h
}

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
			want bool
		}{
			{name: "reports true for OutcomeSuccess", give: telemetry.OutcomeSuccess, want: true},
			{name: "reports true for OutcomeFailure", give: telemetry.OutcomeFailure, want: true},
			{name: "reports false for an Outcome after OutcomeFailure", give: telemetry.OutcomeFailure + 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the Outcome is a constant")
			})
		}
	})
}

func TestBoundedHistogram(t *testing.T) {
	t.Parallel()

	t.Run("NewBoundedHistogram", func(t *testing.T) {
		t.Parallel()

		refused := []struct {
			name      string
			histogram telemetry.Histogram
			clock     clock.Clock
			perSecond int
		}{
			{name: "returns ErrConfig for a nil Histogram", clock: fake.New(origin), perSecond: 1},
			{name: "returns ErrConfig for a nil clock", histogram: &recordingHistogram{}, perSecond: 1},
			{name: "returns ErrConfig for a rate of zero", histogram: &recordingHistogram{}, clock: fake.New(origin)},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := telemetry.NewBoundedHistogram(tt.histogram, tt.perSecond, tt.clock)
				testkit.ErrorIs(t, err, telemetry.ErrConfig, "NewBoundedHistogram must refuse the arguments")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}

		t.Run("returns a BoundedHistogram of a rate of one value per second", func(t *testing.T) {
			t.Parallel()
			_, err := telemetry.NewBoundedHistogram(&recordingHistogram{}, 1, fake.New(origin))
			testkit.NoError(t, err, "a rate of 1 must be valid")
		})
	})

	t.Run("Record", func(t *testing.T) {
		t.Parallel()

		t.Run("records every successful value before the first Flush", func(t *testing.T) {
			t.Parallel()
			h := &recordingHistogram{}
			b := newBounded(t, h, fake.New(origin))

			for v := range 50 {
				b.Record(t.Context(), float64(v), telemetry.OutcomeSuccess)
			}

			testkit.Equal(t, h.recorded(), 50, "N must be 1 until the first Flush")
		})

		t.Run("records two of eight successful values at an N of 4", func(t *testing.T) {
			t.Parallel()
			h, clk := &recordingHistogram{}, fake.New(origin)
			b := newBounded(t, h, clk)
			sampleAt(t, b, clk, 4)

			before := h.recorded()
			for v := range 8 {
				b.Record(t.Context(), float64(v), telemetry.OutcomeSuccess)
			}

			testkit.Equal(t, h.recorded()-before, 2, "Record must record one successful value in N")
		})

		failures := []struct {
			name    string
			outcome telemetry.Outcome
		}{
			{name: "records every value of a failed call", outcome: telemetry.OutcomeFailure},
			{name: "records every value of an Outcome that is not Valid", outcome: telemetry.OutcomeFailure + 1},
		}
		for _, tt := range failures {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				h, clk := &recordingHistogram{}, fake.New(origin)
				b := newBounded(t, h, clk)
				sampleAt(t, b, clk, 4)

				before := h.recorded()
				for v := range 3 {
					b.Record(t.Context(), float64(v), tt.outcome)
				}

				testkit.Equal(t, h.recorded()-before, 3, "Record must not sample the values of failures")
			})
		}
	})

	t.Run("Flush", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			successes int
			want      uint64
		}{
			{name: "returns 1 for no successful calls", successes: 0, want: 1},
			{name: "returns 1 for a rate below the configured rate", successes: perSecond, want: 1},
			{name: "returns 1 for the configured rate", successes: 2 * perSecond, want: 1},
			{name: "returns 2 for twice the configured rate", successes: 4 * perSecond, want: 2},
			{name: "returns 4 for a rate above twice the configured rate", successes: 4*perSecond + 1, want: 4},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				clk := fake.New(origin)
				b := newBounded(t, &recordingHistogram{}, clk)

				for range tt.successes {
					b.Record(t.Context(), 1, telemetry.OutcomeSuccess)
				}

				clk.Advance(interval)
				testkit.Equal(t, b.Flush(), tt.want, "Flush must return the smallest power of two that bounds the rate")
			})
		}

		t.Run("measures the rate since the previous Flush", func(t *testing.T) {
			t.Parallel()
			clk := fake.New(origin)
			b := newBounded(t, &recordingHistogram{}, clk)
			sampleAt(t, b, clk, 4)

			for range 2 * perSecond {
				b.Record(t.Context(), 1, telemetry.OutcomeSuccess)
			}

			clk.Advance(interval)
			testkit.Equal(t, b.Flush(), uint64(1), "the calls before the previous Flush must not count")
		})

		t.Run("returns the current N when the clock has not advanced", func(t *testing.T) {
			t.Parallel()
			clk := fake.New(origin)
			b := newBounded(t, &recordingHistogram{}, clk)
			sampleAt(t, b, clk, 4)

			testkit.Equal(t, b.Flush(), uint64(4), "Flush must keep N without elapsed time")
		})
	})
}

func BenchmarkBoundedHistogram(b *testing.B) {
	clk := fake.New(origin)
	histogram := noop.Reporter{}.Histogram(telemetry.InstrumentSpec{Name: "bench"})
	h := newBounded(b, histogram, clk)
	sampleAt(b, h, clk, 64)

	b.Run("Record of a successful call", func(b *testing.B) {
		allocs(b, func() { h.Record(b.Context(), 1, telemetry.OutcomeSuccess) })
	})

	b.Run("Record of a failed call", func(b *testing.B) {
		allocs(b, func() { h.Record(b.Context(), 1, telemetry.OutcomeFailure) })
	})

	b.Run("Flush", func(b *testing.B) {
		allocs(b, func() {
			clk.Advance(time.Millisecond)
			h.Flush()
		})
	})
}

// newBounded returns a BoundedHistogram of h at perSecond on clk, and fails
// tb when NewBoundedHistogram refuses them.
func newBounded(tb testing.TB, h telemetry.Histogram, clk *fake.Clock) *telemetry.BoundedHistogram {
	tb.Helper()

	b, err := telemetry.NewBoundedHistogram(h, perSecond, clk)
	testkit.NoError(tb, err, "NewBoundedHistogram must accept the arguments")

	return b
}

// sampleAt brings the N of b to n, a power of two, with successful calls
// at n times the rate over one interval of clk, and fails tb when Flush
// returns another N.
func sampleAt(tb testing.TB, b *telemetry.BoundedHistogram, clk *fake.Clock, n uint64) {
	tb.Helper()

	for range 2 * perSecond * n {
		b.Record(tb.Context(), 1, telemetry.OutcomeSuccess)
	}

	clk.Advance(interval)
	testkit.Equal(tb, b.Flush(), n, "Flush must set the N of the case")
}
