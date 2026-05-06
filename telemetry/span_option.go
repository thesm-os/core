// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

// SpanOption configures a [Span] at creation time. Use
// [WithSpanKind] to override the default [SpanKindInternal].
//
// SpanOption is a value type rather than a function-typed
// closure so [Tracer.Start] / [ApplySpanOptions] can iterate
// the slice without forcing the loop's accumulator to escape
// to the heap. Future options add fields to this struct
// directly; the kind field's zero value ([SpanKindUnspecified])
// means "this option does not set kind" and is ignored by
// [ApplySpanOptions].
type SpanOption struct {
	// kind, when non-zero, sets the [SpanKind] for the new
	// [Span]. [SpanKindUnspecified] (the zero value) means the
	// option does not set kind.
	kind SpanKind
}

// WithSpanKind sets the [SpanKind] on the new [Span]. When no
// kind option is supplied, [Tracer.Start] treats the kind as
// [SpanKindInternal].
func WithSpanKind(kind SpanKind) SpanOption {
	return SpanOption{kind: kind}
}

// ApplySpanOptions reduces a slice of [SpanOption] values to
// the configured [SpanKind]. Exposed for [Tracer]
// implementations to share a single canonical option-resolution
// path.
//
// Returns [SpanKindInternal] when no kind option is supplied,
// or when every supplied option carries [SpanKindUnspecified]
// (the per-option sentinel for "does not set kind"). When
// multiple options set kind, the last one wins.
//
// # Allocation contract
//
// Zero-allocation: the loop reads each option's value-type
// fields without taking pointers, so escape analysis keeps
// the local accumulator on the stack.
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
