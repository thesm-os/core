---
adr: 0039
title: Emit Methods Do Not Panic
status: Accepted
date: 2026-09-29
supersedes: none
superseded-by: none
---

# ADR-0039: Emit Methods Do Not Panic

## Status

Accepted

## Context

RFC-0004 asked production-grade implementations of
`telemetry.Counter.Add` to panic with a diagnostic message for a
negative value, because silent acceptance of the value would corrupt
downstream aggregations. The contracts of `Gauge.Set`, `Gauge.Add` and
`Histogram.Record` did not cover NaN, an infinity or a negative
histogram value.

A caller often computes an increment at run time, as the difference of
two readings, so a negative value can first appear in production. A
panic in the goroutine of a request then ends the process because of a
metric. No method of `telemetry` returns an error, and a caller cannot
act on a failure of telemetry.

The specification and three implementations treat the value
differently:

- The OpenTelemetry error-handling specification requires that "API
  methods MUST NOT throw unhandled exceptions when used incorrectly by
  end users". A library that suppresses an error SHOULD log it.
- The OpenTelemetry metrics API expects a non-negative value for a
  counter and for a histogram, and leaves the validation to
  implementations.
- Prometheus `client_golang` v1.24.1 panics in `Counter.Add` for a
  negative value.
- The OpenTelemetry Go SDK v1.46.0 adds a negative value to a sum that
  it marks as monotonic.
- The OpenTelemetry Java SDK drops a negative value and logs a
  throttled warning.

## Decision

We will specify that a method that emits does not panic for any
argument, because a defect in the value of a metric must not end the
process that the metric observes:

- `Counter.Add` discards a negative value, so no aggregation receives
  it.
- The backend of an implementation defines how `Gauge.Set`, `Gauge.Add`
  and `Histogram.Record` treat NaN and an infinity, and how
  `Histogram.Record` treats a negative value.
- An implementation may report such a value through its own
  diagnostics, such as a rate-limited log line.
- The contract assertions of `coretest/telemetrytest` pass these values
  to the implementation under test.

## Alternatives Considered

### Panic for a negative increment

Rejected. A negative value that a caller computes at run time ends the
process from a call that records a metric.

### Record a negative increment

The OpenTelemetry Go SDK records it.

Rejected. The series of the counter decreases although it is declared
monotonic, and Prometheus's `rate` and `increase` treat a decrease as a
counter reset.

### An unsigned increment

`Add` would take a `uint64`.

Rejected. The conversion moves to the call site, where `uint64(-1)` is
18,446,744,073,709,551,615. Every call with a typed `int64` value
changes.

## Consequences

**Positive:**

- A defect in the value of a metric cannot end the process.
- An adapter that runs the contract assertions receives a negative
  increment, NaN and both infinities, so a panic for one of them fails
  its tests.

**Negative:**

- A negative increment is lost. Only the diagnostics of the adapter
  report it.
- An adapter on Prometheus `client_golang` checks the sign before it
  calls `Counter.Add`. An adapter on the OpenTelemetry Go SDK drops a
  negative increment before the SDK records it.
- The contract assertions check that the methods return. They cannot
  check what an adapter exports.

**Neutral:**

- A test that must detect a negative increment reads the calls that the
  generated `telemetrytest.CounterStub` records.

## References

- RFC-0004, the telemetry seam: `Counter.Add` and the monotonic
  precondition.
- OpenTelemetry, error handling,
  <https://opentelemetry.io/docs/specs/otel/error-handling/>.
- OpenTelemetry, metrics API: Counter and Histogram,
  <https://opentelemetry.io/docs/specs/otel/metrics/api/>.
- Prometheus, query functions: `rate` and `increase`,
  <https://prometheus.io/docs/prometheus/latest/querying/functions/>.
- Prometheus `client_golang` v1.24.1: `prometheus/counter.go`.
- OpenTelemetry Go, `sdk/metric` v1.46.0: `pipeline.go`.
- OpenTelemetry Java, `SdkLongCounter.java`.
