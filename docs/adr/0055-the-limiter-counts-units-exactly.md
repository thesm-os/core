---
adr: 0055
title: The Limiter Counts Units Exactly
status: Accepted
date: 2026-10-15
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0055: The Limiter Counts Units Exactly

## Status

Accepted

## Context

`resilience.Limiter` kept its bucket as one time, due, in whole
nanoseconds. A call of n units moved due forward by the time in which the
bucket gains n units, rounded up to a whole nanosecond. A cancelled call
moved due back by the same rounded time, and the capacity of the bucket
was the rounded time of Burst units.

Above one unit per nanosecond the rounding admits more units than the
contract allows:

- At 2 units per nanosecond, a call of 3 units costs 2 ns. The refund of a
  cancelled call of 3 units then admits two calls of 2 units.
- A full bucket of Burst 3 at the same rate admits two calls of 2 units at
  once, 4 units in all.

A probe ran the trace of issue 52 on core at c87154f with Go 1.27.2 under
the race detector. In the trace each of three calls reserves one chunk,
and two of the calls are cancelled. The units of the successful calls in
a window of 1 ns then exceed the bound: the Rate times the window, plus
Burst, plus the chunks of the three open reservations.

| Rate, units per second | Burst and chunk | Successful units | Bound |
|---|---|---|---|
| 2,000,000,000 | 3 | 15 | 14 |
| 3,000,000,000 | 4,096 | 16,390 | 16,387 |
| 2,000,000,000 | 1,048,575 | 4,194,303 | 4,194,302 |

A caller that charges the bytes of a disk runs above one unit per
nanosecond. Such a caller sizes its charges by `Limiter.Burst`, and its
accounting needs the exact bound.

## Decision

We will keep due and the capacity of the bucket exactly:

- Each time is a whole number of nanoseconds and a remainder below Rate,
  in units of a Rate-th of a nanosecond. The quotient and the remainder of
  n times 10^9 divided by Rate are the time of n units.
- A reservation adds the exact time of its units, and a cancellation
  subtracts the same time. A refund then returns exactly the units that
  its reservation took.
- `AllowN` compares exact times, so the bucket never has more than Burst
  units.
- Only the timer of `WaitN` rounds. It waits until the next whole
  nanosecond, so no wait ends before the bucket has gained its units.

The API of the Limiter does not change. This decision supersedes the
bucket of one time in int64 nanoseconds that RFC-0050 describes.

## Alternatives Considered

### A bound with the rounding in it

The documentation would state the slack of the rounding: a few units per
call above one unit per nanosecond.

Rejected. Every caller would then repeat the arithmetic of the Limiter to
size its work by Rate and Burst.

### Time scaled by Rate

Every time would be a count of Rate-ths of a nanosecond.

Rejected. Every call would multiply in 128 bits, because the product of a
time and the Rate overflows int64 after seconds. A remainder beside the
nanoseconds gives the same exact result in int64.

### Tokens in floating point

The bucket would count tokens as a float64, as golang.org/x/time/rate
does.

Rejected. A float64 rounds too. RFC-0050 rejected floating point for the
same reason.

### A count of tokens beside a time of the last refill

The bucket would count its tokens in billionths of a unit. Each call would
refill them from the time since the last call.

Rejected. It would replace the generic cell rate algorithm and its order
of waiting calls, although the count is exact as well. A remainder keeps
both.

## Consequences

**Positive:**

- A refund returns exactly the units of its reservation.
- At any Rate, a full bucket admits at most Burst units at once.
- The units of the successful calls in a window stay within the Rate
  times the window, plus Burst, plus the units of the reservations open
  when the window starts.

**Negative:**

- The Limiter keeps two more fields, and each call adds or subtracts with
  a carry.

**Neutral:**

- At a Rate of at most one unit per nanosecond the rounding never admitted
  an extra unit. Such a Limiter now charges each call its exact time
  instead of a time rounded up, so many calls no longer lose a few
  nanoseconds of the rate.

## References

- Issue 52, <https://github.com/thesm-os/core/issues/52>.
- RFC-0050, the rate limiter, the section The bucket as one time.
- `resilience/limiter.go`: `Limiter`, `NewLimiter`, `AllowN`, `WaitN`,
  `reserve`, `release`, `after` and `cost`.
- The ATM Forum, Traffic Management Specification 4.0, the generic cell
  rate algorithm.
