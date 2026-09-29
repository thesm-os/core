// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
)

// GaugeContractAssertions returns the assertions that every
// [telemetry.Gauge] implementation satisfies:
//
//   - Set and Add return for a value of any sign.
//   - With returns a usable Gauge for nil attributes, for empty
//     attributes and when called on a bound Gauge.
//   - Release returns for a bound Gauge and for a released one.
//   - Release leaves an unbound Gauge usable.
//   - Set and Add return for a released Gauge.
//   - Release leaves the Gauge that its With returned usable.
//   - Release runs concurrently with Set or Add on the same Gauge
//     without a data race.
//
// A test of an implementation runs them:
//
//	telemetrytest.AssertGaugeContract(t, factory,
//	    telemetrytest.GaugeContractAssertions()...,
//	)
func GaugeContractAssertions() []GaugeOption {
	return []GaugeOption{
		GaugeCustom("Set returns for a value of any sign", func(t *testing.T, g telemetry.Gauge) {
			for _, v := range []float64{0, 1, -1, 1e9, -1e9} {
				testkit.AssertNilSafe(t, func() { g.Set(t.Context(), v) })
			}
		}),

		GaugeCustom("Add returns for a delta of any sign", func(t *testing.T, g telemetry.Gauge) {
			for _, v := range []float64{0, 1, -1, 1e9, -1e9} {
				testkit.AssertNilSafe(t, func() { g.Add(t.Context(), v) })
			}
		}),

		GaugeCustom("With returns a usable Gauge for nil attributes", func(t *testing.T, g telemetry.Gauge) {
			derived := g.With(nil)
			testkit.True(t, derived != nil, "With(nil) must return a non-nil Gauge")
			testkit.AssertNilSafe(t, func() { derived.Set(t.Context(), 1) })
		}),

		GaugeCustom("With returns a usable Gauge for empty attributes", func(t *testing.T, g telemetry.Gauge) {
			derived := g.With([]telemetry.Attr{})
			testkit.True(t, derived != nil, "With([]) must return a non-nil Gauge")
			testkit.AssertNilSafe(t, func() { derived.Set(t.Context(), 1) })
		}),

		GaugeCustom("With returns a usable Gauge when called on a bound Gauge", func(t *testing.T, g telemetry.Gauge) {
			a := g.With([]telemetry.Attr{telemetry.AttrString("k1", "v1")})
			b := a.With([]telemetry.Attr{telemetry.AttrString("k2", "v2")})
			testkit.True(t, b != nil, "chained With must return a non-nil Gauge")
			testkit.AssertNilSafe(t, func() {
				b.Set(t.Context(), 42)
				b.Add(t.Context(), 1)
			})
		}),

		GaugeCustom("Release returns for a bound Gauge", func(t *testing.T, g telemetry.Gauge) {
			testkit.AssertNilSafe(t, g.With(releaseAttrs).Release)
		}),

		GaugeCustom("Release returns for a released Gauge", func(t *testing.T, g telemetry.Gauge) {
			bound := g.With(releaseAttrs)
			bound.Release()
			testkit.AssertNilSafe(t, bound.Release)
		}),

		GaugeCustom("Release leaves an unbound Gauge usable", func(t *testing.T, g telemetry.Gauge) {
			testkit.AssertNilSafe(t, g.Release)
			testkit.AssertNilSafe(t, func() { g.Set(t.Context(), 1) })
		}),

		GaugeCustom("Set returns for a released Gauge", func(t *testing.T, g telemetry.Gauge) {
			bound := g.With(releaseAttrs)
			bound.Release()
			testkit.AssertNilSafe(t, func() { bound.Set(t.Context(), 1) })
		}),

		GaugeCustom("Add returns for a released Gauge", func(t *testing.T, g telemetry.Gauge) {
			bound := g.With(releaseAttrs)
			bound.Release()
			testkit.AssertNilSafe(t, func() { bound.Add(t.Context(), 1) })
		}),

		GaugeCustom("Release leaves the Gauge that its With returned usable", func(t *testing.T, g telemetry.Gauge) {
			parent := g.With(releaseAttrs)
			child := parent.With([]telemetry.Attr{telemetry.AttrString("k2", "v2")})
			parent.Release()
			testkit.AssertNilSafe(t, func() { child.Set(t.Context(), 1) })
		}),

		GaugeCustom("Release runs concurrently with Set on the same Gauge", func(t *testing.T, g telemetry.Gauge) {
			ctx := t.Context()
			bound := g.With(releaseAttrs)
			releaseWhileEmitting(bound.Release, func(i int) { bound.Set(ctx, float64(i)) })
		}),

		GaugeCustom("Release runs concurrently with Add on the same Gauge", func(t *testing.T, g telemetry.Gauge) {
			ctx := t.Context()
			bound := g.With(releaseAttrs)
			releaseWhileEmitting(bound.Release, func(int) { bound.Add(ctx, 1) })
		}),
	}
}
