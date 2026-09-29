// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"math"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
)

// CounterContractAssertions returns the assertions that every
// [telemetry.Counter] implementation satisfies:
//
//   - Add returns for a positive, a zero and a negative value.
//   - With returns a usable Counter for nil attributes, for empty
//     attributes and when called on a bound Counter.
//   - Release returns for a bound Counter and for a released one.
//   - Release leaves an unbound Counter usable.
//   - Add returns for a released Counter.
//   - Release leaves the Counter that its With returned usable.
//   - Release runs concurrently with Add on the same Counter without a
//     data race.
//
// A test of an implementation runs them:
//
//	telemetrytest.AssertCounterContract(t, factory,
//	    telemetrytest.CounterContractAssertions()...,
//	)
func CounterContractAssertions() []CounterOption {
	return []CounterOption{
		CounterCustom("Add returns for a positive value", func(t *testing.T, c telemetry.Counter) {
			testkit.AssertNilSafe(t, func() { c.Add(t.Context(), 1) })
		}),

		CounterCustom("Add returns for a zero value", func(t *testing.T, c telemetry.Counter) {
			testkit.AssertNilSafe(t, func() { c.Add(t.Context(), 0) })
		}),

		CounterCustom("Add returns for a negative value", func(t *testing.T, c telemetry.Counter) {
			for _, v := range []int64{-1, math.MinInt64} {
				testkit.AssertNilSafe(t, func() { c.Add(t.Context(), v) })
			}
		}),

		CounterCustom(
			"With returns a usable Counter for nil attributes",
			func(t *testing.T, c telemetry.Counter) {
				derived := c.With(nil)
				testkit.True(t, derived != nil, "With(nil) must return a non-nil Counter")
				testkit.AssertNilSafe(t, func() { derived.Add(t.Context(), 1) })
			},
		),

		CounterCustom(
			"With returns a usable Counter for empty attributes",
			func(t *testing.T, c telemetry.Counter) {
				derived := c.With([]telemetry.Attr{})
				testkit.True(t, derived != nil, "With([]) must return a non-nil Counter")
				testkit.AssertNilSafe(t, func() { derived.Add(t.Context(), 1) })
			},
		),

		CounterCustom(
			"With returns a usable Counter when called on a bound Counter",
			func(t *testing.T, c telemetry.Counter) {
				a := c.With([]telemetry.Attr{telemetry.AttrString("k1", "v1")})
				b := a.With([]telemetry.Attr{telemetry.AttrString("k2", "v2")})
				testkit.True(t, b != nil, "chained With must return a non-nil Counter")
				testkit.AssertNilSafe(t, func() { b.Add(t.Context(), 1) })
			},
		),

		CounterCustom("Release returns for a bound Counter", func(t *testing.T, c telemetry.Counter) {
			testkit.AssertNilSafe(t, c.With(releaseAttrs).Release)
		}),

		CounterCustom("Release returns for a released Counter", func(t *testing.T, c telemetry.Counter) {
			bound := c.With(releaseAttrs)
			bound.Release()
			testkit.AssertNilSafe(t, bound.Release)
		}),

		CounterCustom("Release leaves an unbound Counter usable", func(t *testing.T, c telemetry.Counter) {
			testkit.AssertNilSafe(t, c.Release)
			testkit.AssertNilSafe(t, func() { c.Add(t.Context(), 1) })
		}),

		CounterCustom("Add returns for a released Counter", func(t *testing.T, c telemetry.Counter) {
			bound := c.With(releaseAttrs)
			bound.Release()
			testkit.AssertNilSafe(t, func() { bound.Add(t.Context(), 1) })
		}),

		CounterCustom(
			"Release leaves the Counter that its With returned usable",
			func(t *testing.T, c telemetry.Counter) {
				parent := c.With(releaseAttrs)
				child := parent.With([]telemetry.Attr{telemetry.AttrString("k2", "v2")})
				parent.Release()
				testkit.AssertNilSafe(t, func() { child.Add(t.Context(), 1) })
			},
		),

		CounterCustom(
			"Release runs concurrently with Add on the same Counter",
			func(t *testing.T, c telemetry.Counter) {
				ctx := t.Context()
				bound := c.With(releaseAttrs)
				releaseWhileEmitting(bound.Release, func(int) { bound.Add(ctx, 1) })
			},
		),
	}
}
