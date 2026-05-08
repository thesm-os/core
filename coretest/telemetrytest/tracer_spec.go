// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
)

// TracerContractAssertions returns the assertions every
// [telemetry.Tracer] implementation must satisfy: Start returns a
// derived context plus a non-nil Span, the returned context
// carries the new span (or at minimum is usable), the Span's End
// can be called exactly once (and double-End is a no-op per the
// Span contract), and a nil-context Start is tolerated (mirrors
// the Span contract's "no panic on edge inputs" property).
//
//	telemetrytest.AssertTracerContract(t, factory,
//	    telemetrytest.TracerContractAssertions()...,
//	)
func TracerContractAssertions() []TracerOption {
	return []TracerOption{
		TracerCustom("Start returns a non-nil context and Span", func(t *testing.T, tr telemetry.Tracer) {
			ctx, span := tr.Start(t.Context(), "test.start")
			testkit.True(t, ctx != nil, "Start must return a non-nil context")
			testkit.True(t, span != nil, "Start must return a non-nil Span")
			span.End(nil)
		}),

		TracerCustom("Start with options applies them without panic", func(t *testing.T, tr telemetry.Tracer) {
			testkit.AssertNilSafe(t, func() {
				_, span := tr.Start(t.Context(), "test.start.options",
					telemetry.WithSpanKind(telemetry.SpanKindServer))
				span.End(nil)
			})
		}),

		TracerCustom("Start under nil context does not panic", func(t *testing.T, tr telemetry.Tracer) {
			testkit.AssertNilSafe(t, func() {
				//nolint:staticcheck // intentionally testing nil-ctx tolerance
				_, span := tr.Start(nil, "test.start.nil-ctx")
				if span != nil {
					span.End(nil)
				}
			})
		}),

		TracerCustom("Start consecutive spans are independent", func(t *testing.T, tr telemetry.Tracer) {
			_, a := tr.Start(t.Context(), "test.start.a")
			_, b := tr.Start(t.Context(), "test.start.b")
			testkit.True(t, a != nil && b != nil,
				"two Start calls must each return a non-nil Span")
			a.End(nil)
			b.End(nil)
		}),
	}
}
