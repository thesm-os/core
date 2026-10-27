// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"context"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// The names of the events that the checks of a Span add.
const (
	spanEmptyEventName     telemetry.EventName = "event.empty"
	spanPopulatedEventName telemetry.EventName = "event.populated"
	spanLateEventName      telemetry.EventName = "post.end"
	spanSoundName          telemetry.SpanName  = "test.sound"
)

// spanAttrs are the attributes that the checks of a Span set.
var spanAttrs = []telemetry.Attr{telemetry.AttrString("k1", "v1"), telemetry.AttrString("k2", "v2")}

// SpanSetAttributesAcceptsNoAttributes checks that SetAttributes of nil and of
// an empty slice returns without a panic.
//
//dokimi:check Span.SetAttributes defect=spanSetAttributesPanicsForAnEmptySlice
func SpanSetAttributesAcceptsNoAttributes(h *SpanHandle) {
	s := h.Subject()
	assert.NotPanics(h, func() { s.SetAttributes(nil) }, h.Statement())
	assert.NotPanics(h, func() { s.SetAttributes([]telemetry.Attr{}) }, h.Statement())
}

// SpanSetAttributesAcceptsAttributes checks that SetAttributes of two
// attributes returns without a panic.
//
//dokimi:check Span.SetAttributes defect=spanSetAttributesPanicsForAttributes
func SpanSetAttributesAcceptsAttributes(h *SpanHandle) {
	s := h.Subject()
	assert.NotPanics(h, func() { s.SetAttributes(spanAttrs) }, h.Statement())
}

// SpanAddEventAcceptsAttributes checks that AddEvent of an event without
// attributes and of an event with attributes returns without a panic.
//
//dokimi:check Span.AddEvent defect=spanAddEventPanicsForAttributes
func SpanAddEventAcceptsAttributes(h *SpanHandle) {
	s := h.Subject()
	assert.NotPanics(h, func() { s.AddEvent(spanEmptyEventName, nil) }, h.Statement())
	assert.NotPanics(h, func() { s.AddEvent(spanPopulatedEventName, spanAttrs) }, h.Statement())
}

// SpanEndAcceptsANilError checks that End(nil) returns without a panic.
//
//dokimi:check Span.End defect=spanEndPanicsForANilError
func SpanEndAcceptsANilError(h *SpanHandle) {
	s := h.Subject()
	assert.NotPanics(h, func() { s.End(nil) }, h.Statement())
}

// SpanSecondEndReturns checks that a second End returns without a panic.
//
//dokimi:check Span.End defect=spanSecondEndPanics
func SpanSecondEndReturns(h *SpanHandle) {
	s := h.Subject()
	s.End(context.Canceled)
	assert.NotPanics(h, func() { s.End(context.Canceled) }, h.Statement())
}

// SpanMethodsReturnAfterEnd checks that SetAttributes and AddEvent of an ended
// span return without a panic.
//
//dokimi:check Span.SetAttributes defect=spanSetAttributesPanicsAfterEnd
func SpanMethodsReturnAfterEnd(h *SpanHandle) {
	s := h.Subject()
	s.End(context.Canceled)
	assert.NotPanics(h, func() { s.SetAttributes(nil) }, h.Statement())
	assert.NotPanics(h, func() { s.AddEvent(spanLateEventName, nil) }, h.Statement())
	s.SpanContext()
}

// spanSetAttributesPanicsForAnEmptySlice returns the planted defect of
// SpanSetAttributesAcceptsNoAttributes: a span whose SetAttributes panics for
// an empty slice that is not nil.
func spanSetAttributesPanicsForAnEmptySlice(h *SpanHandle) telemetry.Span {
	return NewSpanStub(h, SpanStubDelegateTo(soundSpan(h)), WithSpanSetAttributes(func(attrs []telemetry.Attr) {
		if attrs != nil && len(attrs) == 0 {
			panic("telemetrytest: SetAttributes panics for an empty slice")
		}
	}))
}

// spanSetAttributesPanicsForAttributes returns the planted defect of
// SpanSetAttributesAcceptsAttributes: a span whose SetAttributes panics for
// attributes.
func spanSetAttributesPanicsForAttributes(h *SpanHandle) telemetry.Span {
	return NewSpanStub(h, SpanStubDelegateTo(soundSpan(h)), WithSpanSetAttributes(func(attrs []telemetry.Attr) {
		if len(attrs) > 0 {
			panic("telemetrytest: SetAttributes panics for attributes")
		}
	}))
}

// spanAddEventPanicsForAttributes returns the planted defect of
// SpanAddEventAcceptsAttributes: a span whose AddEvent panics for attributes.
func spanAddEventPanicsForAttributes(h *SpanHandle) telemetry.Span {
	return NewSpanStub(h, SpanStubDelegateTo(soundSpan(h)),
		WithSpanAddEvent(func(_ telemetry.EventName, attrs []telemetry.Attr) {
			if len(attrs) > 0 {
				panic("telemetrytest: AddEvent panics for attributes")
			}
		}))
}

// spanEndPanicsForANilError returns the planted defect of
// SpanEndAcceptsANilError: a span whose End panics for a nil error.
func spanEndPanicsForANilError(h *SpanHandle) telemetry.Span {
	return NewSpanStub(h, SpanStubDelegateTo(soundSpan(h)), WithSpanEnd(func(err error) {
		if err == nil {
			panic("telemetrytest: End panics for a nil error")
		}
	}))
}

// spanSecondEndPanics returns the planted defect of SpanSecondEndReturns: a
// span whose second End panics.
func spanSecondEndPanics(h *SpanHandle) telemetry.Span {
	var ended bool

	return NewSpanStub(h, SpanStubDelegateTo(soundSpan(h)), WithSpanEnd(func(error) {
		if ended {
			panic("telemetrytest: a second End panics")
		}
		ended = true
	}))
}

// spanSetAttributesPanicsAfterEnd returns the planted defect of
// SpanMethodsReturnAfterEnd: a span whose SetAttributes panics after End.
func spanSetAttributesPanicsAfterEnd(h *SpanHandle) telemetry.Span {
	var ended bool

	return NewSpanStub(h, SpanStubDelegateTo(soundSpan(h)),
		WithSpanEnd(func(error) { ended = true }),
		WithSpanSetAttributes(func([]telemetry.Attr) {
			if ended {
				panic("telemetrytest: SetAttributes panics after End")
			}
		}))
}

// soundSpan returns a span of a no-op Tracer, which keeps the calls of a
// planted defect that do not break its law.
func soundSpan(h *SpanHandle) telemetry.Span {
	_, span := noop.New().Tracer(tracerSoundName).Start(h.Context(), spanSoundName)

	return span
}
