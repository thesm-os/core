// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package w3c implements W3C Trace Context propagation over the
// [telemetry.Propagator] seam.
//
// Trace Context propagates a trace between systems that share no tracing
// library. Its traceparent header contains the trace identity and the
// sampling decision, and its tracestate header contains the state of
// tracing vendors.
//
// # Identifier formats
//
// [telemetry.TraceID] and [telemetry.SpanID] are opaque in the seam, so an
// implementation can use W3C hex, a UUID or another form. This propagator
// requires the W3C forms: 32 and 16 lowercase hex digits, not all of them
// zero. [Propagator.Inject] writes no header for a context that it cannot
// represent, because a receiver refuses a header outside the grammar.
//
// # Concurrency
//
// [Propagator] is an empty struct and is safe for concurrent use.
//
// # Dependency position
//
// Imports context, slices, strconv and strings from the standard library,
// and go.thesmos.sh/core/telemetry from this module.
package w3c

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"go.thesmos.sh/core/telemetry"
)

// The names of the headers of Trace Context.
const (
	// TraceParentHeader is the header of the trace identity and the
	// sampling decision.
	TraceParentHeader = "traceparent"

	// TraceStateHeader is the header of the state of tracing vendors.
	TraceStateHeader = "tracestate"
)

// The widths of the fields of a traceparent of version 00.
const (
	traceIDLen = 32
	spanIDLen  = 16

	// traceParentLen is the length of a traceparent of version 00: two
	// digits of the version, the trace-id, the parent-id, two digits of the
	// flags, and three hyphens. It is a literal, because a mutation tool
	// runs no mutant of an operator in a const declaration.
	traceParentLen = 55
)

// sampledFlag is bit 0 of the flags byte, the sampled flag of the
// specification.
const sampledFlag = 0x01

// fields are the headers that [Propagator.Inject] writes.
var fields = []string{TraceParentHeader, TraceStateHeader}

// Propagator implements W3C Trace Context. The zero value is ready to use.
//
// # Concurrency
//
// Safe for concurrent use. Propagator has no state.
//
// # Allocation contract
//
// Inject allocates the traceparent value, Extract allocates nothing, and
// Fields allocates the slice that it returns.
type Propagator struct{}

// Propagator satisfies the seam.
var _ telemetry.Propagator = Propagator{}

// Inject writes sc as a traceparent header, and as a tracestate header
// when [telemetry.SpanContext.TraceState] is not empty. An empty
// tracestate writes no header, because an empty header differs from no
// header.
//
// Inject writes nothing for a context whose identifiers are not in the W3C
// form. The identifiers are opaque in the seam, so the context of a tracer
// of another format has no traceparent. A header outside the grammar would
// make the receiver start a new trace, and would leave a malformed header
// in the carrier.
//
// # Allocation contract
//
// One allocation, for the 55 bytes of the traceparent value.
// [telemetry.Carrier.Set] takes a string, and Go concatenates the parts of
// the value in one allocation. Inject runs once per outbound call.
func (Propagator) Inject(_ context.Context, sc telemetry.SpanContext, carrier telemetry.Carrier) {
	traceID, spanID := string(sc.TraceID), string(sc.SpanID)
	if len(traceID) != traceIDLen || !isLowerHex(traceID) || strings.Trim(traceID, "0") == "" {
		return
	}

	if len(spanID) != spanIDLen || !isLowerHex(spanID) || strings.Trim(spanID, "0") == "" {
		return
	}

	flags := "00"
	if sc.Sampled {
		flags = "01"
	}

	// Inject writes version 00. A receiver of a later version parses the
	// fields of version 00.
	carrier.Set(TraceParentHeader, "00-"+traceID+"-"+spanID+"-"+flags)

	if sc.TraceState != "" {
		carrier.Set(TraceStateHeader, sc.TraceState)
	}
}

// Extract reads the span context of carrier, and reports false when the
// traceparent header is absent or malformed. A malformed header reports
// false and not an error, because the specification requires the receiver
// to start a new trace and not to refuse the request.
//
// Extract returns the tracestate header unparsed. It reads the header only
// for a valid traceparent, because the state of a vendor belongs to a
// trace identity.
//
// A traceparent of a version above 00 can append fields after a hyphen,
// which Extract ignores, as the specification requires. The specification
// forbids version ff.
//
// # Allocation contract
//
// Zero alloc.
func (Propagator) Extract(_ context.Context, carrier telemetry.Carrier) (telemetry.SpanContext, bool) {
	raw := carrier.Get(TraceParentHeader)
	if len(raw) < traceParentLen {
		return telemetry.SpanContext{}, false
	}

	version, traceID, spanID, flags := raw[0:2], raw[3:35], raw[36:52], raw[53:55]
	if raw[2] != '-' || raw[35] != '-' || raw[52] != '-' {
		return telemetry.SpanContext{}, false
	}

	// Every version other than ff is 00 or a later version, whose fields of
	// version 00 apply.
	if !isLowerHex(version) || version == "ff" {
		return telemetry.SpanContext{}, false
	}

	// A traceparent of version 00 has exactly traceParentLen characters. A
	// later version can append fields after a hyphen.
	if len(raw) > traceParentLen {
		if version == "00" || raw[traceParentLen] != '-' {
			return telemetry.SpanContext{}, false
		}
	}

	// The slices of raw fix the lengths of the identifiers.
	if !isLowerHex(traceID) || strings.Trim(traceID, "0") == "" {
		return telemetry.SpanContext{}, false
	}

	if !isLowerHex(spanID) || strings.Trim(spanID, "0") == "" || !isLowerHex(flags) {
		return telemetry.SpanContext{}, false
	}

	// Bit 0 of the flags byte is the sampled flag, so 03, 09, 0f and ff are
	// sampled, and 0a and fe are not. isLowerHex accepted the two digits of
	// flags, so ParseUint returns no error.
	traceFlags, _ := strconv.ParseUint(flags, 16, 8)

	return telemetry.SpanContext{
		TraceID:    telemetry.TraceID(traceID),
		SpanID:     telemetry.SpanID(spanID),
		TraceState: carrier.Get(TraceStateHeader),
		Sampled:    traceFlags&sampledFlag != 0,
	}, true
}

// Fields returns the headers that Inject writes, so that a middleware can
// clear them before it injects a context again. Each call returns a new
// slice, which the caller can sort or truncate.
//
// # Allocation contract
//
// One allocation, for the slice of the two names.
func (Propagator) Fields() []string { return slices.Clone(fields) }

// isLowerHex reports whether every byte of s is a digit or a letter from a
// to f. encoding/hex also accepts upper case, which the specification
// forbids, so that two headers of one identity are equal as bytes.
func isLowerHex(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}
