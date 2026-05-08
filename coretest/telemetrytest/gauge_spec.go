// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
)

// GaugeContractAssertions returns the assertions every
// [telemetry.Gauge] implementation must satisfy: Set / Add accept
// canonical values without panic across the positive, zero, and
// negative ranges; With returns a non-nil derived instrument; the
// With chain composes; nil/empty attribute slices are tolerated.
//
//	telemetrytest.AssertGaugeContract(t, factory,
//	    telemetrytest.GaugeContractAssertions()...,
//	)
func GaugeContractAssertions() []GaugeOption {
	return []GaugeOption{
		GaugeCustom("Set accepts canonical values across the float64 range", func(t *testing.T, g telemetry.Gauge) {
			for _, v := range []float64{0, 1, -1, 1e9, -1e9} {
				testkit.AssertNilSafe(t, func() { g.Set(t.Context(), v) })
			}
		}),

		GaugeCustom("Add accepts positive, zero, and negative deltas", func(t *testing.T, g telemetry.Gauge) {
			for _, v := range []float64{0, 1, -1, 1e9, -1e9} {
				testkit.AssertNilSafe(t, func() { g.Add(t.Context(), v) })
			}
		}),

		GaugeCustom("With nil attributes returns a usable Gauge", func(t *testing.T, g telemetry.Gauge) {
			derived := g.With(nil)
			testkit.True(t, derived != nil, "With(nil) must return a non-nil Gauge")
			testkit.AssertNilSafe(t, func() { derived.Set(t.Context(), 1) })
		}),

		GaugeCustom("With empty attributes returns a usable Gauge", func(t *testing.T, g telemetry.Gauge) {
			derived := g.With([]telemetry.Attr{})
			testkit.True(t, derived != nil, "With([]) must return a non-nil Gauge")
			testkit.AssertNilSafe(t, func() { derived.Set(t.Context(), 1) })
		}),

		GaugeCustom("With chain composes — derived Gauge accepts further With", func(t *testing.T, g telemetry.Gauge) {
			a := g.With([]telemetry.Attr{telemetry.AttrString("k1", "v1")})
			b := a.With([]telemetry.Attr{telemetry.AttrString("k2", "v2")})
			testkit.True(t, b != nil, "chained With must return a non-nil Gauge")
			testkit.AssertNilSafe(t, func() {
				b.Set(t.Context(), 42)
				b.Add(t.Context(), 1)
			})
		}),
	}
}
