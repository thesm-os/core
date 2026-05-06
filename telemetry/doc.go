// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package telemetry defines the metric and trace seams used by every
// thesmos library that needs to emit observation signals from a
// hot-path code path.
//
// The seam exists so library code can construct counters, gauges,
// histograms, and spans through an injected [Reporter] without
// binding to OpenTelemetry, Prometheus, or any other backend at
// compile time. Tests inject a noop reporter; production deployments
// inject an OTel- or Prometheus-backed adapter constructed in the
// consumer module.
//
// # Hot-path zero-allocation discipline
//
// Metric instruments separate two phases:
//
//   - Init: [Reporter.Counter] / [Reporter.Gauge] / [Reporter.Histogram]
//     resolve a named instrument, and [Counter.With] / [Gauge.With] /
//     [Histogram.With] pre-bind attributes for one specific
//     time-series. Both may allocate.
//   - Emit: [Counter.Add] / [Gauge.Set] / [Gauge.Add] /
//     [Histogram.Record] are called many times per request and must
//     be zero-allocation on every implementation in this module.
//
// Pre-binding attributes at init is what eliminates the dominant
// allocation cost under real OTel-grade adapters: attribute
// resolution happens once, not per call. The hot path is
// `c.Add(ctx, 1)` — one method call, no slice construction, no
// attribute walking.
//
// # ctx on the hot path
//
// [Counter.Add] and the other emission methods take a
// [context.Context]. ctx is not used for cancellation here — atomic
// adds have no I/O — but it carries observability state that
// adapters use:
//
//   - The active OpenTelemetry [Span], so adapters can attach
//     exemplars linking metric data points back to the traces that
//     produced them.
//   - W3C [baggage] entries, so adapters can enrich metrics with
//     per-request labels propagated across services.
//   - The trace ID, so logs / metrics / traces correlate in
//     OTel-shaped backends without manual stitching.
//
// Dropping ctx would save a single interface-pointer pass per call
// and permanently break exemplar correlation. The cost is paid; the
// capability is preserved.
//
// # Tracing
//
// [Tracer.Start] returns a child [Span] for cold-path operations.
// Spans are NOT subject to the zero-allocation contract — span
// creation, attribute mutation, and event recording all allocate by
// design (the OTel SDK records into a span buffer, samplers may
// emit, exporters batch). Library code emits a span per request,
// not per loop iteration.
//
// # Provided implementations
//
//   - [telemetry/noop] — discards every signal; suitable for tests
//     and for libraries running outside an observability deployment.
//
// # Failure semantics
//
// Telemetry emission cannot fail in a way callers can act on —
// metric SDKs queue and drop on overflow, exporters retry
// internally. No method on [Reporter], [Counter], [Gauge],
// [Histogram], or [Span] returns an error.
//
// # Allocation contract
//
// Hot-path methods that must be zero-allocation:
//
//   - [Counter.Add], [Gauge.Set], [Gauge.Add], [Histogram.Record].
//
// Init-path methods that may allocate (cold path):
//
//   - [Reporter.Counter], [Reporter.Gauge], [Reporter.Histogram],
//     [Reporter.Tracer], the various [Counter.With] /
//     [Gauge.With] / [Histogram.With] constructors,
//     [Tracer.Start], every [Span] method.
//
// Implementations carry a TestZeroAlloc suite enforcing the hot-path
// contract via [testing.AllocsPerRun].
//
// # Bridging to log/slog
//
// [Attr.SlogAttr] converts a [telemetry.Attr] to a [slog.Attr]
// without boxing primitive values. Consumers that want one
// attribute construction across metrics, traces, and logs build a
// [telemetry.Attr] slice once and pass it through:
//
//	attrs := []telemetry.Attr{
//	    telemetry.AttrString("user_id", uid),
//	    telemetry.AttrInt("retry", retries),
//	}
//	counter.With(attrs).Add(ctx, 1)
//	span.SetAttributes(attrs)
//	for i, a := range attrs {
//	    slogAttrs[i] = a.SlogAttr()
//	}
//	logger.LogAttrs(ctx, slog.LevelInfo, "request", slogAttrs...)
package telemetry
