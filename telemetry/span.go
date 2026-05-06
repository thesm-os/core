// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

// TraceID identifies a distributed trace — one logical request
// spanning multiple processes. Opaque string; implementations
// choose the format (W3C 32-hex, UUID, etc.). The empty string
// is the conventional zero value meaning "no trace context."
type TraceID string

// SpanID identifies one measured operation within a trace. Opaque
// string; format is implementation-defined. The empty string is
// the conventional zero value meaning "no span."
type SpanID string

// SpanKind classifies the relationship between a [Span] and its
// parent. Mirrors the OpenTelemetry SpanKind enumeration.
type SpanKind uint8

// EventName is a typed string naming a span event.
// Use named [EventName] constants for the same reason as
// [InstrumentName].
type EventName string

// Span kinds.
const (
	// SpanKindUnspecified is the reserved zero value. Treated as
	// [SpanKindInternal] by [Tracer.Start] when no kind option is
	// supplied.
	SpanKindUnspecified SpanKind = iota

	// SpanKindInternal — operation internal to an application;
	// the default kind when no option is supplied.
	SpanKindInternal

	// SpanKindServer — operation handled by a server in response
	// to a remote request.
	SpanKindServer

	// SpanKindClient — operation that issues a request to a
	// remote service.
	SpanKindClient

	// SpanKindProducer — operation that publishes a message to a
	// broker or queue.
	SpanKindProducer

	// SpanKindConsumer — operation that consumes a message from a
	// broker or queue.
	SpanKindConsumer
)

// SpanContext carries the immutable identity of a [Span].
// Returned by [Span.SpanContext]; passed to logging or correlation
// code that wants to stamp the trace identity onto an external
// signal.
//
// # Allocation contract
//
// Value type; pass by value. Construction is zero-alloc.
type SpanContext struct {
	TraceID  TraceID
	SpanID   SpanID
	ParentID SpanID
	Kind     SpanKind
}

// Span is a mutable handle to a measured operation within a trace.
// Implementations record attributes, events, and status during the
// span's lifetime.
//
// # Lifetime
//
// A [Span] returned by [Tracer.Start] is open until [Span.End] is
// called exactly once. Calls to other [Span] methods after [Span.End]
// are no-ops; double-ending is a no-op (the second call's err is
// dropped).
//
// # Allocation contract
//
// Outside the zero-allocation contract — see [Tracer].
type Span interface {
	// End records the end of the span. When err is non-nil, the
	// span's status is set to error with err's message.
	End(err error)

	// SetAttributes records additional attributes on the span.
	// Multiple calls accumulate; later attributes do not replace
	// earlier ones with the same key by default (implementations
	// may dedupe per OTel SDK behaviour).
	//
	// Slice semantics follow [Counter.With] — the slice is taken
	// by reference; callers must not mutate it after the call.
	SetAttributes(attrs []Attr)

	// AddEvent records a named event with optional attributes at
	// the current time. Events are point-in-time annotations
	// within the span's lifetime (for example "cache.miss",
	// "retry.attempt"). Pass nil for attrs when the event needs
	// no attribute payload.
	AddEvent(name EventName, attrs []Attr)

	// SpanContext returns the span's identity. Useful for
	// stamping the trace and span IDs onto external signals (log
	// records, audit headers) for cross-signal correlation.
	SpanContext() SpanContext
}
