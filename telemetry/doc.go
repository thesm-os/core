// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package telemetry defines the seams through which every thesmos
// library emits metrics and traces from a hot path.
//
// Library code constructs counters, gauges, histograms and spans
// through an injected [Reporter], and does not bind to OpenTelemetry,
// Prometheus or another backend at compile time. Tests inject a no-op
// reporter. A production deployment injects an adapter for
// OpenTelemetry or Prometheus that the consumer's module constructs.
//
// # Instrument lifecycle
//
// A metric instrument goes through three steps:
//
//   - Bind: [Reporter.Counter], [Reporter.Gauge] and [Reporter.Histogram]
//     resolve a named instrument, and [Counter.With], [Gauge.With] and
//     [Histogram.With] bind an attribute set for one time series. Both
//     may allocate.
//   - Emit: [Counter.Add], [Gauge.Set], [Gauge.Add] and
//     [Histogram.Record] run many times per request, and do not allocate
//     on any implementation in this module.
//   - Release: [Counter.Release], [Gauge.Release] and
//     [Histogram.Release] end a bound instrument when its caller stops
//     using the attribute set, so that an adapter can forget the set.
//
// Binding at initialisation keeps attribute resolution off the hot
// path: the adapter resolves the attributes once, not per call. The hot
// path is c.Add(ctx, 1), one method call without a slice to build or
// attributes to walk.
//
// # Overflow
//
// An adapter that caps the attribute sets of an instrument aggregates
// the measurements of every set beyond the cap into one overflow
// series. The overflow series of a counter or a histogram has a correct
// total, because their measurements add. [InstrumentSpec.Aggregation]
// states how the values of a gauge's attribute sets combine, and [Gauge]
// describes the overflow series of each aggregation.
//
// # ctx on the hot path
//
// [Counter.Add] and the other methods that emit take a
// [context.Context]. They do not use it for cancellation, because an
// atomic add does no I/O. Adapters read three values from it:
//
//   - The active OpenTelemetry span, so that an exemplar links a data
//     point of a metric to the trace that produced it.
//   - The W3C baggage entries, which add labels per request across
//     services.
//   - The trace ID, which correlates logs, metrics and traces in an
//     OpenTelemetry backend.
//
// Without ctx, exemplar correlation would end for every instrument.
// Passing ctx costs one interface value per call.
//
// # Tracing
//
// [Tracer.Start] returns a child [Span] for cold-path operations. Spans
// are outside the zero-allocation contract: creating a span, changing
// its attributes and recording its events allocate, because the
// OpenTelemetry SDK records into a span buffer, samplers may emit and
// exporters batch. Library code emits a span per request, not per
// iteration of a loop.
//
// # Provided implementations
//
//   - [go.thesmos.sh/core/telemetry/noop] discards every signal. Tests
//     and libraries that run outside an observability deployment use it.
//
// # Failure semantics
//
// A caller cannot act on a failure of telemetry: metric SDKs queue and
// drop on overflow, and exporters retry internally. No method of
// [Reporter], [Counter], [Gauge], [Histogram] or [Span] returns an
// error.
//
// A method that emits does not panic for any argument:
//
//   - [Counter.Add] discards a negative value, because a counter only
//     increases.
//   - The backend of an implementation defines how [Gauge.Set],
//     [Gauge.Add] and [Histogram.Record] treat NaN and an infinity, and
//     how [Histogram.Record] treats a negative value.
//
// An implementation may report such a value through its own
// diagnostics, such as a rate-limited log line.
//
// # Bounding the cost of a hot path
//
// Three types bound what a hot path spends on its telemetry:
//
//   - [ShardedCounter] spreads the adds to a [Counter] over padded cells
//     that the caller chooses, and adds their sum to the Counter on Flush,
//     so goroutines add without contending for one cache line.
//   - [BoundedHistogram] records every value of a failed call, and one
//     value in N of the calls that succeeded, N a power of two that Flush
//     recomputes from the rate that it measured.
//   - [RateLimitHandler] is a [slog.Handler] that passes one record per
//     interval of each message and subject value, passes every record at
//     [slog.LevelError] and above, and counts the records that it drops.
//
// Their constructors return [ErrConfig], classified
// [go.thesmos.sh/core/errs.Invalid], for an argument that they refuse.
//
// # Allocation contract
//
// These methods do not allocate:
//
//   - [Counter.Add], [Gauge.Set], [Gauge.Add] and [Histogram.Record],
//     on the hot path.
//   - [Counter.Release], [Gauge.Release] and [Histogram.Release].
//   - [ShardedCounter.Add], [ShardedCounter.Flush],
//     [BoundedHistogram.Record] and [BoundedHistogram.Flush], apart from
//     the instrument that they wrap.
//   - [RateLimitHandler.Handle], for an event that it remembers and a
//     subject value that is a string or an integer, apart from the
//     handler that it wraps.
//
// These methods may allocate, on a cold path: [Reporter.Counter],
// [Reporter.Gauge], [Reporter.Histogram], [Reporter.Tracer],
// [Counter.With], [Gauge.With], [Histogram.With], [Tracer.Start] and
// every method of [Span].
//
// The benchmarks of each implementation in this module check the
// contract through the plug-ins of the conformance suites in
// go.thesmos.sh/core/coretest/telemetrytest.
//
// # Bridging to log/slog
//
// [Attr.SlogAttr] converts a [telemetry.Attr] to a [slog.Attr] without
// boxing a primitive value. A consumer can build one attribute slice
// when it binds an instrument, and use it for the metric, the span and
// the log record of every request:
//
//	// Once per region, at initialisation:
//	attrs := []telemetry.Attr{
//	    telemetry.AttrString("operation", "create"),
//	    telemetry.AttrString("region", region),
//	}
//	requests := counter.With(attrs)
//	slogAttrs := make([]slog.Attr, len(attrs))
//	for i, a := range attrs {
//	    slogAttrs[i] = a.SlogAttr()
//	}
//
//	// On every request:
//	requests.Add(ctx, 1)
//	span.SetAttributes(attrs)
//	logger.LogAttrs(ctx, slog.LevelInfo, "request", slogAttrs...)
//
//	// When the region is retired:
//	requests.Release()
//
// # Dependency position
//
// Imports context, encoding/binary, errors, hash/maphash, log/slog, maps,
// math/bits, slices, sync, sync/atomic and time from the standard library,
// and go.thesmos.sh/core/cache, go.thesmos.sh/core/clock and
// go.thesmos.sh/core/errs from this module.
package telemetry
