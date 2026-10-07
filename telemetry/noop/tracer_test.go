// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"context"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// The names of the telemetry of the cases.
const (
	tracerName telemetry.InstrumentName = "test.lib"
	spanName   telemetry.SpanName       = "op"
	eventName  telemetry.EventName      = "event"
)

func TestNoopTracerContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertTracerContract(t, func() telemetry.Tracer { return noop.New().Tracer(tracerName) },
		telemetrytest.TracerContractAssertions()...)
}

func TestNoopSpanContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertSpanContract(t, newSpan, telemetrytest.SpanContractAssertions()...)
}

func TestTracer(t *testing.T) {
	t.Parallel()

	t.Run("Start", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the context that it receives", func(t *testing.T) {
			t.Parallel()

			type key struct{}

			ctx := context.WithValue(t.Context(), key{}, tracerName)
			got, _ := noop.New().Tracer(tracerName).Start(ctx, spanName)
			assert.Equal(t, got, ctx, "Start must return its context unchanged", assert.ByIdentity())
		})

		t.Run("returns a span", func(t *testing.T) {
			t.Parallel()
			_, got := noop.New().Tracer(tracerName).Start(t.Context(), spanName)
			assert.NotNil(t, got, "Start must return a span")
		})
	})

	t.Run("span", func(t *testing.T) {
		t.Parallel()

		t.Run("SpanContext", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the zero SpanContext", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, newSpan().SpanContext(), telemetry.SpanContext{},
					"a no-op span must have no trace identity")
			})
		})
	})
}

// TestTracerAllocs checks that no method of the tracer or its span
// allocates. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestTracerAllocs(t *testing.T) {
	ctx := t.Context()
	tr := noop.New().Tracer(tracerName)
	sp := newSpan()
	attrs := []telemetry.Attr{telemetry.AttrString("k", "v")}

	t.Run("Start", func(t *testing.T) {
		var got telemetry.Span
		expect.MaxAllocs(t, func() { _, got = tr.Start(ctx, spanName) }, 0, "Start must not allocate")
		assert.NotNil(t, got, "the test must measure a started span")
	})

	t.Run("span", func(t *testing.T) {
		t.Run("End", func(t *testing.T) {
			expect.MaxAllocs(t, func() { sp.End(nil) }, 0, "End must not allocate")
		})

		t.Run("SetAttributes", func(t *testing.T) {
			expect.MaxAllocs(t, func() { sp.SetAttributes(attrs) }, 0, "SetAttributes must not allocate")
		})

		t.Run("AddEvent", func(t *testing.T) {
			expect.MaxAllocs(t, func() { sp.AddEvent(eventName, attrs) }, 0, "AddEvent must not allocate")
		})

		t.Run("SpanContext", func(t *testing.T) {
			var got telemetry.SpanContext
			expect.MaxAllocs(t, func() { got = sp.SpanContext() }, 0, "SpanContext must not allocate")
			assert.Equal(t, got, telemetry.SpanContext{}, "the test must measure the zero SpanContext")
		})
	})
}

func BenchmarkNoopTracer(b *testing.B) {
	telemetrytest.BenchmarkTracerContract(b, func() telemetry.Tracer { return noop.New().Tracer(tracerName) })
}

func BenchmarkNoopSpan(b *testing.B) {
	telemetrytest.BenchmarkSpanContract(b, newSpan)
}

// newSpan returns a span that the no-op tracer starts, for the contract
// suite of a span and the cases of its methods.
func newSpan() telemetry.Span {
	_, sp := noop.New().Tracer(tracerName).Start(context.Background(), spanName)

	return sp
}
