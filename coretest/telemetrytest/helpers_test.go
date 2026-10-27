// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest_test

import "go.thesmos.sh/core/telemetry"

// The names of the instruments, the tracer and the spans of the cases.
const (
	counterName   telemetry.InstrumentName = "test.counter"
	gaugeName     telemetry.InstrumentName = "test.gauge"
	histogramName telemetry.InstrumentName = "test.histogram"
	tracerName    telemetry.InstrumentName = "test.library"
	spanName      telemetry.SpanName       = "test.operation"
)
