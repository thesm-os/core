// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
)

// HistogramContractAssertions returns the assertions every
// [telemetry.Histogram] implementation must satisfy: Record
// accepts canonical values without panic, With returns a non-nil
// derived instrument, the With chain composes, nil/empty
// attribute slices are tolerated.
//
//	telemetrytest.AssertHistogramContract(t, factory,
//	    telemetrytest.HistogramContractAssertions()...,
//	)
func HistogramContractAssertions() []HistogramOption {
	return []HistogramOption{
		HistogramCustom(
			"Record accepts canonical values across the float64 range",
			func(t *testing.T, h telemetry.Histogram) {
				for _, v := range []float64{0, 0.5, 1, 1e3, 1e9} {
					testkit.AssertNilSafe(t, func() { h.Record(t.Context(), v) })
				}
			},
		),

		HistogramCustom(
			"With nil attributes returns a usable Histogram",
			func(t *testing.T, h telemetry.Histogram) {
				derived := h.With(nil)
				testkit.True(t, derived != nil, "With(nil) must return a non-nil Histogram")
				testkit.AssertNilSafe(t, func() { derived.Record(t.Context(), 1) })
			},
		),

		HistogramCustom(
			"With empty attributes returns a usable Histogram",
			func(t *testing.T, h telemetry.Histogram) {
				derived := h.With([]telemetry.Attr{})
				testkit.True(t, derived != nil, "With([]) must return a non-nil Histogram")
				testkit.AssertNilSafe(t, func() { derived.Record(t.Context(), 1) })
			},
		),

		HistogramCustom(
			"With chain composes — derived Histogram accepts further With",
			func(t *testing.T, h telemetry.Histogram) {
				a := h.With([]telemetry.Attr{telemetry.AttrString("k1", "v1")})
				b := a.With([]telemetry.Attr{telemetry.AttrString("k2", "v2")})
				testkit.True(t, b != nil, "chained With must return a non-nil Histogram")
				testkit.AssertNilSafe(t, func() { b.Record(t.Context(), 42) })
			},
		),
	}
}
