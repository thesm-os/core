// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest_test

import (
	"testing"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
)

// TestSpan checks that a span of a Recorder passes the checks of the suite of
// telemetry.Span.
func TestSpan(t *testing.T) {
	t.Parallel()

	telemetrytest.RunSpanSuiteFromHandle(t, func(h *telemetrytest.SpanHandle) telemetry.Span {
		_, span := telemetrytest.NewRecorder().Tracer(tracerName).Start(h.Context(), spanName)

		return span
	})
}
