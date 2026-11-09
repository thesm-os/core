// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"context"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// The names of the spans that the checks of a Tracer start.
const (
	tracerStartName      telemetry.SpanName = "test.start"
	tracerOptionsName    telemetry.SpanName = "test.start.options"
	tracerNilContextName telemetry.SpanName = "test.start.nil-ctx"
	tracerFirstName      telemetry.SpanName = "test.start.a"
	tracerSecondName     telemetry.SpanName = "test.start.b"
)

// tracerSoundName is the instrumentation name of the no-op Tracers that keep
// the calls of a planted defect that do not break its law.
const tracerSoundName telemetry.InstrumentName = "test.sound"

// tracerLinks are the links of the option of TracerStartAppliesItsOptions:
// one link to a span of another trace.
var tracerLinks = []telemetry.Link{{SpanContext: telemetry.SpanContext{
	TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
	SpanID:  "00f067aa0ba902b7",
}}}

// TracerStartReturnsNonNilResults checks that Start returns a context and a
// span that are not nil.
//
//dokimi:check Tracer.Start defect=tracerStartReturnsANilContext
func TracerStartReturnsNonNilResults(h *TracerHandle) {
	ctx, span := h.Subject().Start(h.Context(), tracerStartName)
	assert.NotNil(h, ctx, h.Statement())
	assert.NotNil(h, span, h.Statement())
	span.End(nil)
}

// TracerStartAppliesItsOptions checks that Start with a span kind option and a
// link option and End of its span return without a panic.
//
//dokimi:check Tracer.Start defect=tracerStartPanicsForAnOption
func TracerStartAppliesItsOptions(h *TracerHandle) {
	tr := h.Subject()
	assert.NotPanics(h, func() {
		_, span := tr.Start(h.Context(), tracerOptionsName,
			telemetry.WithSpanKind(telemetry.SpanKindServer), telemetry.WithLinks(tracerLinks))
		span.End(nil)
	}, h.Statement())
}

// TracerStartReturnsAContextWithItsSpan checks that the context of Start has
// the span of Start, for a span whose SpanContext is not zero.
//
//dokimi:check Tracer.Start defect=tracerStartDropsItsSpan
func TracerStartReturnsAContextWithItsSpan(h *TracerHandle) {
	ctx, span := h.Subject().Start(h.Context(), tracerStartName)
	defer span.End(nil)
	if span.SpanContext() == (telemetry.SpanContext{}) {
		return
	}

	got, ok := telemetry.SpanFromContext(ctx)
	assert.True(h, ok, h.Statement())
	assert.Equal(h, got.SpanContext(), span.SpanContext(), h.Statement())
}

// TracerStartAcceptsANilContext checks that Start of a nil context returns
// without a panic.
//
//dokimi:check Tracer.Start defect=tracerStartPanicsForANilContext
func TracerStartAcceptsANilContext(h *TracerHandle) {
	tr := h.Subject()
	assert.NilContextSafe(h, func(ctx context.Context) error {
		if _, span := tr.Start(ctx, tracerNilContextName); span != nil {
			span.End(nil)
		}

		return nil
	}, h.Statement())
}

// TracerSecondStartReturnsASpan checks that the second of two calls of Start
// returns a span.
//
//dokimi:check Tracer.Start defect=tracerSecondSpanIsNil
func TracerSecondStartReturnsASpan(h *TracerHandle) {
	tr := h.Subject()
	_, first := tr.Start(h.Context(), tracerFirstName)
	_, second := tr.Start(h.Context(), tracerSecondName)
	assert.NotNil(h, second, h.Statement())
	first.End(nil)
	second.End(nil)
}

// tracerStartReturnsANilContext returns the planted defect of
// TracerStartReturnsNonNilResults: a Tracer whose Start returns a nil context.
func tracerStartReturnsANilContext(h *TracerHandle) telemetry.Tracer {
	sound := noop.New().Tracer(tracerSoundName)

	return NewTracerStub(h, TracerStubDelegateTo(sound), WithTracerStart(
		func(ctx context.Context, name telemetry.SpanName, opts ...telemetry.SpanOption) (context.Context, telemetry.Span) {
			_, span := sound.Start(ctx, name, opts...)

			return nil, span
		},
	))
}

// tracerStartDropsItsSpan returns the planted defect of
// TracerStartReturnsAContextWithItsSpan: a Tracer whose Start returns the
// context that it receives with the span of a Recorder.
func tracerStartDropsItsSpan(h *TracerHandle) telemetry.Tracer {
	recording := NewRecorder().Tracer(tracerSoundName)

	return NewTracerStub(h, TracerStubDelegateTo(recording), WithTracerStart(
		func(ctx context.Context, name telemetry.SpanName, opts ...telemetry.SpanOption) (context.Context, telemetry.Span) {
			_, span := recording.Start(ctx, name, opts...)

			return ctx, span
		},
	))
}

// tracerStartPanicsForAnOption returns the planted defect of
// TracerStartAppliesItsOptions: a Tracer whose Start panics for an option.
func tracerStartPanicsForAnOption(h *TracerHandle) telemetry.Tracer {
	sound := noop.New().Tracer(tracerSoundName)

	return NewTracerStub(h, TracerStubDelegateTo(sound), WithTracerStart(
		func(ctx context.Context, name telemetry.SpanName, opts ...telemetry.SpanOption) (context.Context, telemetry.Span) {
			if len(opts) > 0 {
				panic("telemetrytest: Start panics for an option")
			}

			return sound.Start(ctx, name)
		},
	))
}

// tracerStartPanicsForANilContext returns the planted defect of
// TracerStartAcceptsANilContext: a Tracer whose Start panics for a nil
// context.
func tracerStartPanicsForANilContext(h *TracerHandle) telemetry.Tracer {
	sound := noop.New().Tracer(tracerSoundName)

	return NewTracerStub(h, TracerStubDelegateTo(sound), WithTracerStart(
		func(ctx context.Context, name telemetry.SpanName, opts ...telemetry.SpanOption) (context.Context, telemetry.Span) {
			if ctx == nil {
				panic("telemetrytest: Start panics for a nil context")
			}

			return sound.Start(ctx, name, opts...)
		},
	))
}

// tracerSecondSpanIsNil returns the planted defect of
// TracerSecondStartReturnsASpan: a Tracer whose second and later calls of
// Start return a nil span.
func tracerSecondSpanIsNil(h *TracerHandle) telemetry.Tracer {
	sound := noop.New().Tracer(tracerSoundName)
	var calls int

	return NewTracerStub(h, TracerStubDelegateTo(sound), WithTracerStart(
		func(ctx context.Context, name telemetry.SpanName, opts ...telemetry.SpanOption) (context.Context, telemetry.Span) {
			calls++
			if calls > 1 {
				return ctx, nil
			}

			return sound.Start(ctx, name, opts...)
		},
	))
}
