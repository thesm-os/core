---
adr: 0009
title: Logging Is log/slog
status: Accepted
date: 2026-08-03
supersedes: none
superseded-by: none
---

# ADR-0009: Logging Is log/slog

## Status

Accepted

## Context

RFC-0004 defined metric and trace seams and left logging open. The
question has stayed open because the third signal looks like it should
be symmetrical with the first two: if `core` defines `Counter` and
`Span`, it appears to owe a `Logger`.

It does not, and the reason is that the seam already exists. `log/slog`
ships `Handler` in the standard library — an interface with exactly the
shape a logging seam needs, already implemented by every logging
backend that matters, already the target consumers write adapters
against. A second interface in `core` would not decouple anything that
`slog.Handler` does not already decouple. It would add a spelling.

What is genuinely missing is smaller and concrete. `telemetry.Attr`
converts to `slog.Attr` in one direction only, so a caller holding
pre-bound telemetry attributes cannot attach them to a log record
without rebuilding them by hand. And there is no documented path from
an active span to a correlated log record, so the trace identifier that
would join the two signals is available and unused.

## Decision

`core` defines no `Logger` interface. `log/slog.Handler` is the logging
seam for the ecosystem.

`core` ships the two pieces the standard library cannot: a bidirectional
bridge between `telemetry.Attr` and `slog.Attr`, and a documented,
tested path from a `telemetry.SpanContext` to the attributes that
correlate a log record with the active trace.

## Alternatives Considered

### Define a `Logger` interface in `telemetry`

Rejected. It would be a second spelling of `slog.Handler` with no added
decoupling, and every consumer would write an adapter between two
interfaces that already describe the same thing. The symmetry argument
— `core` defines the other two signals, so it should define this one —
does not survive the observation that `core` defines metric and trace
seams precisely because the standard library defines neither.

### Ship nothing and leave RFC-0004's question open

Rejected. The one-way `Attr` conversion is a real gap with a real
consequence: correlated logging is left to each caller to rediscover,
and the trace identifier that makes it possible is sitting unused in a
type `core` owns.

### Re-export or wrap `slog.Handler` under a `core` name

Rejected. A type alias adds an import path without adding a contract,
and a wrapper adds a layer that every implementation must be unwrapped
through. Consumers should import `log/slog` directly.

## Consequences

**Positive:**

- RFC-0004's open question is closed with a decision rather than a
  deferral.
- Consumers write one adapter, against a standard-library interface,
  instead of two.
- Attribute values pre-bound for metrics can be reused on log records
  without reconstruction, which keeps the two signals labelled
  identically by construction rather than by convention.

**Negative:**

- The three signals are asymmetrical in `core`: two are seams it owns,
  one is a seam it defers to. A reader expecting symmetry has to read
  this ADR to learn why.
- The bridge is a maintenance surface tied to `slog.Attr`'s shape. If
  the standard library extends `slog.Value`'s kinds, the bridge must
  follow or silently lose fidelity.
- `core` gains a dependency on `log/slog`'s design decisions in a place
  where it previously had none.

**Neutral:**

- No change to the metric or trace seams. This decision constrains what
  `core` will not add.
