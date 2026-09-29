// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
)

// HistogramContractAssertions returns the assertions that every
// [telemetry.Histogram] implementation satisfies:
//
//   - Record returns for a non-negative value.
//   - With returns a usable Histogram for nil attributes, for empty
//     attributes and when called on a bound Histogram.
//   - Release returns for a bound Histogram and for a released one.
//   - Release leaves an unbound Histogram usable.
//   - Record returns for a released Histogram.
//   - Release leaves the Histogram that its With returned usable.
//   - Release runs concurrently with Record on the same Histogram
//     without a data race.
//
// A test of an implementation runs them:
//
//	telemetrytest.AssertHistogramContract(t, factory,
//	    telemetrytest.HistogramContractAssertions()...,
//	)
func HistogramContractAssertions() []HistogramOption {
	return []HistogramOption{
		HistogramCustom(
			"Record returns for a non-negative value",
			func(t *testing.T, h telemetry.Histogram) {
				for _, v := range []float64{0, 0.5, 1, 1e3, 1e9} {
					testkit.AssertNilSafe(t, func() { h.Record(t.Context(), v) })
				}
			},
		),

		HistogramCustom(
			"With returns a usable Histogram for nil attributes",
			func(t *testing.T, h telemetry.Histogram) {
				derived := h.With(nil)
				testkit.True(t, derived != nil, "With(nil) must return a non-nil Histogram")
				testkit.AssertNilSafe(t, func() { derived.Record(t.Context(), 1) })
			},
		),

		HistogramCustom(
			"With returns a usable Histogram for empty attributes",
			func(t *testing.T, h telemetry.Histogram) {
				derived := h.With([]telemetry.Attr{})
				testkit.True(t, derived != nil, "With([]) must return a non-nil Histogram")
				testkit.AssertNilSafe(t, func() { derived.Record(t.Context(), 1) })
			},
		),

		HistogramCustom(
			"With returns a usable Histogram when called on a bound Histogram",
			func(t *testing.T, h telemetry.Histogram) {
				a := h.With([]telemetry.Attr{telemetry.AttrString("k1", "v1")})
				b := a.With([]telemetry.Attr{telemetry.AttrString("k2", "v2")})
				testkit.True(t, b != nil, "chained With must return a non-nil Histogram")
				testkit.AssertNilSafe(t, func() { b.Record(t.Context(), 42) })
			},
		),

		HistogramCustom("Release returns for a bound Histogram", func(t *testing.T, h telemetry.Histogram) {
			testkit.AssertNilSafe(t, h.With(releaseAttrs).Release)
		}),

		HistogramCustom("Release returns for a released Histogram", func(t *testing.T, h telemetry.Histogram) {
			bound := h.With(releaseAttrs)
			bound.Release()
			testkit.AssertNilSafe(t, bound.Release)
		}),

		HistogramCustom("Release leaves an unbound Histogram usable", func(t *testing.T, h telemetry.Histogram) {
			testkit.AssertNilSafe(t, h.Release)
			testkit.AssertNilSafe(t, func() { h.Record(t.Context(), 1) })
		}),

		HistogramCustom("Record returns for a released Histogram", func(t *testing.T, h telemetry.Histogram) {
			bound := h.With(releaseAttrs)
			bound.Release()
			testkit.AssertNilSafe(t, func() { bound.Record(t.Context(), 1) })
		}),

		HistogramCustom(
			"Release leaves the Histogram that its With returned usable",
			func(t *testing.T, h telemetry.Histogram) {
				parent := h.With(releaseAttrs)
				child := parent.With([]telemetry.Attr{telemetry.AttrString("k2", "v2")})
				parent.Release()
				testkit.AssertNilSafe(t, func() { child.Record(t.Context(), 1) })
			},
		),

		HistogramCustom(
			"Release runs concurrently with Record on the same Histogram",
			func(t *testing.T, h telemetry.Histogram) {
				ctx := t.Context()
				bound := h.With(releaseAttrs)
				releaseWhileEmitting(bound.Release, func(i int) { bound.Record(ctx, float64(i)) })
			},
		),
	}
}
