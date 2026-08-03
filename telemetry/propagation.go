// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"maps"
	"slices"
)

// Carrier is a set of string key/value pairs a [Propagator] reads from
// and writes to: HTTP headers, RPC metadata, a broker's attribute map.
//
// The method set is the one the observability ecosystem converged on,
// adopted verbatim. Because Go satisfies interfaces structurally, a
// carrier written against a concrete tracing library's equivalent
// satisfies this one and vice versa, with no adapter and no
// conversion. Defining it here fragments nothing; it lets code built
// on this seam participate in propagation without importing a tracing
// library.
type Carrier interface {
	// Get returns the value for key, or the empty string when the
	// carrier holds none.
	Get(key string) string

	// Set writes key, replacing any existing value.
	Set(key, value string)

	// Keys lists every key the carrier currently holds.
	Keys() []string
}

// Propagator moves a [SpanContext] across a process boundary.
//
// A trace that stops at a process boundary is not a trace. This is the
// seam that carries one across, so callers making remote calls do not
// have to reach past the telemetry abstraction to a concrete tracing
// library — which is the coupling this package exists to prevent.
//
// core ships no transport. Defining how context crosses a boundary
// does not require shipping the boundary, any more than [io.Reader]
// requires shipping a file.
//
// # Concurrency
//
// Implementations must be safe for concurrent use.
type Propagator interface {
	// Inject writes sc into carrier.
	//
	// A SpanContext the propagator cannot represent writes nothing.
	// [TraceID] and [SpanID] are opaque in this seam, so a context
	// minted by a tracer using a different identifier format may not
	// be expressible in a given wire format, and a malformed header
	// is worse than an absent one.
	Inject(ctx context.Context, sc SpanContext, carrier Carrier)

	// Extract reads a span context from carrier. ok is false when the
	// carrier holds none, in which case the caller starts a new trace
	// rather than a child span.
	//
	// A malformed context also reports false rather than an error.
	// That is the W3C rule and it generalises: an unparseable header
	// is not grounds to fail a request, and a caller given an error
	// would be tempted to make it one.
	Extract(ctx context.Context, carrier Carrier) (sc SpanContext, ok bool)

	// Fields lists the carrier keys this propagator writes.
	//
	// Middleware re-injecting into a carrier that already holds a
	// context must clear these first: a propagator that writes
	// conditionally would otherwise leave a stale value beside a
	// fresh one. Fields is what makes that possible without
	// hardcoding header names into every middleware.
	Fields() []string
}

// MapCarrier adapts a map[string]string — the canonical carrier for
// tests, and for brokers whose message attributes are already a map.
//
// # Concurrency
//
// Not safe for concurrent use, as the underlying map is not.
type MapCarrier map[string]string

// Compile-time proof that MapCarrier satisfies the seam.
var _ Carrier = MapCarrier(nil)

// Get returns the value for key, or the empty string.
func (c MapCarrier) Get(key string) string { return c[key] }

// Set writes key, replacing any existing value.
func (c MapCarrier) Set(key, value string) { c[key] = value }

// Keys lists every key held, in unspecified order.
func (c MapCarrier) Keys() []string { return slices.Collect(maps.Keys(c)) }
