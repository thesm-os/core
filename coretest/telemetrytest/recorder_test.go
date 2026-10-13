// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest_test

import (
	"math"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
)

// The instruments of the cases.
const (
	counterName   telemetry.InstrumentName = "test.counter"
	gaugeName     telemetry.InstrumentName = "test.gauge"
	histogramName telemetry.InstrumentName = "test.histogram"
)

// The attributes of the cases.
var (
	region = telemetry.AttrString("region", "eu")
	tier   = telemetry.AttrInt("tier", 2)
)

func TestRecorderReporterContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertReporterContract(t, func() telemetry.Reporter { return telemetrytest.NewRecorder() },
		telemetrytest.ReporterContractAssertions()...)
}

func TestRecorderCounterContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertCounterContract(t, func() telemetry.Counter {
		return telemetrytest.NewRecorder().Counter(telemetry.InstrumentSpec{Name: counterName})
	}, telemetrytest.CounterContractAssertions()...)
}

func TestRecorderGaugeContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertGaugeContract(t, func() telemetry.Gauge {
		return telemetrytest.NewRecorder().Gauge(telemetry.InstrumentSpec{Name: gaugeName})
	}, telemetrytest.GaugeContractAssertions()...)
}

func TestRecorderHistogramContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertHistogramContract(t, func() telemetry.Histogram {
		return telemetrytest.NewRecorder().Histogram(telemetry.InstrumentSpec{Name: histogramName})
	}, telemetrytest.HistogramContractAssertions()...)
}

func TestRecorder(t *testing.T) {
	t.Parallel()

	t.Run("Counter", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps each value of Add in the series of the empty attribute set", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			c := r.Counter(telemetry.InstrumentSpec{Name: counterName})
			c.Add(t.Context(), 1)
			c.Add(t.Context(), 2)
			assert.Equal(t, r.Values(counterName), []float64{1, 2}, "the series must keep each value of Add")
		})

		t.Run("discards a negative value", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			c := r.Counter(telemetry.InstrumentSpec{Name: counterName})
			c.Add(t.Context(), -1)
			c.Add(t.Context(), math.MinInt64)
			c.Add(t.Context(), 3)
			assert.Equal(t, r.Values(counterName), []float64{3}, "the series must not keep a negative value")
		})

		t.Run("returns the instrument of a name that an earlier call constructed", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			r.Counter(telemetry.InstrumentSpec{Name: counterName}).Add(t.Context(), 1)
			r.Counter(telemetry.InstrumentSpec{Name: counterName}).Add(t.Context(), 2)
			assert.Equal(t, r.Values(counterName), []float64{1, 2},
				"both counters must keep their values in one series")
		})
	})

	t.Run("Gauge", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the value of the gauge after each Set and Add", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			g := r.Gauge(telemetry.InstrumentSpec{Name: gaugeName})
			g.Set(t.Context(), 5)
			g.Add(t.Context(), 2)
			g.Add(t.Context(), -3)
			assert.Equal(t, r.Values(gaugeName), []float64{5, 7, 4}, "the series must keep the value after each call")
		})

		t.Run("gives each gauge that With returns a value of its own", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			g := r.Gauge(telemetry.InstrumentSpec{Name: gaugeName})
			a, b := g.With([]telemetry.Attr{region}), g.With([]telemetry.Attr{region})
			a.Add(t.Context(), 1)
			b.Add(t.Context(), 1)
			a.Add(t.Context(), 1)
			assert.Equal(t, r.Values(gaugeName, region), []float64{1, 1, 2},
				"the series must keep the value of each gauge after its call")
		})
	})

	t.Run("Histogram", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps each value of Record", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			h := r.Histogram(telemetry.InstrumentSpec{Name: histogramName})
			h.Record(t.Context(), 0.5)
			h.Record(t.Context(), 2)
			assert.Equal(t, r.Values(histogramName), []float64{0.5, 2}, "the series must keep each value of Record")
		})
	})

	t.Run("Specs", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the first spec of each name in the order of its first construction", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			first := telemetry.InstrumentSpec{Name: counterName, Unit: "{request}", Description: "Requests."}
			second := telemetry.InstrumentSpec{Name: histogramName, Unit: "s", Bounds: []float64{0.1, 1}}
			r.Counter(first)
			r.Histogram(second)
			r.Counter(telemetry.InstrumentSpec{Name: counterName, Unit: "By"})
			assert.Equal(t, r.Specs(), []telemetry.InstrumentSpec{first, second},
				"Specs must return the first spec of each name")
		})
	})

	t.Run("Values", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the values of the series of the attribute set in any order", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			c := r.Counter(telemetry.InstrumentSpec{Name: counterName})
			c.With([]telemetry.Attr{region, tier}).Add(t.Context(), 4)
			c.With([]telemetry.Attr{tier}).Add(t.Context(), 5)
			expect.Equal(t, r.Values(counterName, tier, region), []float64{4},
				"Values must find the series of the attributes in another order")
			expect.Equal(t, r.Values(counterName, tier), []float64{5}, "Values must keep each attribute set apart")
			expect.Nil(t, r.Values(counterName), "the empty attribute set must have no series")
		})

		t.Run("returns nil for a name without an instrument", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			r.Counter(telemetry.InstrumentSpec{Name: counterName}).Add(t.Context(), 1)
			assert.Nil(t, r.Values(gaugeName), "Values must return nil for a name without an instrument")
		})

		t.Run("returns no value that a bound instrument emits after its Release", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			bound := r.Histogram(telemetry.InstrumentSpec{Name: histogramName}).With([]telemetry.Attr{region})
			bound.Record(t.Context(), 1)
			bound.Release()
			bound.Record(t.Context(), 2)
			assert.Equal(t, r.Values(histogramName, region), []float64{1}, "a released instrument must keep no value")
		})

		t.Run("returns the values of an instrument of a Recorder method after its Release", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			g := r.Gauge(telemetry.InstrumentSpec{Name: gaugeName})
			g.Release()
			g.Set(t.Context(), 1)
			assert.Equal(t, r.Values(gaugeName), []float64{1},
				"Release must not end the instrument of a Recorder method")
		})
	})

	t.Run("Series", func(t *testing.T) {
		t.Parallel()

		t.Run("returns each attribute set in the order of its first binding", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			c := r.Counter(telemetry.InstrumentSpec{Name: counterName})
			c.With([]telemetry.Attr{tier, region}).Release()
			c.With([]telemetry.Attr{tier})
			c.With([]telemetry.Attr{region, tier})
			assert.Equal(t, r.Series(counterName), [][]telemetry.Attr{{region, tier}, {tier}},
				"Series must return each attribute set once, sorted by key, a released one included")
		})

		t.Run("returns nil for a name without an instrument", func(t *testing.T) {
			t.Parallel()
			assert.Nil(t, telemetrytest.NewRecorder().Series(counterName),
				"Series must return nil for a name without an instrument")
		})
	})

	t.Run("Bound", func(t *testing.T) {
		t.Parallel()

		t.Run("counts each instrument that With returned for the attribute set in any order", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			g := r.Gauge(telemetry.InstrumentSpec{Name: gaugeName})
			g.With([]telemetry.Attr{region, tier})
			g.With([]telemetry.Attr{tier, region})
			g.With([]telemetry.Attr{tier})
			assert.Equal(t, r.Bound(gaugeName, tier, region), 2,
				"Bound must count the two instruments of the attribute set")
		})

		t.Run("does not count an instrument after its Release", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			c := r.Counter(telemetry.InstrumentSpec{Name: counterName})
			a, b := c.With([]telemetry.Attr{region}), c.With([]telemetry.Attr{region})
			a.Release()
			expect.Equal(t, r.Bound(counterName, region), 1, "Bound must not count the released instrument")
			b.Release()
			expect.Equal(t, r.Bound(counterName, region), 0, "Bound must count no instrument once both are released")
		})

		t.Run("ignores a second Release of one instrument", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			h := r.Histogram(telemetry.InstrumentSpec{Name: histogramName})
			a := h.With([]telemetry.Attr{region})
			h.With([]telemetry.Attr{region})
			a.Release()
			a.Release()
			assert.Equal(t, r.Bound(histogramName, region), 1,
				"a second Release of one instrument must not end the other instrument")
		})

		t.Run("does not count an instrument of a Recorder method", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			g := r.Gauge(telemetry.InstrumentSpec{Name: gaugeName})
			g.Set(t.Context(), 1)
			g.Release()
			assert.Equal(t, r.Bound(gaugeName), 0, "Bound must count only the instruments that With returned")
		})

		t.Run("returns 0 for a name without an instrument", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			r.Counter(telemetry.InstrumentSpec{Name: counterName}).With([]telemetry.Attr{region})
			assert.Equal(t, r.Bound(gaugeName, region), 0, "Bound must return 0 for a name without an instrument")
		})

		t.Run("returns 0 for an attribute set without a series", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			r.Counter(telemetry.InstrumentSpec{Name: counterName}).With([]telemetry.Attr{region})
			assert.Equal(t, r.Bound(counterName, tier), 0,
				"Bound must return 0 for an attribute set without a series")
		})
	})

	t.Run("keeps every value of emits on many goroutines", func(t *testing.T) {
		t.Parallel()
		const clients, adds = 4, 100
		r := telemetrytest.NewRecorder()
		c := r.Counter(telemetry.InstrumentSpec{Name: counterName}).With([]telemetry.Attr{region})
		outcomes := history.Concurrently(clients, time.Minute, func(int) (any, error) {
			for range adds {
				c.Add(t.Context(), 1)
			}

			return nil, nil
		})
		for _, o := range outcomes {
			expect.True(t, o.Finished, "every client must finish")
		}
		assert.Length(t, r.Values(counterName, region), clients*adds, "the series must keep every value")
	})
}
