// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"maps"
	"slices"
)

// Carrier is a set of string key/value pairs that a [Propagator] reads
// from and writes to: HTTP headers, RPC metadata, a broker's attribute map.
// [HeaderCarrier] adapts the headers of an HTTP message, and [MapCarrier]
// adapts a map of strings.
//
// The method set is the method set of OpenTelemetry's TextMapCarrier. Go
// satisfies interfaces structurally, so a carrier of a tracing library
// satisfies this interface, and a Carrier satisfies the library's, without
// an adapter. Code built on this seam propagates a trace without importing
// a tracing library.
type Carrier interface {
	// Get returns the value for key, or the empty string when the
	// carrier has no value for it.
	Get(key string) string

	// Set writes key, replacing any existing value.
	Set(key, value string)

	// Keys lists every key in the carrier.
	Keys() []string
}

// Propagator moves a [SpanContext] across a process boundary. Inject
// writes the context of an outgoing call into its carrier, and Extract
// reads the context of an incoming call from its carrier, so a trace
// continues in the process that serves the call. A caller of a remote
// service propagates its trace without importing a tracing library.
//
// A server starts the span of an incoming request under the extracted
// context with [WithRemoteParent]. The HTTP server and client of this
// module, [go.thesmos.sh/core/net/httpserver] and
// [go.thesmos.sh/core/net/httpclient], extract and inject through a
// [HeaderCarrier].
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
	// carrier contains none, and the caller then starts a new trace
	// instead of a child span.
	//
	// A malformed context also reports false, not an error, as W3C Trace
	// Context requires: a header that does not parse is no reason to fail
	// the request that it arrived with.
	Extract(ctx context.Context, carrier Carrier) (sc SpanContext, ok bool)

	// Fields lists the carrier keys this propagator writes.
	//
	// Middleware that injects into a carrier that contains a context
	// already must delete these keys first. A propagator that writes a
	// key only under a condition would otherwise leave a stale value
	// beside a fresh one. Fields names the keys, so the middleware
	// contains no header names.
	Fields() []string
}

// MapCarrier adapts a map[string]string, for tests and for brokers whose
// message attributes are a map of strings.
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

// Keys lists every key of the map, in unspecified order, and returns nil
// for an empty map.
//
// # Allocation contract
//
// One allocation, for the slice of the keys.
func (c MapCarrier) Keys() []string {
	if len(c) == 0 {
		return nil
	}

	return slices.AppendSeq(make([]string, 0, len(c)), maps.Keys(c))
}
