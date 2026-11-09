---
adr: 0085
title: A Context Has the Span of Its Tracer
status: Accepted
date: 2026-11-09
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0085: A Context Has the Span of Its Tracer

## Status

Accepted

## Context

`Tracer.Start` returns a context with the new span. Each implementation
keeps the span under a context key of its own, such as the key of
OpenTelemetry, and no function of `telemetry` reads it. A function that
receives the context of a span cannot read that span.

In a transactional outbox, a store writes each event of a transaction with
the W3C trace context of the call that published the event. A relay
publishes the event later on a goroutine of its own, and its span links to
that call. The store receives the context of the call, takes no tracer, and
writes no trace context today.

The trace API of OpenTelemetry requires an API to extract the span from a
context and to combine a span with a context.

## Decision

We will add the span of a context to `telemetry`, because a function that
receives a context reads the span of the caller there, as the trace API of
OpenTelemetry provides.

```go
func ContextWithSpan(ctx context.Context, s Span) context.Context
func SpanFromContext(ctx context.Context) (Span, bool)
```

- `ContextWithSpan` returns a child of ctx with s, and of
  `context.Background` for a nil ctx. `SpanFromContext` returns the span of
  ctx or of an ancestor, and reports false for a context without a span and
  for a nil ctx.
- `Tracer.Start` returns a context whose `SpanFromContext` returns the span
  that Start returns, unless that span has the zero `SpanContext`. The suite
  of `Tracer` in `coretest/telemetrytest` checks it.
- The noop tracer returns its context unchanged. Its span has the zero
  `SpanContext`, which a component does not inject.
- The tracer of `telemetrytest.Recorder` keeps its spans through
  `ContextWithSpan`, and takes the parent of a span from `SpanFromContext`.

## Alternatives Considered

### A span in the context of every tracer

`Tracer.Start` of the noop tracer would also return a context with its span.

Rejected. The noop `Start` would then allocate a context on every call, and
the allocation ceilings of the clients and servers over it would rise by
one. A context without a span gives a caller the same result as the noop
span, whose span context has no trace ID to inject.

### A span context of the consumer in the context

Each component that starts the span of a call would keep the span context
under a key of its own consumer.

Rejected. The context then has two copies of the span. A span that a
handler starts through the tracer updates the copy of the tracer alone.

## Consequences

**Positive:**

- A function that receives a context injects the span context of its caller
  without a tracer.

**Negative:**

- An implementation of `Tracer` outside core changes its `Start` to keep the
  span through `ContextWithSpan`, and the suite fails one that does not.
- `ContextWithSpan` allocates a context.

**Neutral:**

- An adapter keeps the span under its own key as well, for the parent of
  the spans of its own library.

## References

- Issue 134, <https://github.com/thesm-os/core/issues/134>.
- RFC-0004, the telemetry seam: `Tracer` and `Span`.
- RFC-0020, trace context propagation: `Propagator`.
- OpenTelemetry, the trace API, context interaction,
  <https://opentelemetry.io/docs/specs/otel/trace/api/#context-interaction>.
