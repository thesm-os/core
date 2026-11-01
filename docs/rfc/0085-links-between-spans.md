---
rfc: 0085
title: Links Between Spans
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-11-01
updated: 2026-11-01
discussion: https://github.com/thesm-os/core/issues/113
supersedes: none
superseded-by: none
produces-adr: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0085: Links Between Spans

## Summary

We propose links between spans in package `telemetry`. `WithLinks` links
the span that `Tracer.Start` creates to other spans, and a `Tracer` reads
the links with `Links`. A batch that serves the requests of many callers
then links its span to the span of each caller. The links exist when the
span starts, so the sampler of a `Tracer` can read them.

## Motivation

A batch that serves the requests of many callers runs on a goroutine of
its own, under a context of its own. A group commit and a batch of
timestamp requests work this way. The span of the batch has no parent
among its callers:

- `SpanOption` sets a remote parent and a kind alone
  (`telemetry/span_option.go:18-26`).
- `Span` has `End`, `SetAttributes`, `AddEvent` and `SpanContext`, and no
  method adds a link.
- RFC-0004 and RFC-0020 do not mention links.

The trace of each caller then ends at the hand-off to the batch. The trace
of the batch shows none of its callers.

A link of the trace API of OpenTelemetry consists of the `SpanContext` of
the span to link to and of attributes that describe the link. The API
states: "During `Span` creation, a user
MUST have the ability to record links to other `Span`s." It adds that
samplers may consider the links of a span at its creation, and may not
consider the links that a span adds later.

## Detailed design

### API

```go
// Link links a span to another span, as a link of OpenTelemetry does.
// SpanContext is the identity of the other span, and Attributes describe
// the link.
type Link struct {
	SpanContext SpanContext
	Attributes  []Attr
}

// WithLinks links the new Span to the spans of links. A Tracer reads the
// links with Links.
func WithLinks(links []Link) SpanOption

// Links returns the links that WithLinks set in opts, for a Tracer that
// creates a span.
func Links(opts []SpanOption) []Link
```

### Rules

- `WithLinks` takes the slice by reference, as `Counter.With` takes its
  attributes. The caller does not change the slice or the attributes of its
  links after the call.
- An empty slice does not set links.
- When two or more options set links, the last of them applies, as it does for
  `ApplySpanOptions` and `RemoteParent`. A caller with two groups of links
  joins them into one slice.
- `Links` returns the slice of that option. It returns nil when no option
  sets links.
- A `Tracer` applies its own rules to the links. The SDK of OpenTelemetry
  keeps 128 links per span by default, as its `LinkCountLimit`, and evicts
  the oldest link beyond it. It keeps a link to an empty `SpanContext` only
  when the link has attributes or a trace state.
- `SpanOption` gains the field of the links, and grows from 80 to 104
  bytes. The thresholds of hugeParam and rangeValCopy in `.golangci.yml`
  are 256 bytes.
- `telemetry/noop` ignores the links.
- `telemetrytest.Span` records the links of a span in `Links`, in a copy of
  the slice of the option.
- The check `TracerStartAppliesItsOptions` of the suite of `Tracer` starts a
  span with a kind option and a link option.

### Example

```go
links := make([]telemetry.Link, 0, len(calls))
for _, c := range calls {
	links = append(links, telemetry.Link{SpanContext: c.span})
}

ctx, span := tracer.Start(ctx, commitSpan, telemetry.WithLinks(links))
defer span.End(err)
```

### Invariants

1. `Links` returns the slice of the last option that sets links, by
   reference.
2. `ApplySpanOptions` and `RemoteParent` do not read the links, and `Links`
   does not read the kind or the parent.
3. `WithLinks`, `Links`, `ApplySpanOptions` and `RemoteParent` do not
   allocate.

### Failure handling

| Condition | Result |
|---|---|
| `WithLinks` of an empty slice | The option sets no links |
| A link to a `SpanContext` with an empty `TraceID` | `Links` returns it. The SDK of OpenTelemetry keeps it only with attributes or a trace state |
| More links than a `Tracer` keeps | The `Tracer` drops links. The SDK of OpenTelemetry evicts the oldest and counts them as dropped |

### Tests

| # | Guarantee |
|---|---|
| 1 | `Links` returns nil for no options, for options that set no links and for `WithLinks` of an empty slice |
| 2 | `Links` returns the slice of `WithLinks` by reference |
| 3 | `Links` returns the slice of the last option that sets links |
| 4 | `ApplySpanOptions` and `RemoteParent` return the same results with `WithLinks` among the options as without it |
| 5 | The allocation tests and the benchmarks state 0 allocations for `WithLinks` and `Links` |
| 6 | The Recorder of `telemetrytest` keeps the links of each span in a copy |
| 7 | The specs cover every statement, and dokimi-mutate-go detects every mutant |

### Migration

A `Tracer` of an adapter reads `Links` when it creates a span and passes
the links to its SDK. A caller of the existing API does not change.

## Alternatives considered

### A. A method of Span that adds a link

`Span` would gain an `AddLink` method, as the span of OpenTelemetry has
since version 1.31.0 of its specification.

**Why not:** a sampler runs when a span starts, so it does not read a link
that a span adds later. The specification states that links at
creation are preferred for that reason. A batch knows its callers before it
starts its span. The verdict changes when a caller learns of a related span
only after its own span started.

### B. Links joined across options

`Links` would return the links of the options in one slice, as
`trace.WithLinks` of OpenTelemetry for Go does.

**Why not:** `Links` would then allocate for two or more such options.
`ApplySpanOptions` and `RemoteParent` apply the last option, and `Links`
follows them.

### C. A variadic WithLinks

`WithLinks` would take `links ...Link`.

**Why not:** a call with literal links would allocate the slice of the
arguments at each call. `Counter.With` and `Span.SetAttributes` take a
slice, and the caller decides where it allocates the slice.

## Drawbacks

- Every `SpanOption` grows by 24 bytes, from 80 to 104.
- A `Tracer` that does not read `Links` drops the links without an error.
  The suite of `Tracer` checks that `Start` accepts a link option, not
  that the `Tracer` exports the links.

## Open questions

None.

## Unresolved / future work

None.

## References

- Issue 113, <https://github.com/thesm-os/core/issues/113>.
- RFC-0004, the telemetry seam.
- RFC-0020, trace context propagation.
- OpenTelemetry, the trace API, links:
  <https://opentelemetry.io/docs/specs/otel/trace/api/#link>.
- OpenTelemetry, the limits of a span of the SDK:
  <https://opentelemetry.io/docs/specs/otel/trace/sdk/#span-limits>.
