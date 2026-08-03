---
rfc: 0020
title: Trace Context Propagation
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-08-03
updated: 2026-08-03
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0020: Trace Context Propagation

## Summary

`telemetry` carries span context within a process and has no way to
move it between processes. This RFC adds a carrier and propagator pair
plus a W3C Trace Context implementation. The carrier's method set is
the one the ecosystem already converged on, so bridging to a concrete
tracing library is structural rather than adapter code, and W3C Trace
Context is string parsing that needs no dependency.

## Motivation

A trace that stops at a process boundary is not a trace. Every caller
that makes a remote call therefore reaches past the `telemetry` seam to
a concrete tracing library to inject headers, which is exactly the
coupling the seam exists to prevent — and it reintroduces it at the one
place where the seam was supposed to pay off.

RFC-0004 deferred propagation as "required when `core` ships HTTP / RPC
clients". That reasoning inverts the dependency. Propagation is needed
by whoever *makes* calls, not by whoever ships a client, and `core`
need not ship a transport to define how context crosses one. `io.Reader`
does not ship a file.

The format is settled and small. W3C Trace Context is two header values
with a specified grammar, and parsing them is string handling over a
fixed layout.

## Detailed design

```go
// In package telemetry.

// Carrier is a set of string key/value pairs a propagator reads from
// and writes to: HTTP headers, RPC metadata, a broker's attribute map.
type Carrier interface {
    Get(key string) string
    Set(key, value string)
    Keys() []string
}

// Propagator serialises span context across a process boundary.
type Propagator interface {
    // Inject writes sc into carrier.
    Inject(ctx context.Context, sc SpanContext, carrier Carrier)

    // Extract reads a span context from carrier. ok is false when the
    // carrier holds none, in which case callers start a new trace
    // rather than a child span.
    Extract(ctx context.Context, carrier Carrier) (sc SpanContext, ok bool)

    // Fields lists the carrier keys this propagator writes, so
    // middleware can clear them before re-injecting.
    Fields() []string
}

// MapCarrier adapts a map[string]string — the canonical carrier for
// tests and for brokers whose attributes are already a map.
type MapCarrier map[string]string
```

with `telemetry/w3c` implementing `traceparent` and `tracestate`.

### The carrier method set is not designed here

`Get`/`Set`/`Keys` is the shape the observability ecosystem converged
on, and it is adopted verbatim.

The consequence is worth stating explicitly, because it answers the
strongest objection to this RFC. A carrier written against a concrete
tracing library's interface satisfies `telemetry.Carrier` structurally,
and vice versa, with no adapter and no conversion — Go satisfies
interfaces structurally, so identical method sets are interchangeable.
Defining this interface therefore does not fragment anything; it lets
`core`-based code participate in propagation without importing a
tracing library.

This is the distinction from ADR-0009's refusal to define a logger. A
second spelling of `slog.Handler` would compete with a
standard-library interface every implementation already targets.
`Carrier` is not a second spelling — it is the same spelling, and the
alternative is not "use the standard library's" because the standard
library has none.

### `Extract` returns `ok`, not an error

A carrier with no trace context is the normal case at the edge of a
system, not a failure. Returning an error would make every ingress
handler branch on a condition that is expected, and would tempt callers
to log it.

A carrier holding a *malformed* trace context also returns `ok = false`.
The W3C specification requires exactly this: an unparseable
`traceparent` is treated as absent, and the receiver starts a new trace
rather than rejecting the request. Distinguishing the two would invite
callers to fail a request over a header they should ignore.

### `Fields` exists for one specific bug

Middleware that re-injects into a carrier that already holds a trace
context must clear the previous keys first, or a propagator that writes
conditionally leaves a stale `traceparent` alongside a fresh one.
`Fields` is what makes clearing possible without hardcoding the header
names of every propagator in every middleware.

### Composition

Multiple propagators compose by iteration rather than by a composite
type in `core`: a caller holding several calls `Inject` on each. A
composite would need a policy for what happens when two propagators
extract different contexts, and that policy is the caller's.

### Allocation contract

`Inject` and `Extract` are cold-path relative to the metric seams —
once per remote call, not once per loop iteration — and no
zero-allocation contract is claimed. `Fields` should return a
package-level slice rather than building one per call.

## Alternatives considered

### A. Wait until `core` ships an HTTP or RPC client

RFC-0004's original position.

**Why not:** it inverts the dependency. The need belongs to callers who
make remote calls; the transport is incidental. `core` will likely
never ship a transport, which would defer this forever.

### B. Define `SpanContext` serialisation directly, without a carrier

`Marshal`/`Unmarshal` on `SpanContext` and let callers place the bytes.

**Why not:** the placement is the interoperable part. Every other system
in a trace expects a `traceparent` header with a specified grammar, not
a `core`-specific encoding in a header of the caller's choosing.

### C. Ship the interfaces without a W3C implementation

**Why not:** an interface with no implementation gives the conformance
suite nothing to run and leaves every caller to write the parser. The
parser is the part that has a specification to get right.

### D. A `Carrier` that is `map[string]string` only

**Why not:** HTTP headers and RPC metadata are multi-valued and are not
maps, so a map-typed carrier would force a copy at every real boundary.
`MapCarrier` covers the map case as an implementation of the interface.

## Drawbacks

- `telemetry` gains three exported types whose value depends entirely
  on an implementation that lives outside the module, since `core`
  ships no transport to inject into.
- W3C Trace Context is versioned, and the parser must accept future
  versions it does not understand per the specification's forward-
  compatibility rules. That is subtle behaviour which is easy to
  implement as a rejection by mistake.
- `tracestate` has a size limit, a member limit, and mutation rules
  that are more intricate than `traceparent`, and a partial
  implementation is worse than none because it silently drops vendor
  state.
- `Extract` collapsing "absent" and "malformed" means a
  misconfigured upstream is invisible: traces silently start fresh
  instead of continuing, and nothing reports why.

## Open questions

None. Both questions this RFC previously carried are resolved: baggage
moves to future work, because it carries application data across a
trust boundary and that is a policy question this RFC should not settle
in passing; and `Propagator` gains no "did I write anything" method,
because a caller that needs to know holds the `SpanContext` it passed
to `Inject` and can test it directly, which is information the
propagator would only be echoing back.

## Unresolved / future work

- A baggage propagator. It travels the same path and is specified by
  the same working group, but it carries application data across a
  trust boundary, so its size limits and its propagation policy need
  deciding rather than inheriting.
- A binary propagation format for transports where header parsing is
  the dominant cost.
- Whether `Span.SpanContext()` should be usable directly as `Inject`'s
  second argument at the call site without the caller unpacking it,
  which is a helper rather than a contract change.
