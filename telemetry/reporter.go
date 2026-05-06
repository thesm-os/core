// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

// Reporter is the unified telemetry seam. Library code constructs
// [Counter], [Gauge], [Histogram], and [Tracer] values through an
// injected Reporter and emits through them on the hot path; tests
// inject [telemetry/noop], production deployments inject an OTel-
// or Prometheus-backed adapter constructed in the consumer module.
//
// # Usage shape
//
// At init:
//
//	attrs := []telemetry.Attr{
//	    telemetry.AttrString("operation", "create"),
//	    telemetry.AttrString("region", region),
//	}
//	requestCounter := r.Counter(telemetry.InstrumentSpec{
//	    Name:        "thesmos.requests",
//	    Description: "Number of requests handled.",
//	    Unit:        "{request}",
//	}).With(attrs)
//
// On the hot path:
//
//	requestCounter.Add(ctx, 1)
//
// Pre-binding attributes via [Counter.With] at init eliminates the
// dominant allocation cost under real OTel-grade adapters. The hot
// path is one method call carrying ctx and an int64; ctx preserves
// exemplar and baggage correlation.
//
// # Concurrency
//
// Implementations must be safe for concurrent use; consumers share
// one Reporter and its instruments across many goroutines. The
// returned [Span] from [Tracer.Start] is single-goroutine.
type Reporter interface {
	// Counter returns a counter instrument matching spec.
	// Subsequent calls with the same [InstrumentSpec.Name] on the
	// same Reporter return the same underlying instrument.
	Counter(spec InstrumentSpec) Counter

	// Gauge returns a gauge instrument matching spec.
	Gauge(spec InstrumentSpec) Gauge

	// Histogram returns a histogram instrument matching spec.
	// [InstrumentSpec.Bounds], if non-nil, supplies the explicit
	// bucket boundary set; otherwise the implementation's default
	// schema applies.
	Histogram(spec InstrumentSpec) Histogram

	// Tracer returns a [Tracer] scoped under the given
	// instrumentation name. The name is the library identifier
	// (typically the consumer's import path) that observability
	// backends use to attribute spans to a producer.
	Tracer(name InstrumentName) Tracer
}
