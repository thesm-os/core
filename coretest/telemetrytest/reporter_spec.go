// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
)

// canonicalSpec is the canonical [telemetry.InstrumentSpec] used
// across the Reporter contract assertions.
func canonicalSpec(name telemetry.InstrumentName) telemetry.InstrumentSpec {
	return telemetry.InstrumentSpec{
		Name:        name,
		Description: "test instrument",
		Unit:        "{ops}",
	}
}

// ReporterContractAssertions returns the assertions every
// [telemetry.Reporter] implementation must satisfy: each factory
// method returns a non-nil instrument, repeated calls with the
// same name are stable (re-resolution is allowed but the result
// must remain usable), and the Tracer factory returns a non-nil
// Tracer.
//
//	telemetrytest.AssertReporterContract(t, factory,
//	    telemetrytest.ReporterContractAssertions()...,
//	)
func ReporterContractAssertions() []ReporterOption {
	return []ReporterOption{
		ReporterCustom("Counter returns non-nil for a canonical spec", func(t *testing.T, r telemetry.Reporter) {
			c := r.Counter(canonicalSpec("test.counter"))
			testkit.True(t, c != nil, "Counter must return non-nil for a canonical spec")
		}),

		ReporterCustom("Gauge returns non-nil for a canonical spec", func(t *testing.T, r telemetry.Reporter) {
			g := r.Gauge(canonicalSpec("test.gauge"))
			testkit.True(t, g != nil, "Gauge must return non-nil for a canonical spec")
		}),

		ReporterCustom("Histogram returns non-nil for a canonical spec", func(t *testing.T, r telemetry.Reporter) {
			h := r.Histogram(canonicalSpec("test.histogram"))
			testkit.True(t, h != nil, "Histogram must return non-nil for a canonical spec")
		}),

		ReporterCustom("Tracer returns non-nil for a canonical name", func(t *testing.T, r telemetry.Reporter) {
			tr := r.Tracer(telemetry.InstrumentName("test.library"))
			testkit.True(t, tr != nil, "Tracer must return non-nil for a canonical instrumentation name")
		}),

		ReporterCustom("repeated Counter calls remain usable", func(t *testing.T, r telemetry.Reporter) {
			spec := canonicalSpec("test.counter.repeat")
			a := r.Counter(spec)
			b := r.Counter(spec)
			testkit.True(t, a != nil && b != nil,
				"both Counter resolutions must return non-nil instruments")
		}),
	}
}
