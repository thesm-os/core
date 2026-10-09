---
adr: 0030
title: A Class Encodes as Its Name
status: Accepted
date: 2026-09-28
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0030: A Class Encodes as Its Name

## Status

Accepted

## Context

`errs.Class` had `String` and no text encoding. With Go 1.27.1,
`encoding/json` wrote a struct field set to `errs.Transient` as
`{"Class":1}`, and the JSON handler of `log/slog` wrote `"class":1`. The
number means `Transient` only through the order of the constants, which
`errs/class.go` forbids changing for that reason.

A consumer that passes a class across a process boundary, such as a job
queue that records why a job failed, had to choose a spelling of its
own. RFC-0015 argued against that divergence when it added
`version.ErrMismatch`. Stripe sends the same decision over HTTP in its
`Stripe-Should-Retry` header.

## Decision

We will give `Class` the methods `AppendText`, `MarshalText` and
`UnmarshalText` over the names that `String` returns, refuse a value
outside the eight classes and any other text with `errs.ErrUnknownClass`,
and add the names to the encodings that ADR-0016 freezes, because a
class that leaves the process must mean the same thing when it is read
back.

## Alternatives Considered

### The number

A class would keep encoding as its number.

Rejected. A reader of a log line needs the table of constants to read
the number, which encodes only the position of the class in the order.

### Another spelling

A class would encode in lower case, such as `transient`.

Rejected. `String` already returns the Go names, and two spellings of
one class would diverge.

### `Unspecified` for an unknown name

`UnmarshalText` would decode a name it does not know as `Unspecified`.

Rejected. A class that a later version adds would then pass as
`Unspecified` without a sign. The caller that decodes chooses the
fallback.

## Consequences

**Positive:**

- JSON, the JSON handler of `log/slog` and other text formats show the
  name of a class.
- RFC-0015's pending bridge to `log/slog` does not need a function,
  because `slog.Any` writes the name.
- The names are predictable and few, as the OpenTelemetry attribute
  `error.type` requires of its values.

**Negative:**

- No class can be renamed.
- A class that a later version adds is `ErrUnknownClass` to a reader of
  an earlier version.

**Neutral:**

- `MarshalText` allocates its result. `AppendText` into a buffer with
  room and `UnmarshalText` do not allocate.
- A new class still takes the number after the last one.

## References

- RFC-0015, error classification.
- ADR-0016, persisted encodings are frozen before 1.0.
- `errs/class.go`: `AppendText`, `MarshalText` and `UnmarshalText`.
- OpenTelemetry semantic conventions, attribute `error.type`,
  <https://opentelemetry.io/docs/specs/semconv/registry/attributes/error/>.
- Stripe, "The Stripe-Should-Retry header",
  <https://docs.stripe.com/error-low-level>.
