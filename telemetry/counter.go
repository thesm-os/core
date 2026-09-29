// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import "context"

// Counter is a metric instrument whose value only increases.
//
// An instrument goes through four steps:
//
//  1. [Reporter.Counter] resolves the named instrument once, at
//     initialisation. It allocates.
//  2. [Counter.With] binds an attribute set for one time series. It
//     allocates.
//  3. [Counter.Add] records a value, many times per request, and does
//     not allocate on any implementation in this module.
//  4. [Counter.Release] ends the bound instrument when its caller stops
//     using the attribute set.
//
// # ctx semantics
//
// Add does not use its [context.Context] for cancellation, because an
// atomic add does no I/O. Adapters read three values from ctx:
//
//   - The active OpenTelemetry span, which an exemplar links to.
//   - The W3C baggage entries, which add labels per request.
//   - The trace ID, which correlates metrics with logs and traces.
//
// Without ctx an adapter loses these for the instrument. Passing ctx
// costs one interface value per call.
//
// # Allocation contract
//
// [Counter.Add] and [Counter.Release] are zero-alloc. [Counter.With]
// allocates.
type Counter interface {
	// Add increments the counter by value against the bound attribute
	// set. A negative value violates the monotonic precondition:
	// production-grade implementations panic with a diagnostic message,
	// and [go.thesmos.sh/core/telemetry/noop] discards it, as it
	// discards every value. Portable code never passes a negative value.
	//
	//testkit:mutator
	Add(ctx context.Context, value int64)

	// With returns a Counter of the same instrument, bound to attrs.
	// Later calls of Add on the returned Counter record against that
	// attribute set.
	//
	// With takes the slice by reference, so the caller does not change
	// it after the call. An implementation may copy it, and does not
	// keep the slice as its store.
	With(attrs []Attr) Counter

	// Release ends this bound instrument, which [Counter.With] returned.
	// After Release returns, Add records nothing. A second Release, and
	// Release on a Counter that a [Reporter] method returned, do
	// nothing. Releasing one Counter does not end another, including a
	// Counter that its own With returned.
	//
	// Release is safe for concurrent use with every method of every
	// instrument. An Add on the same Counter that runs concurrently with
	// Release may record or may not. An adapter that keeps state per
	// attribute set may forget a set after the last Counter bound to it
	// is released and the adapter has exported the set's last
	// measurement. A later With of the set then starts a new series,
	// from zero, with a new start time.
	//
	// Release does not allocate.
	//
	//testkit:mutator
	Release()
}
