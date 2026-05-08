// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import "context"

// Gauge is a non-monotonic metric instrument carrying a current
// value (queue depth, cache size, in-flight request count).
//
// # Set vs Add
//
//   - [Gauge.Set] records an absolute value. Use when the caller
//     knows the current value (for example after a recompute).
//   - [Gauge.Add] applies a relative delta. Use when the caller
//     observes a change (for example "one connection opened").
//
// # Allocation contract
//
// [Gauge.Set] and [Gauge.Add] are zero-alloc. [Gauge.With]
// allocates.
type Gauge interface {
	// Set records an absolute value against the bound attribute
	// set.
	//
	//testkit:mutator
	Set(ctx context.Context, value float64)

	// Add applies a relative delta. Positive increments, negative
	// decrements.
	//
	//testkit:mutator
	Add(ctx context.Context, delta float64)

	// With returns a [Gauge] sharing this instrument but bound to
	// the given attributes. Slice semantics follow [Counter.With].
	With(attrs []Attr) Gauge
}
