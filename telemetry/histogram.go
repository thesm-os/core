// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import "context"

// Histogram records a distribution of values (request latencies,
// payload sizes, queue waits).
//
// # Allocation contract
//
// [Histogram.Record] is zero-alloc. [Histogram.With] allocates.
type Histogram interface {
	// Record adds a value to the distribution against the bound
	// attribute set.
	Record(ctx context.Context, value float64)

	// With returns a [Histogram] sharing this instrument but bound
	// to the given attributes. Slice semantics follow [Counter.With].
	With(attrs []Attr) Histogram
}
