---
adr: 0088
title: A Bulkhead Changes Its Limit in Place
status: Accepted
date: 2026-11-12
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0088: A Bulkhead Changes Its Limit in Place

## Status

Accepted

## Context

A `resilience.Bulkhead` fixed its `Limit` at `NewBulkhead`. It kept one token
for each free permit in a channel, and a caller took a permit with a receive.
The capacity of that channel was the Limit. Go fixes the capacity of a channel
when it makes the channel.

A service that bounds the calls in flight of each tenant reads the limit of a
tenant from a feature flag. The limit then changes while calls are in flight.
A caller could only build a new Bulkhead, and a new Bulkhead starts with every
permit free. A test against core v0.44.0 took 32 permits of a Bulkhead with a
limit of 32 and then filled a new Bulkhead of the same limit. It counted 64
calls in flight.

## Decision

We will count the permits of a Bulkhead under a mutex and queue the waiting
callers in the order of their calls, because a count compares with any limit
and a channel of tokens has one limit for its lifetime.

```go
func (b *Bulkhead) SetLimit(limit int) error
```

- `SetLimit` changes the limit under the mutex. It does not free the permits
  that callers have taken.
- A lower limit refuses new calls until the calls in flight drop below it.
  `InFlight` can then return a count above the limit.
- A higher limit passes the permits of the difference to the queued callers
  at once. The first caller of the queue takes the first permit.
- A queued caller waits on a channel of its own. The Bulkhead sends the permit
  of the caller on that channel. A `testing/synctest` bubble counts the wait
  as durable.
- A free permit goes to the first queued caller, so `Acquire` does not take a
  permit ahead of a queued caller.
- A caller whose wait ends after the Bulkhead passed it a permit gives the
  permit back. It then returns its error without a permit.

## Alternatives Considered

### A new Bulkhead for each limit

The caller would replace the Bulkhead when the limit changes.

Rejected. The new Bulkhead admits up to its limit on top of the calls that
have permits of the old one.

### A debt of permits on the channel of tokens

A lower limit would take the free tokens from the channel and count the rest
as a debt. A release would not return its token while the debt is above zero.

Rejected. A limit above the capacity of the channel needs a new channel. The
callers that wait on the old channel would never receive the tokens of the
new one.

### The x/sync semaphore

`golang.org/x/sync/semaphore.Weighted` would count the permits.

Rejected. Its size is fixed at `NewWeighted`, and no method changes it.

## Consequences

**Positive:**

- A caller changes the limit while calls are in flight. The calls in flight do
  not exceed the larger of the old and the new limit.
- The queued callers take their permits in the order of their calls.
- `Acquire` of a free permit with its release takes 35.87 ns against 43.95 ns
  before on one core. On four cores it takes 32.56 ns against 41.46 ns.

**Negative:**

- A refusal of `Acquire` takes 9.63 ns against 7.27 ns before, because it
  locks the mutex.
- `InFlight` and `Queued` lock the mutex. `InFlight` takes 9.89 ns against
  3.60 ns before.
- A queued caller allocates its channel and its place in the queue.

Four alternating rounds of three runs per build measured these times on an
AMD Ryzen 9 9950X3D with Go 1.27.4.

**Neutral:**

- The release that `Acquire` returns still allocates twice. A refusal does not
  allocate.

## References

- Issue 148, <https://github.com/thesm-os/core/issues/148>.
- RFC-0023, resilience primitives: the Bulkhead.
- `Limiter.SetRate`, which changes the rate of a `Limiter` in place.
- golang.org/x/sync at v0.25.0, `semaphore/semaphore.go`: `NewWeighted` fixes
  the size. Each waiter of `Acquire` waits on a channel of its own,
  <https://github.com/golang/sync/blob/v0.25.0/semaphore/semaphore.go#L41-L113>.
