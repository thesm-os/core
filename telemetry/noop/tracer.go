// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop

import (
	"context"

	"go.thesmos.sh/core/telemetry"
)

// tracer is the empty-struct no-op [telemetry.Tracer]. The zero
// value is the only useful value; stateless and safe for
// concurrent use.
type tracer struct{}

// Compile-time interface check.
var _ telemetry.Tracer = tracer{}

// Start returns the input context unchanged plus a no-op
// [telemetry.Span]. Span options are evaluated to honour any
// caller-side side effects but their resolved kind is discarded
// (the no-op span carries [telemetry.SpanKindUnspecified] in its
// returned [telemetry.SpanContext]).
func (tracer) Start(
	ctx context.Context,
	_ telemetry.SpanName,
	opts ...telemetry.SpanOption,
) (context.Context, telemetry.Span) {
	// Resolve options for caller-side side effects (rare; most
	// SpanOption values are pure). Discard the resolved kind —
	// no-op spans carry no identity.
	_ = telemetry.ApplySpanOptions(opts)
	return ctx, span{}
}

// span is the empty-struct no-op [telemetry.Span]. The zero value
// is the only useful value; stateless and safe for concurrent use.
type span struct{}

// Compile-time interface check.
var _ telemetry.Span = span{}

// End discards err.
func (span) End(error) {}

// SetAttributes discards attrs.
func (span) SetAttributes([]telemetry.Attr) {}

// AddEvent discards name and attrs.
func (span) AddEvent(telemetry.EventName, []telemetry.Attr) {}

// SpanContext returns the zero [telemetry.SpanContext] — no-op
// spans carry no trace identity.
func (span) SpanContext() telemetry.SpanContext { return telemetry.SpanContext{} }
