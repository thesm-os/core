---
adr: 0092
title: A Reporter Reads a Gauge at Each Collection
status: Accepted
date: 2026-11-12
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0092: A Reporter Reads a Gauge at Each Collection

## Status

Accepted

## Context

`telemetry` has synchronous instruments alone. A library that exports a
value calls `Gauge.Set` or `Gauge.Add` at each change of the value. Some
values change on a hot path, such as the bytes of the write queues of a
storage engine and the bytes of its block cache. A call of a bound gauge of
an adapter of a metrics SDK then runs on each change, and it costs more than
the atomic addition that changes the value.

The metrics API of OpenTelemetry defines asynchronous instruments, whose
callback the SDK calls only at a collection. Prometheus defines a GaugeFunc,
a gauge whose value a function returns at the time of a collection.

## Decision

We will add an optional interface of a `Reporter` that registers a function
that reads the value of a gauge, because the hot path then changes the value
alone and the reporter reads it only at a collection.

```go
type Observer interface {
	Observe(spec InstrumentSpec, attrs []Attr, read func(ctx context.Context) float64) Observation
}

type Observation interface {
	Release()
}
```

- A library asserts `Observer` on its `Reporter`. A Reporter does not have to
  implement it.
- The reporter calls read with the context of each collection until
  `Release`. `InstrumentSpec.Aggregation` states how the reads of the
  registrations of one attribute set combine, as it does for the gauges of
  `Gauge.With`.
- The no-op Reporter implements `Observer` and does not call a read
  function.
- `telemetrytest.Recorder` implements `Observer`. `Recorder.Value` calls the
  read functions of a series, and `Recorder.Bound` counts the observations
  that `Release` has not ended.

## Alternatives Considered

### Observe as a method of Reporter

Rejected. Every implementation of `Reporter` outside core would have to add
the method before it builds again.

### A goroutine that sets the gauges at an interval

The library would set its gauges on a ticker.

Rejected. The values are stale between two ticks, the interval is a guess,
and the goroutine runs on an idle process too.

## Consequences

**Positive:**

- A hot path that changes an exported value does not call an instrument.
- A test reads the value of a series of an observed gauge through
  `Recorder.Value`.

**Negative:**

- A library with a Reporter that does not implement `Observer` exports
  nothing for an observed gauge, unless it also sets a gauge itself.
- A read function runs on a goroutine of the reporter, so it reads its value
  with an operation that is safe for concurrent use.

**Neutral:**

- An adapter of OpenTelemetry implements `Observer` with an asynchronous
  gauge of its SDK.

## References

- Issue 152, <https://github.com/thesm-os/core/issues/152>.
- OpenTelemetry, the metrics API, asynchronous instruments,
  <https://opentelemetry.io/docs/specs/otel/metrics/api/>.
- Prometheus, `GaugeFunc` of client_golang,
  <https://pkg.go.dev/github.com/prometheus/client_golang/prometheus#GaugeFunc>.
