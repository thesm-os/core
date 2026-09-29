---
adr: 0037
title: A Bound Instrument Is Released
status: Accepted
date: 2026-09-29
supersedes: none
superseded-by: none
---

# ADR-0037: A Bound Instrument Is Released

## Status

Accepted

## Context

`Counter.With`, `Gauge.With` and `Histogram.With` bind an attribute set
and return an instrument for it, and no method ends that binding. An
adapter that aggregates with cumulative temporality keeps every set that
it has seen, as the OpenTelemetry metrics SDK specification requires.
The sets of retired entities, such as old tenants, versions or shards,
then occupy the instrument's cardinality limit and memory until the
process restarts.

The calls that an adapter receives do not show that a caller stopped
using a set. RFC-0046 describes the problem and compares the
alternatives.

## Decision

We will declare `Release()` on `Counter`, `Gauge` and `Histogram` as a
required method, because an adapter can forget an attribute set only
when its callers state that they stopped using it, and a required method
makes every implementation, decorators included, state what it does
with that statement.

- After `Release` returns, the instrument records nothing.
- A second `Release`, and `Release` on an instrument that a `Reporter`
  method returned, do nothing.
- `Release` is safe for concurrent use with every method of every
  instrument, and does not allocate.
- An adapter that keeps state per attribute set may forget a set after
  the last instrument bound to it is released and its last measurement
  is exported.

## Alternatives Considered

### An optional Releaser interface

`Release` would be an optional interface that a helper asserts. This is
how `sign.SignContext` treats `sign.ContextSigner`.

Rejected. A decorator of an instrument that does not forward the
optional method keeps every set alive. Nothing reports it.

### Delete by attribute set

The unbound instrument would delete a set by its attributes.
Prometheus's `client_golang` works this way with `MetricVec.Delete`.

Rejected. A deletion removes the set for every holder of an instrument
bound to it. A holder that still uses its instrument then writes into a
series that no longer exports.

## Consequences

**Positive:**

- An adapter with cumulative temporality can forget the sets of retired
  entities and keep an instrument within its cardinality limit.
- The lifecycle of an instrument has three explicit steps: bind, emit
  and release.

**Negative:**

- Every implementation of the three interfaces adds the method:
  `telemetry/noop`, the stubs of `coretest/telemetrytest` and every
  consumer's adapter.
- An adapter checks the released state on each emit, or points a
  released instrument at storage that discards its writes.
- An adapter that cannot forget a set, such as one on the OpenTelemetry
  Go SDK with cumulative temporality, gains only the end of the
  instrument.

**Neutral:**

- A caller that never calls `Release` keeps today's behaviour.
- A `With` after the last `Release` of a set starts a new series, and a
  cumulative counter or histogram of it starts from zero.

## References

- RFC-0046, released instruments and gauge aggregation.
- RFC-0004, the telemetry seam.
- OpenTelemetry, metrics SDK: cardinality limits,
  <https://opentelemetry.io/docs/specs/otel/metrics/sdk/>.
