// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import "context"

// Gauge is a metric instrument whose value goes up and down, such as a
// queue depth, a cache size or a count of requests in flight.
//
// # Set and Add
//
//   - [Gauge.Set] records an absolute value, for a caller that knows the
//     current value, such as after a recount.
//   - [Gauge.Add] records a change, for a caller that observes one, such
//     as one opened connection.
//
// # Aggregation
//
// [InstrumentSpec.Aggregation] states how the values of a gauge's
// attribute sets combine:
//
//   - [GaugeAggregationSum]: each Gauge that [Gauge.With] returns has a
//     value of its own, which Set replaces and Add changes. The value of
//     an attribute set is the sum of the values of its unreleased
//     Gauges. The overflow series of an adapter that caps the attribute
//     sets is the sum over the overflowed sets.
//   - [GaugeAggregationMax]: the overflow series is the largest value
//     that a Gauge of an overflowed set recorded in the export interval,
//     so a caller sets every Gauge in every interval.
//   - [GaugeAggregationUnspecified]: the values of different sets do not
//     combine, and an overflow series has no defined value.
//
// # Allocation contract
//
// [Gauge.Set], [Gauge.Add] and [Gauge.Release] are zero-alloc.
// [Gauge.With] allocates.
type Gauge interface {
	// Set records value, an absolute value, against the bound attribute
	// set.
	//
	//testkit:mutator
	Set(ctx context.Context, value float64)

	// Add records delta, a change: a positive delta increases the value,
	// and a negative one decreases it.
	//
	//testkit:mutator
	Add(ctx context.Context, delta float64)

	// With returns a Gauge of the same instrument, bound to attrs, with
	// the slice semantics of [Counter.With].
	With(attrs []Attr) Gauge

	// Release ends this bound instrument, with the contract of
	// [Counter.Release]. In a sum gauge, Release also removes the
	// Gauge's value from the value of its attribute set.
	//
	//testkit:mutator
	Release()
}
