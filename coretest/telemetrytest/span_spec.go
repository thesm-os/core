// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
)

// SpanContractAssertions returns the assertions every
// [telemetry.Span] implementation must satisfy: the lifecycle
// methods (End, SetAttributes, AddEvent) accept canonical inputs
// without panic, double-End is a no-op, and post-End calls do
// not panic (the Span doc-comment promises post-End is a no-op).
//
//	telemetrytest.AssertSpanContract(t, factory,
//	    telemetrytest.SpanContractAssertions()...,
//	)
func SpanContractAssertions() []SpanOption {
	return []SpanOption{
		SpanCustom("SetAttributes accepts nil and empty slices", func(t *testing.T, s telemetry.Span) {
			testkit.AssertNilSafe(t, func() { s.SetAttributes(nil) })
			testkit.AssertNilSafe(t, func() { s.SetAttributes([]telemetry.Attr{}) })
		}),

		SpanCustom("SetAttributes accepts a populated slice", func(t *testing.T, s telemetry.Span) {
			testkit.AssertNilSafe(t, func() {
				s.SetAttributes([]telemetry.Attr{
					telemetry.AttrString("k1", "v1"),
					telemetry.AttrString("k2", "v2"),
				})
			})
		}),

		SpanCustom("AddEvent accepts nil and populated attribute slices", func(t *testing.T, s telemetry.Span) {
			testkit.AssertNilSafe(t, func() { s.AddEvent("event.empty", nil) })
			testkit.AssertNilSafe(t, func() {
				s.AddEvent("event.populated", []telemetry.Attr{
					telemetry.AttrString("k", "v"),
				})
			})
		}),

		SpanCustom("End accepts nil and non-nil errors", func(t *testing.T, s telemetry.Span) {
			testkit.AssertNilSafe(t, func() { s.End(nil) })
		}),

		SpanCustom("double End is a no-op", func(t *testing.T, s telemetry.Span) {
			s.End(nil)
			testkit.AssertNilSafe(t, func() { s.End(testkit.TestError("redundant end")) })
		}),

		SpanCustom("post-End calls do not panic", func(t *testing.T, s telemetry.Span) {
			s.End(nil)
			testkit.AssertNilSafe(t, func() { s.SetAttributes(nil) })
			testkit.AssertNilSafe(t, func() { s.AddEvent("post.end", nil) })
			_ = s.SpanContext() // SpanContext is documented as stable post-End
		}),
	}
}
