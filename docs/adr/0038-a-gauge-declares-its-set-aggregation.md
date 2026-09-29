---
adr: 0038
title: A Gauge Declares the Aggregation of Its Attribute Sets
status: Accepted
date: 2026-09-29
supersedes: none
superseded-by: none
---

# ADR-0038: A Gauge Declares the Aggregation of Its Attribute Sets

## Status

Accepted

## Context

An adapter that caps the attribute sets of an instrument aggregates the
measurements of every set beyond the cap into one overflow series, as the
OpenTelemetry metrics SDK specification defines. The specification does
not define a rule for a gauge's Last Value aggregation in that series.

`telemetry.Gauge` has `Set` and `Add`, and serves two kinds of value. A
queue depth is additive, and its overflow series is the sum over the
overflowed sets. A lag is not additive, and its overflow series is their
maximum. `InstrumentSpec` has no field that tells an adapter which kind
a gauge measures. RFC-0046 describes the problem and compares the
alternatives.

## Decision

We will add `InstrumentSpec.Aggregation`, of type `GaugeAggregation`,
because an adapter can give a gauge's overflow series a value only when
the gauge declares what its values mean:

| Value | Name | Overflow series |
|---|---|---|
| 0 | `GaugeAggregationUnspecified` | Not defined |
| 1 | `GaugeAggregationSum` | The sum of the values of the unreleased instruments of every overflowed set |
| 2 | `GaugeAggregationMax` | The largest value that an instrument of an overflowed set recorded in the export interval |

- In a sum gauge, each instrument has a value of its own, which `Set`
  replaces and `Add` changes. The value of an attribute set is the sum of
  the values of its unreleased instruments.
- A caller sets every instrument of a maximum gauge in every export
  interval.
- `Reporter.Counter` and `Reporter.Histogram` ignore the field.

## Alternatives Considered

### An UpDownCounter beside Gauge

Core would add the UpDownCounter of the OpenTelemetry API for additive
values.

Rejected. Every caller of `Gauge.Set` that measures a depth would move to
another instrument, and a maximum would still need a declared rule.

### One value per attribute set for a sum gauge

`Set` would replace the value of the set, whichever instrument set it.

Rejected. The adapter would then keep the value of every overflowed set,
which is the memory that the cardinality limit bounds.

### The largest current value for a maximum gauge

Rejected. When the largest value falls, the adapter needs the values of
all other overflowed sets to find the next one.

## Consequences

**Positive:**

- The overflow series of a sum gauge and of a maximum gauge have values
  that mean what the gauge measures.
- An adapter keeps the overflow total of a sum gauge as one number, and
  `Set` and `Add` do not allocate.

**Negative:**

- Two instruments of one set of a sum gauge add their values. A caller
  that wants each `Set` to replace the set's value binds one instrument
  per set.
- The overflow maximum is correct only when callers set every instrument
  in every export interval.
- An adapter on the OpenTelemetry SDK computes the overflow maximum
  itself, because the SDK defines no rule for it.

**Neutral:**

- The zero value, `GaugeAggregationUnspecified`, keeps today's meaning of
  every gauge.
- A sum gauge is what the OpenTelemetry API calls an UpDownCounter, and
  an adapter on its SDK maps it onto one.

## References

- RFC-0046, released instruments and gauge aggregation.
- ADR-0037, a bound instrument is released.
- OpenTelemetry, metrics API: Gauge and UpDownCounter,
  <https://opentelemetry.io/docs/specs/otel/metrics/api/>.
- OpenTelemetry, metrics SDK: the overflow attribute set,
  <https://opentelemetry.io/docs/specs/otel/metrics/sdk/>.
