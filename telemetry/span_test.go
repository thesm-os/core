// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"context"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
)

// valueKey is the key of a value that a context keeps beside its span.
type valueKey struct{}

func TestSpan(t *testing.T) {
	t.Parallel()

	t.Run("ContextWithSpan", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a context whose span is s", func(t *testing.T) {
			t.Parallel()
			s := telemetrytest.NewSpanStub(t)
			got, ok := telemetry.SpanFromContext(telemetry.ContextWithSpan(t.Context(), s))
			assert.True(t, ok, "the context must have a span")
			assert.Equal[telemetry.Span](t, got, s, "the span of the context must be s", assert.ByIdentity())
		})

		t.Run("keeps the values of ctx", func(t *testing.T) {
			t.Parallel()
			ctx := context.WithValue(t.Context(), valueKey{}, "kept")
			got := telemetry.ContextWithSpan(ctx, telemetrytest.NewSpanStub(t)).Value(valueKey{})
			assert.Equal[any](t, got, "kept", "the context must keep the values of ctx")
		})

		t.Run("returns a child of the background context for a nil ctx", func(t *testing.T) {
			t.Parallel()
			s := telemetrytest.NewSpanStub(t)
			ctx := telemetry.ContextWithSpan(nil, s) //nolint:staticcheck // the case passes a nil context
			got, ok := telemetry.SpanFromContext(ctx)
			assert.True(t, ok, "the context of a nil ctx must have a span")
			assert.Equal[telemetry.Span](t, got, s, "the span of the context must be s", assert.ByIdentity())
		})
	})

	t.Run("SpanFromContext", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the span of an ancestor", func(t *testing.T) {
			t.Parallel()
			s := telemetrytest.NewSpanStub(t)
			ctx := context.WithValue(telemetry.ContextWithSpan(t.Context(), s), valueKey{}, "child")
			got, ok := telemetry.SpanFromContext(ctx)
			assert.True(t, ok, "the child must have the span of its parent")
			assert.Equal[telemetry.Span](t, got, s, "the span must be the span of the parent", assert.ByIdentity())
		})

		t.Run("returns the span of the latest ContextWithSpan", func(t *testing.T) {
			t.Parallel()
			first, second := telemetrytest.NewSpanStub(t), telemetrytest.NewSpanStub(t)
			ctx := telemetry.ContextWithSpan(telemetry.ContextWithSpan(t.Context(), first), second)
			got, ok := telemetry.SpanFromContext(ctx)
			assert.True(t, ok, "the context must have a span")
			assert.Equal[telemetry.Span](t, got, second, "the span must be the second span", assert.ByIdentity())
		})

		t.Run("reports false for a context without a span", func(t *testing.T) {
			t.Parallel()
			got, ok := telemetry.SpanFromContext(t.Context())
			expect.False(t, ok, "a context without a span must have no span")
			expect.Nil(t, got, "SpanFromContext must return no span")
		})

		t.Run("reports false for a nil context", func(t *testing.T) {
			t.Parallel()
			got, ok := telemetry.SpanFromContext(nil) //nolint:staticcheck // the case passes a nil context
			expect.False(t, ok, "a nil context must have no span")
			expect.Nil(t, got, "SpanFromContext must return no span")
		})
	})
}

// TestSpanAllocs checks that SpanFromContext does not allocate. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
func TestSpanAllocs(t *testing.T) {
	t.Run("SpanFromContext", func(t *testing.T) {
		ctx := telemetry.ContextWithSpan(t.Context(), telemetrytest.NewSpanStub(t))
		var ok bool
		expect.MaxAllocs(t, func() { _, ok = telemetry.SpanFromContext(ctx) }, 0,
			"SpanFromContext must not allocate")
		assert.True(t, ok, "the test must measure a context with a span")
	})
}
