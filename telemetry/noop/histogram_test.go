// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// histogramSpec is the specification of the histograms of the cases.
var histogramSpec = telemetry.InstrumentSpec{Name: "n", Bounds: []float64{1, 10, 100}}

// TestHistogramConformsToHistogram applies the checks of the suite of
// telemetry.Histogram to the histogram of a Reporter.
func TestHistogramConformsToHistogram(t *testing.T) {
	t.Parallel()

	telemetrytest.RunHistogramSuite(t, func() telemetry.Histogram { return noop.New().Histogram(histogramSpec) })
}

// TestHistogramAllocs checks that no method of the histogram allocates.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
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
