// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest_test

import (
	"testing"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
)

// TestTracer checks that a tracer of a Recorder passes the checks of the suite
// of telemetry.Tracer.
func TestTracer(t *testing.T) {
	t.Parallel()

	telemetrytest.RunTracerSuite(t, func() telemetry.Tracer { return telemetrytest.NewRecorder().Tracer(tracerName) })
}
