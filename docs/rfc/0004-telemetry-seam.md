---
rfc: 0004
title: Telemetry Seam
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-05-06
updated: 2026-05-06
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0004: Telemetry Seam

## Summary

A unified telemetry seam — `telemetry.Reporter` — through which
library code emits metrics (counters, gauges, histograms) and
spans without binding to OpenTelemetry, Prometheus, or any other
backend at compile time. Metric instruments separate
attribute pre-binding from the hot path; the bound emission
methods (`Counter.Add`, `Gauge.Set` / `Add`, `Histogram.Record`)
are zero-allocation by contract on every implementation in this
module. One implementation ships from core: `telemetry/noop`,
which discards every signal.

## Motivation

Library code that emits a metric is on the request hot path: an
HTTP handler, a queue consumer loop, a kernel turn. A naive
backend-coupled emit allocates per call (attribute resolution,
slice construction, interface boxing) and adds latency a
foundation library cannot afford.

The seam exists for three reasons:

- **Hot-path zero-alloc.** Library code emits one signal per
  request. The cost of that signal must be a method call and
  an atomic operation, not slice construction and attribute
  walking. Pre-binding attributes once at instrument
  construction is what makes the hot path zero-alloc — not
  variadic ceremony at the call site.

- **Backend independence.** Consumers under different
  observability regimes (OTel, Prometheus, custom audit
  pipelines) need to wire core libraries to their backend
  without forking. The Reporter interface is the airlock; the
  consumer module supplies the implementation.

- **Cross-signal correlation.** Production observability stacks
  correlate metrics, logs, and traces by extracting the active
  span context, baggage entries, and trace ID from the request
  `context.Context`. Metric emission must carry ctx for that
  correlation to survive into the backend. Drop ctx and
  exemplars, baggage-driven label enrichment, and
  trace-to-metric stitching all break — silently.

## Detailed design

### `Reporter`

```go
type Reporter interface {
    Counter(spec InstrumentSpec) Counter
    Gauge(spec InstrumentSpec) Gauge
    Histogram(spec InstrumentSpec) Histogram
    Tracer(name InstrumentName) Tracer
}
```

`Reporter` is the factory. Subsequent calls with the same
`InstrumentSpec.Name` on the same `Reporter` must return the
same underlying instrument — implementations memoise.

### `InstrumentSpec`

```go
type InstrumentSpec struct {
    Name        InstrumentName
    Description string
    Unit        string
    Bounds      []float64 // histogram-only
}
```

A single value type for instrument metadata across all three
metric primitives. Bounds is histogram-only by intent —
explicitly ignored by Counter and Gauge implementations.
Mirrors OpenTelemetry's option-pattern collapsed into a value
type so consumers don't learn three parallel option chains.

### Metric instruments

```go
type Counter interface {
    Add(ctx context.Context, value int64)
    With(attrs []Attr) Counter
}

type Gauge interface {
    Set(ctx context.Context, value float64)
    Add(ctx context.Context, delta float64)
    With(attrs []Attr) Gauge
}

type Histogram interface {
    Record(ctx context.Context, value float64)
    With(attrs []Attr) Histogram
}
```

Two-phase shape:

- **Init**: `Reporter.Counter` resolves the named instrument;
  `.With(attrs)` pre-binds an attribute set for one
  time-series. Both allocate.
- **Emit**: `Add` / `Set` / `Record` are called many times per
  request and are zero-allocation by contract.

`ctx` is carried on every emit. Adapters read the active
OpenTelemetry span from ctx for **exemplar correlation**, the
W3C baggage entries for per-request label enrichment, and the
trace ID for cross-signal correlation. The cost of carrying
ctx is one interface-pointer pass per call — paid once,
preserves the capability forever.

### Tracing

```go
type Tracer interface {
    Start(ctx context.Context, name SpanName,
        opts ...SpanOption) (context.Context, Span)
}

type Span interface {
    End(err error)
    SetAttributes(attrs []Attr)
    AddEvent(name EventName, attrs []Attr)
    SpanContext() SpanContext
}
```

Tracing is explicitly OUTSIDE the zero-allocation contract.
Span creation, attribute mutation, and event recording all
allocate by design — the OTel SDK records into a span buffer,
samplers may emit, exporters batch. Library code emits a span
per request, not per loop iteration.

`Tracer.Start` uses the variadic functional-options pattern
(`...SpanOption`) since options are typed configuration, not
data. `Span.SetAttributes` and `Span.AddEvent` use slice
parameters — same alloc-discipline reasoning as
`Counter.With`.

### Three name types: `InstrumentName`, `SpanName`, `EventName`

Each typed string has its own vocabulary:

- `InstrumentName` — library identifier passed to
  `Reporter.Tracer` and used as `InstrumentSpec.Name`. Stable
  per library, namespaced like a Go import path.
- `SpanName` — operation name passed to `Tracer.Start`.
  Per-call, namespaced like `"ledger.append.batch"`.
- `EventName` — point-in-time event recorded mid-span via
  `Span.AddEvent`.

Three sibling types prevent call-site confusion: a constant
declared as one of the three cannot accidentally flow into
the wrong parameter. The cost is three type aliases; the
benefit is compile-time discipline at every call site.

### `Counter.Add` and the monotonic precondition

`Counter.Add` requires `value >= 0`. Negative values violate
the monotonic precondition. The contract:

- Production-grade implementations panic with a diagnostic
  message — same precondition-violation discipline as the
  cryptographic seam's `Combine` (RFC-0003). Silent acceptance
  of a negative value would corrupt downstream aggregations
  in ways callers can't detect.
- The `noop` implementation discards. Because no signal is
  emitted regardless, the precondition violation has no
  observable consequence; panicking would force callers to
  guard their emit paths against a signal that doesn't reach
  any backend.

Consumers writing portable code must not pass negatives.

### `Attr` and `Value`

```go
type Attr struct {
    Key   string
    Value Value
}

type Value struct {
    Str   string
    Bytes []byte
    Int   int64
    Float float64
    Kind  AttrKind
    Bool  bool
}
```

Kind-tagged value type. The active field is selected by
`Value.Kind`; the others must be zero. Carrying primitives
directly (instead of `any`) avoids per-call boxing
allocation. `AttrString`, `AttrInt`, `AttrFloat`, `AttrBool`,
and `AttrBytes` are zero-alloc constructors.

`Attr.SlogAttr()` bridges to stdlib `log/slog` without
boxing: every primitive kind maps to the matching
`slog.Value` constructor, zero-alloc. `AttrKindBytes` allocates
one heap copy per call (slog has no zero-copy bytes value);
documented honestly.

### Why slice instead of variadic for `With()`

Variadic `With(attrs ...Attr)` allows two call shapes:

```go
c.With(a, b)       // implicit slice — allocates per call
c.With(slice...)   // spread — no alloc
```

Slice signature `With(attrs []Attr)` enforces the discipline:
caller pre-constructs the slice, no alloc surprise. For a
library that documents strict contracts, the more honest shape
wins. Stdlib slog uses variadic, but slog is not in the
zero-alloc business — every log line allocates.

### `noop` implementation

`telemetry/noop` ships from core. Every type is an
empty-struct receiver with trivial method bodies. The package
is zero-alloc by inspection — `var _ telemetry.Counter =
counter{}`, `(counter).Add(ctx, val) {}`, etc. The test suite
locks the contract via `testing.AllocsPerRun`.

OTel- and Prometheus-backed implementations live in consumer
modules — they pull the OTel SDK or `client_golang`, which the
core module's stdlib-only constraint forbids.

## Alternatives considered

### A. Per-call attrs: `Add(ctx, value, attrs []Attr)`

Caller passes an attribute set per call, precomputed at init.

**Rejected:** functionally equivalent to `.With()` pre-binding
when attrs are reused, but adds a slice-pass to every emit.
Pre-binding is the canonical OTel-shaped pattern and what
adapter implementations expect.

### B. No ctx on emit: `Add(value)`

Drop ctx from the metric methods entirely. Justified by the
observation that atomic adds have no I/O, so cancellation /
deadline don't apply.

**Rejected:** the "ctx is dead weight" argument correctly
identifies that ctx isn't for cancellation here, but misses
that ctx carries observability state — active span, baggage,
trace ID. Drop ctx and exemplar correlation, baggage
enrichment, and metrics-trace stitching break permanently.
The cost of carrying ctx is one interface-pointer pass per
call. The capability is worth more than the cost.

### C. Generic `Attr[T]` types

`Attr[string]`, `Attr[int64]`, `Attr[bool]` as distinct types.

**Rejected:** consumers pass mixed-attribute slices —
`{stringAttr, intAttr, boolAttr}`. With generics, the only
common type for the slice element is `any` or an interface,
and converting concrete `Attr[T]` values into either boxes
them — one heap allocation per attribute. Kind-tagged
union is the correct shape for heterogeneous-without-boxing
in Go (same constraint stdlib slog hit when designing
`slog.Attr`).

### D. Variadic `With(attrs ...Attr)`

The stdlib idiom.

**Rejected:** mixed alloc behaviour at call sites
(`c.With(a, b)` allocates, `c.With(slice...)` doesn't). For
a foundation library documenting strict zero-alloc
contracts, the slice signature enforces the discipline
that the variadic form merely permits.

### E. Broader package: include Logger, Probe, Sampling, Propagation, semconv

A full observability surface in one seam.

**Rejected:** scope creep. `Logger` duplicates stdlib slog —
the `Attr.SlogAttr()` bridge gives consumers a shared
attribute vocabulary across signals without owning a Logger
interface. Propagation has no consumer in `core` (no HTTP /
RPC clients here). Sampling is already configured at the OTel
SDK layer. Probe / semconv are different concerns
(lifecycle signals; attribute-key vocabulary). Each can ship
as its own seam later when a consumer demonstrates the need;
none is a breaking addition.

## Drawbacks

- `Reporter` does not expose a Logger seam. Consumers that
  want unified config across all observability signals must
  wire stdlib slog separately. The `Attr.SlogAttr()` bridge
  closes the attribute-vocabulary gap; the rest is consumer
  policy.
- The slice-signature `With(attrs []Attr)` is slightly less
  ergonomic than variadic for one-off attrs. Documented;
  consumers precompute slices at init regardless.
- `Span.SetAttributes` and `Span.AddEvent` accept slices, not
  variadic. Spans are not zero-alloc, so the variadic form
  would have been ergonomically nicer with no contract
  cost — but the slice form is consistent with metric
  `.With()` and avoids two parallel attribute conventions in
  one package.
- `noop` discards every signal including span context.
  Consumers that depend on `SpanContext()` returning a
  non-zero ID for log correlation must inject a non-noop
  Tracer.

## Open questions

- **Logger seam.** A Logger interface in core would either
  duplicate stdlib slog or wrap it; the `Attr.SlogAttr()`
  bridge already gives consumers a shared attribute vocabulary
  without owning the interface. Defer until a consumer
  demonstrates a use case that slog cannot cover
  (multi-handler routing, log sampling, structured event
  taxonomy).
- **Propagation.** Required when `core` ships HTTP / RPC
  clients. Until then, consumers pull the OTel propagator
  directly.
- **`semconv` subpackage.** Predefined attribute key
  constants (`http.method`, `db.statement`). Useful, but the
  vocabulary needs alignment with consumer needs — defer until
  a consumer shows what they want.

## Unresolved / future work

- A `telemetrytest` conformance suite for third-party
  `Reporter` implementations (zero-alloc invariants, attribute
  pre-binding equivalence, span lifecycle).
- An OTel-backed adapter (in a consumer module, not in core).
- A Prometheus-backed adapter (also in a consumer module).
