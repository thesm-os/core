// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest_test

import (
	"testing"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
)

// TestGauge checks that a gauge of a Recorder passes the checks of the suite
// of telemetry.Gauge.
func TestGauge(t *testing.T) {
	t.Parallel()

	telemetrytest.RunGaugeSuite(t, func() telemetry.Gauge {
		return telemetrytest.NewRecorder().Gauge(telemetry.InstrumentSpec{Name: gaugeName})
	})
}
