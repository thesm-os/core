// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

// SpanOption configures a [Span] at creation time. Use
// [WithSpanKind] to override the default [SpanKindInternal].
type SpanOption func(*spanConfig)

// spanConfig collects [SpanOption] values for delivery to a
// [Tracer.Start] implementation.
type spanConfig struct {
	kind SpanKind
}

// WithSpanKind sets the [SpanKind] on the new [Span]. When no
// kind option is supplied, [Tracer.Start] treats the kind as
// [SpanKindInternal].
func WithSpanKind(kind SpanKind) SpanOption {
	return func(c *spanConfig) { c.kind = kind }
}

// ApplySpanOptions reduces a slice of [SpanOption] values to the
// configured [SpanKind]. Exposed for [Tracer] implementations to
// share a single canonical option-resolution path.
//
// Returns [SpanKindInternal] when no kind option is supplied —
// the default per [WithSpanKind].
func ApplySpanOptions(opts []SpanOption) SpanKind {
	var cfg spanConfig
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.kind == SpanKindUnspecified {
		return SpanKindInternal
	}
	return cfg.kind
}
