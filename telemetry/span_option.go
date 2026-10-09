// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

// SpanOption configures a [Span] when [Tracer.Start] creates it:
//
//   - [WithSpanKind] sets the kind of the span, [SpanKindInternal] by
//     default.
//   - [WithRemoteParent] starts the span as the child of a context that a
//     [Propagator] extracted from another process.
//
// SpanOption is a value type, not a function, so [ApplySpanOptions] and
// [RemoteParent] read a slice of options without a closure call per option
// and without moving a value to the heap. The zero value of each field
// leaves its setting unset, so an option sets only the field of the
// function that made it.
type SpanOption struct {
	// parent is the remote parent of the new span when its TraceID is not
	// empty. The zero SpanContext leaves the parent unset.
	parent SpanContext

	// kind is the [SpanKind] of the new span when it is not
	// [SpanKindUnspecified], the zero value, which leaves the kind unset.
	kind SpanKind
}

// WithSpanKind sets the [SpanKind] of the new [Span]. Without it,
// [Tracer.Start] creates a span of [SpanKindInternal].
// [SpanKindUnspecified] leaves the kind unset.
//
// # Allocation contract
//
// Zero alloc.
func WithSpanKind(kind SpanKind) SpanOption {
	return SpanOption{kind: kind}
}

// WithRemoteParent starts the new [Span] as a child of sc, a context that a
// [Propagator] extracted from another process, in place of the span of the
// context that [Tracer.Start] receives. The new span continues the trace of
// sc, and its parent is the span of sc.
//
// A Tracer reads the parent with [RemoteParent]. A Tracer that does not
// read it starts the span under the span of its context, which starts a new
// trace in a server whose context has no span. An sc with an empty TraceID
// sets no parent.
//
// # Allocation contract
//
// Zero alloc. The option is a value of about 80 bytes. A slice of options
// passed to [Tracer.Start], an interface method, escapes to the heap as any
// variadic argument of an interface method does.
func WithRemoteParent(sc SpanContext) SpanOption {
	return SpanOption{parent: sc}
}

// ApplySpanOptions returns the [SpanKind] that opts set, for a [Tracer]
// that creates a span. It returns [SpanKindInternal] when no option sets a
// kind, and an option of [SpanKindUnspecified] sets none. When several
// options set a kind, the last of them applies.
//
// # Allocation contract
//
// Zero alloc. The loop reads each option by value, so the result does not
// move to the heap.
func ApplySpanOptions(opts []SpanOption) SpanKind {
	kind := SpanKindUnspecified
	for _, o := range opts {
		if o.kind != SpanKindUnspecified {
			kind = o.kind
		}
	}
	if kind == SpanKindUnspecified {
		return SpanKindInternal
	}
	return kind
}

// RemoteParent returns the remote parent that [WithRemoteParent] set in
// opts, and reports whether an option set one. When several options set a
// parent, the last of them applies. A [Tracer] calls it beside
// [ApplySpanOptions] when [Tracer.Start] creates a span.
//
// # Allocation contract
//
// Zero alloc.
func RemoteParent(opts []SpanOption) (SpanContext, bool) {
	var parent SpanContext
	for _, o := range opts {
		if o.parent.TraceID != "" {
			parent = o.parent
		}
	}

	return parent, parent.TraceID != ""
}
