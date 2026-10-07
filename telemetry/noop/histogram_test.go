// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// histogramSpec is the specification of the histograms of the cases.
var histogramSpec = telemetry.InstrumentSpec{Name: "n", Bounds: []float64{1, 10, 100}}

func TestNoopHistogramContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertHistogramContract(t,
		func() telemetry.Histogram { return noop.New().Histogram(histogramSpec) },
		telemetrytest.HistogramContractAssertions()...)
}

// TestHistogramAllocs checks that no method of the histogram allocates.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestHistogramAllocs(t *testing.T) {
	ctx := t.Context()
	h := noop.New().Histogram(histogramSpec)
	attrs := []telemetry.Attr{telemetry.AttrString("k", "v")}

	t.Run("Record", func(t *testing.T) {
		expect.MaxAllocs(t, func() { h.Record(ctx, 1) }, 0, "Record must not allocate")
	})

	t.Run("With", func(t *testing.T) {
		var got telemetry.Histogram
		expect.MaxAllocs(t, func() { got = h.With(attrs) }, 0, "With must not allocate")
		assert.Equal(t, got, h, "With must return the receiver")
	})

	t.Run("Release", func(t *testing.T) {
		expect.MaxAllocs(t, func() { h.Release() }, 0, "Release must not allocate")
	})
}

func BenchmarkNoopHistogram(b *testing.B) {
	telemetrytest.BenchmarkHistogramContract(b,
		func() telemetry.Histogram { return noop.New().Histogram(histogramSpec) },
		telemetrytest.HistogramBenchOnRecord(bench.MutatorAllocsWithin[telemetry.Histogram, float64](1.0, 0)),
		telemetrytest.HistogramBenchOnWith(bench.PureAllocsWithin[telemetry.Histogram, telemetry.Histogram](0)),
		telemetrytest.HistogramBenchOnRelease(telemetrytest.ReleaseAllocsWithin(
			func(h telemetry.Histogram) telemetry.Histogram { return h.With(nil) }, 0,
		)),
	)
}
