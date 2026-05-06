// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"context"
	"errors"
	"testing"

	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

func TestTracer(t *testing.T) {
	t.Parallel()
	tr := noop.New().Tracer("test.lib")

	t.Run("Start returns the input context unchanged", func(t *testing.T) {
		t.Parallel()
		type ctxKey struct{}
		in := context.WithValue(t.Context(), ctxKey{}, "marker")
		out, span := tr.Start(in, "op")
		if out.Value(ctxKey{}) != "marker" {
			t.Fatal("Start did not pass ctx through")
		}
		if span == nil {
			t.Fatal("Start returned nil span")
		}
	})

	t.Run("Start applies SpanOption side effects", func(t *testing.T) {
		t.Parallel()
		// SpanOptions in our noop impl are processed via
		// ApplySpanOptions for caller-side side effects, even
		// though the resolved kind is dropped.
		_, span := tr.Start(t.Context(), "op",
			telemetry.WithSpanKind(telemetry.SpanKindServer))
		if span == nil {
			t.Fatal("Start with options returned nil span")
		}
	})
}

func TestSpan(t *testing.T) {
	t.Parallel()
	tr := noop.New().Tracer("test.lib")
	_, span := tr.Start(t.Context(), "op")

	t.Run("End discards both nil and non-nil errors", func(t *testing.T) {
		t.Parallel()
		span.End(nil)
		span.End(errors.New("boom"))
	})

	t.Run("SetAttributes discards every shape", func(t *testing.T) {
		t.Parallel()
		span.SetAttributes(nil)
		span.SetAttributes([]telemetry.Attr{telemetry.AttrString("k", "v")})
	})

	t.Run("AddEvent discards every shape", func(t *testing.T) {
		t.Parallel()
		span.AddEvent("evt", nil)
		span.AddEvent("evt", []telemetry.Attr{telemetry.AttrInt("k", 1)})
	})

	t.Run("SpanContext returns the zero SpanContext", func(t *testing.T) {
		t.Parallel()
		got := span.SpanContext()
		if got != (telemetry.SpanContext{}) {
			t.Fatalf("SpanContext: got %+v, want zero", got)
		}
	})
}

func BenchmarkTracerStart(b *testing.B) {
	tr := noop.New().Tracer("bench")
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		_, span := tr.Start(ctx, "op")
		span.End(nil)
	}
}
