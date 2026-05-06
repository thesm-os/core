// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import "context"

// Counter is a monotonically-increasing metric instrument.
//
// The lifecycle is:
//
//  1. [Reporter.Counter] resolves the named instrument once at init.
//     Allocates.
//  2. [Counter.With] pre-binds the attribute set for one
//     time-series. Allocates.
//  3. [Counter.Add] is called many times per request and is
//     zero-allocation by contract on every implementation in this
//     module.
//
// # ctx semantics
//
// The [context.Context] is not used for cancellation — atomic adds
// have no I/O — but adapters read the active OpenTelemetry span
// from ctx for exemplar correlation, the W3C baggage entries for
// per-request label enrichment, and the trace ID for cross-signal
// correlation. Drop ctx and these capabilities are permanently
// disabled for this instrument; the cost of carrying ctx is one
// interface-pointer pass per call.
//
// # Allocation contract
//
// [Counter.Add] is zero-alloc. [Counter.With] allocates.
type Counter interface {
	// Add increments the counter by value against the bound
	// attribute set. Negative values violate the monotonic
	// precondition: production-grade implementations panic with a
	// diagnostic message (matching the precondition-violation
	// discipline used elsewhere in this module); the [noop]
	// implementation discards as it produces no observable signal
	// regardless. Consumers writing portable code must not pass
	// negative values.
	Add(ctx context.Context, value int64)

	// With returns a [Counter] sharing this instrument but bound
	// to the given attributes. Subsequent [Counter.Add] calls on
	// the returned counter emit against that attribute set.
	//
	// The attribute slice is taken by reference; callers must
	// not mutate it after the call. Implementations may copy
	// for their own state; the slice is not retained as the
	// canonical store.
	With(attrs []Attr) Counter
}
