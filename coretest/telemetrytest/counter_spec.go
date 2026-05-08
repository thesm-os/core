// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
)

// CounterContractAssertions returns the assertions every
// [telemetry.Counter] implementation must satisfy: Add accepts
// canonical values without panic, With returns a non-nil derived
// instrument, the With chain composes (the returned Counter
// itself accepts Add), and nil/empty attribute slices are
// tolerated.
//
//	telemetrytest.AssertCounterContract(t, factory,
//	    telemetrytest.CounterContractAssertions()...,
//	)
func CounterContractAssertions() []CounterOption {
	return []CounterOption{
		CounterCustom("Add accepts a positive value", func(t *testing.T, c telemetry.Counter) {
			testkit.AssertNilSafe(t, func() { c.Add(t.Context(), 1) })
		}),

		CounterCustom("Add tolerates a zero value", func(t *testing.T, c telemetry.Counter) {
			testkit.AssertNilSafe(t, func() { c.Add(t.Context(), 0) })
		}),

		CounterCustom(
			"With nil attributes returns a usable Counter",
			func(t *testing.T, c telemetry.Counter) {
				derived := c.With(nil)
				testkit.True(t, derived != nil, "With(nil) must return a non-nil Counter")
				testkit.AssertNilSafe(t, func() { derived.Add(t.Context(), 1) })
			},
		),

		CounterCustom(
			"With empty attributes returns a usable Counter",
			func(t *testing.T, c telemetry.Counter) {
				derived := c.With([]telemetry.Attr{})
				testkit.True(t, derived != nil, "With([]) must return a non-nil Counter")
				testkit.AssertNilSafe(t, func() { derived.Add(t.Context(), 1) })
			},
		),

		CounterCustom(
			"With chain composes — derived Counter accepts further With",
			func(t *testing.T, c telemetry.Counter) {
				a := c.With([]telemetry.Attr{telemetry.AttrString("k1", "v1")})
				b := a.With([]telemetry.Attr{telemetry.AttrString("k2", "v2")})
				testkit.True(t, b != nil, "chained With must return a non-nil Counter")
				testkit.AssertNilSafe(t, func() { b.Add(t.Context(), 1) })
			},
		),
	}
}
