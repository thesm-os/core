// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"context"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// newTracer is the SUT factory for the testkit-driven Tracer
// contract suite.
func newTracer() telemetry.Tracer {
	return noop.New().Tracer("test.lib")
}

// newSpan is the SUT factory for the testkit-driven Span contract
// suite — each invocation returns a fresh span via the noop tracer.
func newSpan() telemetry.Span {
	_, span := newTracer().Start(context.Background(), "op")
	return span
}

// --- testkit-driven contract layer ---

func TestNoopTracerContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertTracerContract(t, newTracer,
		telemetrytest.TracerContractAssertions()...,
	)
}

func TestNoopSpanContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertSpanContract(t, newSpan,
		telemetrytest.SpanContractAssertions()...,
	)
}

func BenchmarkNoopTracer(b *testing.B) {
	telemetrytest.BenchmarkTracerContract(b, newTracer)
}

func BenchmarkNoopSpan(b *testing.B) {
	telemetrytest.BenchmarkSpanContract(b, newSpan)
}

// --- noop-specific tests ---

func TestTracerPassesContextThrough(t *testing.T) {
	t.Parallel()
	tr := newTracer()

	type ctxKey struct{}
	in := context.WithValue(t.Context(), ctxKey{}, "marker")
	out, span := tr.Start(in, "op")
	testkit.Equal(t, out.Value(ctxKey{}), any("marker"),
		"Start must pass the input context through unchanged")
	testkit.True(t, span != nil, "Start must return a non-nil span")
}

func TestSpanContextReturnsZero(t *testing.T) {
	t.Parallel()
	span := newSpan()
	testkit.Equal(t, span.SpanContext(), telemetry.SpanContext{},
		"noop Span.SpanContext must return the zero SpanContext")
}
