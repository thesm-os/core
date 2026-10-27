// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest_test

import (
	"testing"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
)

// TestHistogram checks that a histogram of a Recorder passes the checks of the
// suite of telemetry.Histogram.
func TestHistogram(t *testing.T) {
	t.Parallel()

	telemetrytest.RunHistogramSuite(t, func() telemetry.Histogram {
		return telemetrytest.NewRecorder().Histogram(telemetry.InstrumentSpec{Name: histogramName})
	})
}
