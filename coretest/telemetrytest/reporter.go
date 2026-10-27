// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// The description and the unit of the instruments that the checks of a
// Reporter construct.
const (
	reporterDescription = "test instrument"
	reporterUnit        = "{ops}"
)

// reporterTracerName is the instrumentation name of the Tracer that a check of
// a Reporter constructs.
const reporterTracerName telemetry.InstrumentName = "test.library"

// The specs of the instruments that the checks of a Reporter construct.
var (
	reporterCounterSpec = telemetry.InstrumentSpec{
		Name:        "test.counter",
		Description: reporterDescription,
		Unit:        reporterUnit,
	}
	reporterGaugeSpec = telemetry.InstrumentSpec{
		Name:        "test.gauge",
		Description: reporterDescription,
		Unit:        reporterUnit,
	}
	reporterHistogramSpec = telemetry.InstrumentSpec{
		Name:        "test.histogram",
		Description: reporterDescription,
		Unit:        reporterUnit,
	}
	reporterRepeatedSpec = telemetry.InstrumentSpec{
		Name:        "test.counter.repeat",
		Description: reporterDescription,
		Unit:        reporterUnit,
	}
)

// ReporterCounterReturnsACounter checks that Counter returns a Counter.
//
//dokimi:check Reporter.Counter defect=reporterFirstCounterIsNil
func ReporterCounterReturnsACounter(h *ReporterHandle) {
	assert.NotNil(h, h.Subject().Counter(reporterCounterSpec), h.Statement())
}

// ReporterCounterReturnsACounterForAKnownName checks that the second Counter
// of one spec returns a Counter.
//
//dokimi:check Reporter.Counter defect=reporterSecondCounterIsNil
func ReporterCounterReturnsACounterForAKnownName(h *ReporterHandle) {
	r := h.Subject()
	r.Counter(reporterRepeatedSpec)
	assert.NotNil(h, r.Counter(reporterRepeatedSpec), h.Statement())
}

// ReporterGaugeReturnsAGauge checks that Gauge returns a Gauge.
//
//dokimi:check Reporter.Gauge defect=reporterGaugeIsNil
func ReporterGaugeReturnsAGauge(h *ReporterHandle) {
	assert.NotNil(h, h.Subject().Gauge(reporterGaugeSpec), h.Statement())
}

// ReporterHistogramReturnsAHistogram checks that Histogram returns a
// Histogram.
//
//dokimi:check Reporter.Histogram defect=reporterHistogramIsNil
func ReporterHistogramReturnsAHistogram(h *ReporterHandle) {
	assert.NotNil(h, h.Subject().Histogram(reporterHistogramSpec), h.Statement())
}

// ReporterTracerReturnsATracer checks that Tracer returns a Tracer.
//
//dokimi:check Reporter.Tracer defect=reporterTracerIsNil
func ReporterTracerReturnsATracer(h *ReporterHandle) {
	assert.NotNil(h, h.Subject().Tracer(reporterTracerName), h.Statement())
}

// reporterFirstCounterIsNil returns the planted defect of
// ReporterCounterReturnsACounter: a Reporter whose first Counter returns nil.
func reporterFirstCounterIsNil(h *ReporterHandle) telemetry.Reporter {
	sound := noop.New()
	var calls int

	return NewReporterStub(h, ReporterStubDelegateTo(sound),
		WithReporterCounter(func(spec telemetry.InstrumentSpec) telemetry.Counter {
			calls++
			if calls == 1 {
				return nil
			}

			return sound.Counter(spec)
		}))
}

// reporterSecondCounterIsNil returns the planted defect of
// ReporterCounterReturnsACounterForAKnownName: a Reporter whose second and
// later calls of Counter return nil.
func reporterSecondCounterIsNil(h *ReporterHandle) telemetry.Reporter {
	sound := noop.New()
	var calls int

	return NewReporterStub(h, ReporterStubDelegateTo(sound),
		WithReporterCounter(func(spec telemetry.InstrumentSpec) telemetry.Counter {
			calls++
			if calls > 1 {
				return nil
			}

			return sound.Counter(spec)
		}))
}

// reporterGaugeIsNil returns the planted defect of ReporterGaugeReturnsAGauge:
// a Reporter whose Gauge returns nil.
func reporterGaugeIsNil(h *ReporterHandle) telemetry.Reporter {
	return NewReporterStub(h, ReporterStubDelegateTo(noop.New()),
		WithReporterGauge(func(telemetry.InstrumentSpec) telemetry.Gauge { return nil }))
}

// reporterHistogramIsNil returns the planted defect of
// ReporterHistogramReturnsAHistogram: a Reporter whose Histogram returns nil.
func reporterHistogramIsNil(h *ReporterHandle) telemetry.Reporter {
	return NewReporterStub(h, ReporterStubDelegateTo(noop.New()),
		WithReporterHistogram(func(telemetry.InstrumentSpec) telemetry.Histogram { return nil }))
}

// reporterTracerIsNil returns the planted defect of ReporterTracerReturnsATracer:
// a Reporter whose Tracer returns nil.
func reporterTracerIsNil(h *ReporterHandle) telemetry.Reporter {
	return NewReporterStub(h, ReporterStubDelegateTo(noop.New()),
		WithReporterTracer(func(telemetry.InstrumentName) telemetry.Tracer { return nil }))
}
