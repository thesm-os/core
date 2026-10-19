---
adr: 0073
title: A Limiter Changes Its Rate in Place
status: Accepted
date: 2026-10-19
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0073: A Limiter Changes Its Rate in Place

## Status

Accepted

## Context

`resilience.Limiter` kept the Rate and the Burst of its construction.
RFC-0050 stated that a caller that changes its rate builds a new Limiter, and
that the calls that wait on the old one finish at the old rate. A new Limiter
starts with a full bucket, so each change admitted a burst beyond the rate.

The probe of issue 87 ran on core at 506c09b with Go 1.27.2. It took chunks
of 64 KiB at 1,000,000 units a second with a burst of one second, for 10
seconds of a fake clock. At each change it built a new Limiter of the same
rate:

| Limiters | Units in 10 s | Times the rate |
|---|---|---|
| One | 10,944,512 | 1.09 |
| A new one every second | 20,643,840 | 2.06 |
| A new one every 100 ms | 105,840,640 | 10.58 |

The calls that waited on the old Limiter also kept its queues, so the
priorities of ADR-0063 did not order them against the calls of the new one.
`golang.org/x/time/rate` changes a running limiter with `SetLimit` and
`SetBurst`, and RocksDB with `GenericRateLimiter::SetBytesPerSecond`.

## Decision

We will change the Rate and the Burst of a Limiter in place with
`Limiter.SetRate(rate, burst int64) error`, because the bucket then keeps its
units across a change and one set of queues orders every waiting call.

- The bucket keeps the units that it has at the time of the call, up to the
  new Burst. It gains units at the new Rate from then on, so a larger Burst
  fills at the new Rate.
- A Clock that moves back can leave the bucket below empty. `SetRate` keeps
  the time at which such a bucket is empty, and the bucket gains units at the
  new Rate after that time.
- The waiting calls keep their places in their queues. The head waits again
  for the units that it lacks at the new Rate.
- A waiting call of more units than the new Burst returns `ErrUnits`,
  because the bucket never contains them.
- A Rate of zero means no limit, so every waiting call returns nil. A change
  from a Rate of zero starts with a full bucket, as `NewLimiter` does.
- `SetRate` returns `ErrConfig` for a configuration that `NewLimiter`
  refuses, and for a positive Rate of the zero Limiter, which has no Clock. A
  refused call does not change the Limiter.
- The mutex of the Limiter guards the Rate and the Burst, so `AllowN` and
  `WaitNPriority` read both under it. `Burst` loads a copy of the Burst from
  an atomic, and takes no lock.

This decision supersedes the rule for a change of rate that RFC-0050 states.

## Alternatives Considered

### A new Limiter for each change

A caller would build a new Limiter at each change, as RFC-0050 states.
Rejected. Each new Limiter starts with a full bucket, and its queues do not
order the calls that still wait on the old one.

### SetLimit and SetBurst, as in golang.org/x/time/rate

`SetLimit` and `SetBurst` would change the Rate and the Burst one at a time.
`golang.org/x/time/rate` keeps the tokens of its bucket in both. Its waiting
calls keep the delays of the old limit, and its documentation states that the
new limit "may be violated or underutilized by those which reserved (using
Reserve or Wait) but did not yet act". Rejected. A policy that changes both
would pass through a state of the new Rate and the old Burst.

### The time due kept as it is

`SetRate` would keep `due`, the time at which the bucket would be empty once
it covered every unit taken so far. Rejected. The units that the bucket lacks
would grow with a higher Rate and shrink with a lower one. A Limiter whose
policy lowered its Rate would then admit a burst at the change.

### A wait for a Burst that rises again

A waiting call of more units than the new Burst would keep its place until a
later `SetRate` raised the Burst. Rejected. Without such a `SetRate` the call
would wait until its context ended, and its caller could not tell that from a
slow grant. `ErrUnits` makes the caller split its work by the new Burst at
once.

## Consequences

**Positive:**

- A policy that sets the same Rate and Burst at every interval admits the
  units of one Limiter. A spec runs the probe of issue 87 with a `SetRate`
  every 100 ms, and admits as many units as one Limiter without `SetRate`.
- The priorities order every waiting call across a change, because one
  Limiter keeps every queue.

**Negative:**

- A caller that splits its work by `Burst` receives `ErrUnits` when a
  `SetRate` lowers the Burst between its read of `Burst` and its call. It
  reads `Burst` again and splits the rest.
- `AllowN` and `WaitNPriority` at a Rate of zero lock the mutex. Alternating
  rounds with Go 1.27.2 on an AMD Ryzen 9 9950X3D measured 38.5 ns for a
  `WaitN` that does not wait, against 37.7 ns before. `AllowN` took 26.2 ns
  and `Burst` 2.0 ns, as before. `SetRate` takes 33.8 ns and does not
  allocate.

**Neutral:**

- The bucket keeps the exact arithmetic of ADR-0055. `SetRate` keeps the
  units that the bucket lacks in nanounits without a rounding. It rounds only
  the time of a bucket below empty, up to a whole nanosecond.

## References

- Issue 87, <https://github.com/thesm-os/core/issues/87>.
- RFC-0050, the rate limiter.
- ADR-0055, the Limiter counts units exactly.
- ADR-0063, waiting calls of a Limiter take units by priority.
- golang/time at fb013b3d30, `rate/rate.go`, `SetLimitAt` and `SetBurstAt`,
  <https://github.com/golang/time/blob/fb013b3d305a26f5ef5350a6eaffc7c87a200383/rate/rate.go#L303-L332>.
- facebook/rocksdb at 54732b0c8f, `util/rate_limiter.cc`,
  `GenericRateLimiter::SetBytesPerSecond`,
  <https://github.com/facebook/rocksdb/blob/54732b0c8f6f8af34f973d7fae0a234022fcf1fa/util/rate_limiter.cc#L156-L168>.
