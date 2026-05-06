// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import "context"

// SpanName is a typed string naming a span operation, passed to
// [Tracer.Start]. Distinct from [InstrumentName] (a library
// identifier, stable per library) and [EventName] (a point-in-time
// event within a span) — span names are operation-scoped, typically
// per-call (for example "ledger.append.batch", "kernel.turn").
//
// The three name types share a string base but live in different
// vocabularies; the typed distinction prevents call sites from
// confusing them.
type SpanName string

// Tracer creates [Span] values for cold-path operations.
//
// Tracing is NOT subject to the zero-allocation contract: span
// creation, attribute mutation, and event recording all allocate
// by design — the OTel SDK records into a span buffer, samplers
// may emit, exporters batch. Library code emits a span per
// request, not per loop iteration.
type Tracer interface {
	// Start begins a new span as a child of the span carried in
	// ctx (or as a root span if ctx carries no span). Returns a
	// derived context carrying the new span and the span handle
	// itself.
	//
	// Callers MUST call [Span.End] when the operation completes,
	// typically via defer:
	//
	//	ctx, span := tracer.Start(ctx, "my.op")
	//	defer span.End(nil)
	Start(
		ctx context.Context,
		name SpanName,
		opts ...SpanOption,
	) (context.Context, Span)
}
