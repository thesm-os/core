// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"testing"

	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// newHistogram returns a Histogram of a new no-op Reporter, for the
// contract assertions and the benchmark.
func newHistogram() telemetry.Histogram {
	return noop.New().Histogram(telemetry.InstrumentSpec{
		Name:   "n",
		Bounds: []float64{1, 10, 100},
	})
}

// --- testkit-driven contract layer ---

func TestNoopHistogramContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertHistogramContract(t, newHistogram,
		telemetrytest.HistogramContractAssertions()...,
	)
}

func BenchmarkNoopHistogram(b *testing.B) {
	telemetrytest.BenchmarkHistogramContract(b, newHistogram,
		telemetrytest.HistogramBenchOnRecord(bench.MutatorAllocsWithin[telemetry.Histogram, float64](1.0, 0)),
		telemetrytest.HistogramBenchOnWith(bench.PureAllocsWithin[telemetry.Histogram, telemetry.Histogram](0)),
		telemetrytest.HistogramBenchOnRelease(telemetrytest.ReleaseAllocsWithin(
			func(h telemetry.Histogram) telemetry.Histogram { return h.With(nil) }, 0)),
	)
}
