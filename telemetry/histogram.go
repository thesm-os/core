// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import "context"

// Histogram records the distribution of values, such as request
// latencies, payload sizes or queue waits.
//
// # Allocation contract
//
// [Histogram.Record] and [Histogram.Release] are zero-alloc.
// [Histogram.With] allocates.
type Histogram interface {
	// Record adds value to the distribution of the bound attribute set.
	//
	//testkit:mutator
	Record(ctx context.Context, value float64)

	// With returns a Histogram of the same instrument, bound to attrs,
	// with the slice semantics of [Counter.With].
	With(attrs []Attr) Histogram

	// Release ends this bound instrument, with the contract of
	// [Counter.Release].
	//
	//testkit:mutator
	Release()
}
