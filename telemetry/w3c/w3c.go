// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package w3c implements W3C Trace Context propagation over the
// [telemetry.Propagator] seam.
//
// Trace Context is the interoperable format: a `traceparent` header
// carrying the trace identity and the sampling decision, and a
// `tracestate` header carrying vendor state. It is what lets a trace
// cross between systems that share no tracing library.
//
// The format is string handling over a fixed grammar, so this needs
// no dependency.
//
// # Identifier formats
//
// [telemetry.TraceID] and [telemetry.SpanID] are opaque in the seam —
// an implementation may use W3C hex, a UUID, or anything else. This
// propagator requires the W3C forms: 32 and 16 lowercase hex digits,
// neither all-zero. A context it cannot represent injects nothing
// rather than writing a header a receiver would reject.
//
// # Concurrency
//
// [Propagator] is an empty struct holding no state and is safe for
// concurrent use.
package w3c

import (
	"context"
	"strings"

	"go.thesmos.sh/core/telemetry"
)

// Header names, fixed by the specification.
const (
	// TraceParentHeader carries the trace identity and flags.
	TraceParentHeader = "traceparent"
	// TraceStateHeader carries vendor state.
	TraceStateHeader = "tracestate"
)

// Field widths, fixed by the specification.
const (
	versionLen = 2
	traceIDLen = 32
	spanIDLen  = 16
	flagsLen   = 2

	// traceParentLen is the length of a version-00 traceparent:
	// four fields and three hyphens.
	traceParentLen = versionLen + 1 + traceIDLen + 1 + spanIDLen + 1 + flagsLen
)

// sampledFlag is bit 0 of the flags byte — the only flag the
// specification currently defines.
const sampledFlag = 0x01

// fields are the headers [Propagator.Inject] writes. Package-level so
// Fields returns a copy of a fixed slice rather than rebuilding one.
var fields = []string{TraceParentHeader, TraceStateHeader}

// Propagator implements W3C Trace Context.
//
// The zero value is ready to use.
type Propagator struct{}

// Compile-time proof that Propagator satisfies the seam.
var _ telemetry.Propagator = Propagator{}

// Inject writes sc as `traceparent`, and `tracestate` when sc carries
// vendor state.
//
// A context whose identifiers are not in the W3C form writes nothing:
// the identifiers are opaque in the seam, so a context from a tracer
// using another format is simply not representable here, and an
// invalid header would be rejected by the receiver anyway while also
// corrupting the carrier.
//
// An empty [telemetry.SpanContext.TraceState] writes no header, since
// an empty tracestate is not the same as none.
//
// # Allocation contract
//
// One allocation, for the 55-byte traceparent value. It is
// unavoidable: [telemetry.Carrier.Set] takes a string, so the header
// has to exist as one, and Go performs the whole concatenation in a
// single allocation. Injection happens once per outbound call, so
// this sits against a network round trip.
//
// [Propagator.Extract] allocates nothing.
func (Propagator) Inject(_ context.Context, sc telemetry.SpanContext, carrier telemetry.Carrier) {
	traceID, spanID := string(sc.TraceID), string(sc.SpanID)
	if !validID(traceID, traceIDLen) || !validID(spanID, spanIDLen) {
		return
	}

	flags := "00"
	if sc.Sampled {
		flags = "01"
	}

	// Version 00, the only version this writes. A receiver
	// understanding a later version still parses these fields.
	carrier.Set(TraceParentHeader, "00-"+traceID+"-"+spanID+"-"+flags)

	if sc.TraceState != "" {
		carrier.Set(TraceStateHeader, sc.TraceState)
	}
}

// Extract reads a span context from carrier.
//
// Reports false when `traceparent` is absent or malformed. A
// malformed header is treated as absent rather than as an error: the
// specification requires the receiver to start a new trace, not to
// reject the request.
//
// `tracestate` is carried through verbatim and never parsed. It is
// only read when a valid `traceparent` is present, since vendor state
// without a trace identity has nothing to attach to.
func (Propagator) Extract(_ context.Context, carrier telemetry.Carrier) (telemetry.SpanContext, bool) {
	raw := carrier.Get(TraceParentHeader)
	if len(raw) < traceParentLen {
		return telemetry.SpanContext{}, false
	}

	version, traceID, spanID, flags := raw[0:2], raw[3:35], raw[36:52], raw[53:55]
	if raw[2] != '-' || raw[35] != '-' || raw[52] != '-' {
		return telemetry.SpanContext{}, false
	}

	// Version ff is forbidden outright; every other value is either
	// this version or a later one whose known fields still apply.
	if !isLowerHex(version) || version == "ff" {
		return telemetry.SpanContext{}, false
	}

	// Version 00 is exactly traceParentLen characters — trailing data
	// makes it malformed, not forward-compatible. Only a later
	// version may append further hyphen-separated fields, which the
	// specification requires be ignored rather than rejected.
	if len(raw) > traceParentLen {
		if version == "00" || raw[traceParentLen] != '-' {
			return telemetry.SpanContext{}, false
		}
	}

	if !validID(traceID, traceIDLen) || !validID(spanID, spanIDLen) || !isLowerHex(flags) {
		return telemetry.SpanContext{}, false
	}

	// The sampled bit is bit 0 of the flags byte, so the low nibble
	// decides it: 03, 09, 0f and ff are all sampled, 0a and fe are
	// not. strconv.ParseUint would express this, but flags is
	// already validated as lowercase hex above — the specification
	// forbids upper case and ParseUint accepts it — which leaves
	// ParseUint's error branch unreachable.
	return telemetry.SpanContext{
		TraceID:    telemetry.TraceID(traceID),
		SpanID:     telemetry.SpanID(spanID),
		TraceState: carrier.Get(TraceStateHeader),
		Sampled:    unhex(flags[1])&sampledFlag != 0,
	}, true
}

// Fields returns the headers Inject writes, so middleware can clear
// them before re-injecting.
//
// The result is a fresh slice: callers iterate, sort, and truncate it,
// and a shared backing array would corrupt every later caller.
func (Propagator) Fields() []string {
	return append([]string(nil), fields...)
}

// validID reports whether s is n lowercase hex digits and not
// all-zero. An all-zero trace-id or parent-id means "no identity"
// rather than an identity that happens to be zero, and the
// specification forbids both.
func validID(s string, n int) bool {
	return len(s) == n && isLowerHex(s) && strings.Trim(s, "0") != ""
}

// isLowerHex reports whether every byte is 0-9 or a-f.
//
// Hand-rolled because the standard library has no equivalent:
// [encoding/hex] accepts upper case, which the specification forbids
// so that a byte comparison of two headers is a comparison of two
// identities. Expressing this through hex.Decode means decoding and
// re-encoding to compare — slower, and needing more explanation than
// the predicate itself.
func isLowerHex(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}

// unhex converts one already-validated lowercase hex digit to its
// value.
func unhex(c byte) byte {
	if c <= '9' {
		return c - '0'
	}

	return c - 'a' + 10
}
