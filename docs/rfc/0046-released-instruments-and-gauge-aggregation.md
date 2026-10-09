---
rfc: 0046
title: Released Instruments and Gauge Aggregation
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-09-29
updated: 2026-09-29
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0037, ADR-0038
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0046: Released Instruments and Gauge Aggregation

## Summary

We propose two additions to `telemetry`:

- `Counter`, `Gauge` and `Histogram` declare `Release`, which ends a
  bound instrument that `With` returned. A released instrument records
  nothing, and an adapter that keeps state per attribute set may forget
  a set once the last instrument bound to it is released.
- `InstrumentSpec` gains `Aggregation`, which states how the values of a
  gauge's attribute sets combine: as a sum, as a maximum, or not at all.
  An adapter that caps the attribute sets of an instrument uses it to
  give the overflow series of a gauge a defined value.

`Release` is a new method of three interfaces, so every implementation
has to add it. `Aggregation` is a new field whose zero value keeps
today's behaviour.

## Motivation

### A bound instrument has no end

`Counter.With`, `Gauge.With` and `Histogram.With` bind an attribute set
and return an instrument for it (`telemetry/counter.go:54`,
`gauge.go:37`, `histogram.go:23`). No method ends that binding, so an
adapter cannot learn that its caller stopped using a set. A library
binds sets for entities that come and go, such as tenants, versions or
shards, and each retired entity leaves its sets behind.

An adapter that aggregates with cumulative temporality keeps every set
that it has seen. The OpenTelemetry metrics SDK specification requires
it: "Aggregators for synchronous instruments with cumulative temporality
MUST continue to export all attribute sets that were observed prior to
the beginning of overflow." Each retired set then occupies a place in
the instrument's cardinality limit, and its memory, until the process
restarts. The OpenTelemetry Go SDK records the problem in its cumulative
aggregators: "This will use an unbounded amount of memory if there are
unbounded number of attribute sets being aggregated"
(`sdk/metric` v1.46.0, `internal/aggregate/sum.go:189`, issue #3006).

Delta temporality does not have the problem. The specification applies
the limit to "the number of Metric Points that can be collected during a
collection cycle", and a set that receives no measurement in a cycle has
no point in it. Core cannot choose the temporality of a consumer's
exporter, and a Prometheus scrape endpoint is cumulative.

### A gauge's overflow series has no defined value

The specification caps the attribute sets of an instrument and
aggregates the measurements of every set beyond the cap into one
overflow set, `otel.metric.overflow=true`. The overflow series of a
counter or a histogram has a correct total, because their measurements
add. The specification does not define a rule for the Last Value
aggregation of a gauge in the overflow set.

The right rule depends on what the gauge measures. The specification of
the metrics API calls a Gauge "non-additive", with the example of "the
background noise level", and sends additive values, such as "the number
of items in a queue", to an UpDownCounter. Core's `Gauge` has both
`Set` and `Add` (`telemetry/gauge.go:22`) and serves both kinds:

- The overflow series of a queue depth is the sum of the depths of the
  overflowed sets.
- The overflow series of a lag is their maximum.
- The last value of whichever set reported last is neither.

`InstrumentSpec` (`telemetry/instrument.go:29`) has no field that tells
an adapter which of the two a gauge is.

## Detailed design

### Components

| Component | Change |
|---|---|
| `telemetry/counter.go`, `gauge.go`, `histogram.go` | `Release` on each interface |
| `telemetry/instrument.go` | `GaugeAggregation` and `InstrumentSpec.Aggregation` |
| `telemetry/doc.go` | The lifecycle of an instrument: bind, emit, release |
| `telemetry/noop` | `Release` does nothing |
| `coretest/telemetrytest` | The regenerated stubs, suites and benchmarks, and the contract assertions of `Release` |

### API

```go
type Counter interface {
    Add(ctx context.Context, value int64)
    With(attrs []Attr) Counter

    // Release ends this bound instrument. After Release returns, Add
    // records nothing. A second Release, and Release on an instrument
    // that With did not return, do nothing.
    Release()
}

// Gauge and Histogram declare the same Release.

// GaugeAggregation states how the values of a gauge's attribute sets
// combine, in the overflow series and in any total across sets.
type GaugeAggregation uint8

const (
    // GaugeAggregationUnspecified defines no combination. The values of
    // different sets are non-additive.
    GaugeAggregationUnspecified GaugeAggregation = 0

    // GaugeAggregationSum adds the values, as for a depth or a count.
    GaugeAggregationSum GaugeAggregation = 1

    // GaugeAggregationMax takes the largest value, as for a lag or an
    // age.
    GaugeAggregationMax GaugeAggregation = 2
)

type InstrumentSpec struct {
    Name        InstrumentName
    Description string
    Unit        string
    Bounds      []float64

    // Aggregation states how the values of a gauge's attribute sets
    // combine. Reporter.Counter and Reporter.Histogram ignore it.
    Aggregation GaugeAggregation
}
```

`Aggregation` follows `Bounds`, a field that one kind of instrument
reads and the other two ignore.

### Release

A bound instrument is a value that `With` returns. `Release` ends it:

- After `Release` returns, `Add`, `Set` and `Record` on the instrument
  record nothing.
- A second `Release` does nothing.
- `Release` on an instrument that a `Reporter` method returned does
  nothing. That instrument has no binding to end.
- `Release` of one instrument does not end another. After
  `b := a.With(attrs)`, releasing `a` leaves `b` bound.
- `Release` is safe for concurrent use with every method of every
  instrument. An emit on the same instrument that runs concurrently with
  `Release` may record or may not.
- `Release` does not allocate.

An adapter that keeps state per attribute set counts the unreleased
instruments of each set. When the count falls to zero, and the adapter
has exported the set's last measurement, it may forget the set and free
its place in the cardinality limit. A later `With` of the same set
starts a new series: a cumulative counter or histogram starts from zero,
with a new start time.

When an adapter cannot forget a set, it keeps the set, and `Release`
then only ends the instrument. The OpenTelemetry Go SDK has no operation
that forgets a set under cumulative temporality, so an adapter on it is
such an adapter.

The emit methods do not allocate. An adapter checks a flag of the
instrument on each emit, or points the released instrument at storage
that discards its writes. `telemetry/noop` implements `Release` as an
empty method.

### Gauge aggregation

| `Aggregation` | Value of an attribute set | Value of the overflow series |
|---|---|---|
| `GaugeAggregationUnspecified` | The last value that any of its instruments recorded | Not defined |
| `GaugeAggregationSum` | The sum of the values of its unreleased instruments | The sum of the values of the unreleased instruments of every overflowed set |
| `GaugeAggregationMax` | The last value that any of its instruments recorded | The largest value that any instrument of an overflowed set recorded in the export interval |

A sum gauge is additive, as an UpDownCounter of the OpenTelemetry API is:

- Each instrument has a value of its own. `Set(v)` makes `v` the
  instrument's value, and `Add(d)` adds `d` to it.
- `Release` removes the instrument's value from its set.
- Two instruments of one set that set 5 and 7 give the set the value 12.
  A caller that wants each `Set` to replace the set's value binds one
  instrument per set.

Because each instrument keeps its own value, an adapter keeps the
overflow total as one number. `Set(v)` adds `v` minus the instrument's
previous value, with a compare-and-swap on the instrument's value, and
`Release` subtracts the value. The adapter does not keep state per
overflowed set, and `Set` and `Add` do not allocate.

A maximum gauge is non-additive with a declared rule for overflow:

- The overflow series reports the largest value that an instrument of
  an overflowed set recorded in the export interval.
- A caller sets every instrument of a maximum gauge in every export
  interval. An instrument that records nothing in an interval does not
  count toward that interval's maximum.
- A released instrument counts toward no later interval.

An adapter on the OpenTelemetry SDK maps a sum gauge onto an
UpDownCounter, whose sum aggregation adds the overflowed sets. It maps a
maximum gauge onto a Gauge, and computes the overflow maximum itself,
because the SDK defines no rule for it.

### Failure handling

| Condition | Behaviour |
|---|---|
| An emit on a released instrument | Records nothing |
| `Release` twice | The second call does nothing |
| `Release` on an instrument that a `Reporter` method returned | Does nothing |
| An emit concurrent with `Release` of the same instrument | Records or not, and does not panic |
| `Aggregation` set on a counter or a histogram | Ignored |
| An `Aggregation` value above 2 | Treated as `GaugeAggregationUnspecified` |

No method returns an error, as for every other method of `telemetry`.

### Tests

| # | Guarantee |
|---|---|
| 1 | The contract assertions of `coretest/telemetrytest` check for each instrument that `Release` returns, that a second `Release` returns, that `Release` on an unbound instrument returns, and that an emit after `Release` returns |
| 2 | An assertion runs `Release` and emits on the same instrument from several goroutines, under the race detector |
| 3 | `telemetry/noop` passes the contract assertions, and a benchmark reports 0 allocations for `Release` |
| 4 | A test pins the values 0, 1 and 2 of the three `GaugeAggregation` constants, and the zero value of `InstrumentSpec.Aggregation` |
| 5 | The regenerated stubs record every call of `Release` |

### Compatibility

- **Breaking:** every implementation of `Counter`, `Gauge` and
  `Histogram` adds `Release`. In core these are `telemetry/noop` and the
  stubs of `coretest/telemetrytest`, which testkit regenerates. A
  consumer's adapter adds the method, and an adapter without state per
  set implements it as an empty method.
- A caller that never calls `Release` keeps today's behaviour.
- `Aggregation` is a new field. Its zero value, `GaugeAggregationUnspecified`,
  keeps today's meaning of every gauge.

## Alternatives considered

### A. An optional Releaser interface

`Release` would be an optional interface that a helper such as
`telemetry.Release(instrument)` asserts. This is how `sign.SignContext`
treats `sign.ContextSigner`.

**Why not:** a decorator of an instrument that does not forward the
optional method would keep every set alive. Nothing would report it. A
required method makes every implementation state what it does, and the
method of `telemetry/noop` is one line.

### B. Delete by attribute set

The unbound instrument would delete a set by its attributes. Prometheus's
`client_golang` (v1.24.1) works this way with `MetricVec.Delete`.

**Why not:** a deletion removes the set for every holder of an
instrument bound to it. A holder that still uses its instrument then
writes into a series that no longer exports. `Release` ends one
instrument, and the set remains while another instrument uses it.

### C. Delta temporality alone

A consumer would export with delta temporality, under which a set without
measurements leaves the next collection.

**Why not:** core cannot choose a consumer's temporality, and a
cumulative exporter, such as a Prometheus scrape endpoint, keeps every
set.

### D. A time to live for attribute sets

The adapter would forget a set that received no measurement for a period.

**Why not:** an idle set and a released set look the same to the
adapter. A gauge whose value does not change between rare updates would
lose its series.

### E. An UpDownCounter beside Gauge

Core would add the UpDownCounter of the OpenTelemetry API for additive
values, and keep `Gauge` for non-additive ones.

**Why not:** every caller of `Gauge.Set` that measures a depth would move
to another instrument, and a maximum would still need a declared rule
for overflow.

### F. One value per attribute set for a sum gauge

`Set` would replace the value of the set, whichever instrument set it.

**Why not:** the adapter would then keep the value of every overflowed
set to compute the overflow total, which is the memory that the
cardinality limit bounds.

### G. The largest current value for a maximum gauge

The overflow series would report the largest current value of the
overflowed sets.

**Why not:** when the largest value falls, the adapter needs the values
of all other overflowed sets to find the next one.

## Drawbacks

- Adding `Release` breaks every implementation of the three interfaces.
- Two instruments of one set of a sum gauge add their values. A caller
  that wants each `Set` to replace the set's value binds one instrument
  per set.
- The overflow maximum is correct only when callers set every instrument
  in every export interval.
- `Release` frees state only in an adapter that can forget a set. An
  adapter on the OpenTelemetry Go SDK with cumulative temporality
  cannot.
- An adapter checks the released state on each emit, or keeps storage
  that discards writes.

## Open questions

None.

## Unresolved / future work

- A conformance suite that checks an adapter's cardinality limit and the
  values of its overflow series through a recording exporter.

## References

- OpenTelemetry, metrics API: Gauge and UpDownCounter,
  <https://opentelemetry.io/docs/specs/otel/metrics/api/>, read
  2026-09-29.
- OpenTelemetry, metrics SDK: cardinality limits, the overflow attribute
  set and the Last Value aggregation,
  <https://opentelemetry.io/docs/specs/otel/metrics/sdk/>, read
  2026-09-29.
- OpenTelemetry Go, `sdk/metric` v1.46.0:
  `internal/aggregate/sum.go:189` and `internal/aggregate/lastvalue.go:131`
  and `:190`, issue #3006.
- Prometheus `client_golang` v1.24.1: `MetricVec.Delete` and
  `DeleteLabelValues`.
- RFC-0004, the telemetry seam.
- ADR-0022, decorators expose what they wrap.
- ADR-0037, a bound instrument is released.
- ADR-0038, a gauge declares the aggregation of its attribute sets.
