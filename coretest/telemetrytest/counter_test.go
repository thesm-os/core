// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest_test

import (
	"testing"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
)

// TestCounter checks that a counter of a Recorder passes the checks of the
// suite of telemetry.Counter.
func TestCounter(t *testing.T) {
	t.Parallel()

	telemetrytest.RunCounterSuite(t, func() telemetry.Counter {
		return telemetrytest.NewRecorder().Counter(telemetry.InstrumentSpec{Name: counterName})
	})
}
