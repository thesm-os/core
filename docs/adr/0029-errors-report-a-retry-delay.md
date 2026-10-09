---
adr: 0029
title: An Error Can Report a Retry Delay
status: Accepted
date: 2026-09-28
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0029: An Error Can Report a Retry Delay

## Status

Accepted

## Context

`resilience.Do` waits a full-jitter backoff before each retry, capped at
`RetryConfig.Max`. A dependency that states when a retry can succeed had
no way to pass that through `errs`, so `Do` could retry after
milliseconds when the server had set 30 seconds.

HTTP, gRPC and Google's RPC error model each let a server set the
delay:

- RFC 9110, section 10.2.3, defines `Retry-After` as a number of seconds
  or an HTTP-date.
- gRPC's retry design, proposal A6, sends a delay in milliseconds in the
  `grpc-retry-pushback-ms` metadata key.
- `google.rpc.RetryInfo.retry_delay` states: "Clients should wait at
  least this long between retrying the same request."

Kubernetes' `SuggestsClientDelay` returns the delay a server suggests
and "does not address whether the error *should* be retried". In
`errs`, `Retryable` reports whether to retry, and the delay sets when.

## Decision

We will let an error report a delay through a method
`RetryAfter() time.Duration`, which `errs.RetryAfter` reads and
`errs.WithRetryAfter` attaches. `resilience.Do` will wait the longer of
that delay and its own backoff. It will return at once a failure whose
delay exceeds `RetryConfig.MaxRetryAfter`, because the server knows when
a retry can succeed and the caller knows how long it can wait.

## Alternatives Considered

### A class for throttling

A ninth class would mark a failure that has a delay.

Rejected. A throttled request needs the handling that `Transient`
already names, a retry after a delay. RFC-0015 closes the set of
classes, and the delay is separate from the decision to retry.

### A delay as an instant

The error would report the time at which a retry may start.

Rejected. An instant needs a clock in the producer and another in the
caller, and the skew between the two clocks shifts every delay. A
duration ages only by the time between the error and the retry. A
transport converts an HTTP-date to a duration where it parses the
header.

### No ceiling

`Do` would wait for any delay.

Rejected. A delay of an hour would keep the caller's request waiting
for an hour, and the context deadline bounds only callers that set one.

### `Max` as the ceiling

`Do` would give up on a delay longer than `RetryConfig.Max`.

Rejected. `Max` caps the caller's own backoff, often at a second or
less, and a server's delay is often tens of seconds. A ceiling on the
server's delay is a separate limit from a cap on the caller's schedule.

### A negative delay that stops the retry

`Do` would stop at a negative delay, as gRPC stops at a negative
pushback.

Rejected. A transport that reads a negative pushback returns a failure
whose class is not `Transient`, so `Do` does not retry it. The class
alone determines whether `Do` retries.

## Consequences

**Positive:**

- `Do` waits at least as long as the server set.
- A caller whose ceiling is shorter than the delay gets the failure back
  at once, and reads the delay with `errs.RetryAfter` to schedule the
  next attempt itself.

**Negative:**

- `RetryConfig` has one more required field. Its zero value makes `Do`
  return every failure with a delay, so a caller that does not set a
  ceiling stops retrying such failures as soon as its dependency
  attaches delays.
- A join reports its longest delay, which can be longer than the delay
  of the failure the caller cares about.

**Neutral:**

- `errs.RetryAfter` walks the error tree as `errs.Classify` does, and
  does not allocate. `errs.WithRetryAfter` allocates one wrapper.
- A zero or negative delay counts as none.

## References

- RFC-0015, error classification.
- RFC-0023, resilience primitives.
- RFC 9110, "HTTP Semantics", section 10.2.3,
  <https://www.rfc-editor.org/rfc/rfc9110.html>.
- gRPC proposal A6, "Client Retries", section Pushback,
  <https://github.com/grpc/proposal/blob/master/A6-client-retries.md>.
- `google/rpc/error_details.proto`, message `RetryInfo`,
  <https://github.com/googleapis/googleapis/blob/master/google/rpc/error_details.proto>.
- Kubernetes, `k8s.io/apimachinery/pkg/api/errors`,
  `SuggestsClientDelay`.
- `errs/retryafter.go`: `RetryAfter` and `WithRetryAfter`.
- `resilience/retrier.go`: `Do`.
