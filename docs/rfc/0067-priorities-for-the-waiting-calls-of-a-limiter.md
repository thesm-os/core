---
rfc: 0067
title: Priorities for the Waiting Calls of a Limiter
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-16
updated: 2026-10-16
discussion: https://github.com/thesm-os/core/issues/55
supersedes: none
superseded-by: none
produces-adr: ADR-0063
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0067: Priorities for the Waiting Calls of a Limiter

## Summary

We propose that a call of `resilience.Limiter` waits at a priority. A
waiting call of `PriorityHigh` takes its units before the waiting calls of
`PriorityNormal`, apart from one grant in ten, which goes to the oldest
normal call while normal calls wait. The Limiter then grants units to its
waiting calls when the bucket covers them, in the order of the priorities,
instead of reserving the units of each call when it starts. The bucket
keeps the exact arithmetic of today, and `WaitN` waits at `PriorityNormal`.

## Motivation

`WaitN` reserves the units of a call when the call starts, so calls that
wait together take their units in the order in which they started. An
engine charges the flushes and the compactions of its volumes to the
limiter of their disk, in chunks of at most 1 MiB. The writers of a volume
wait for its flush once its memtables are full, so a flush is the most
urgent of these calls. A flush that starts after the chunks of N
compactions waits behind those N chunks. On a limiter of 1 MiB per second and
a burst of 1 MiB, the chunk of a flush that started fifth returned after 4
seconds, behind four chunks of compactions.

Each workaround of today has a cost:

- One limiter for each class splits the rate of the disk into fixed parts.
  A class that waits cannot take the rate of an idle class.
- A queue in front of `WaitN` orders the calls by class, and the call waits
  a second time in the limiter. The calls that the queue releases reserve
  in the order of their release, so a flush still waits behind the chunks
  that the queue released before it.

### Prior art

- RocksDB's `GenericRateLimiter` serves one queue of requests per priority,
  each in the order of the requests. It refills its bytes once per refill
  period, 100 ms by default, and grants the queued requests in an order of
  the priorities that a random number decides for each refill. `IO_USER`
  comes first. Of
  the other priorities, a higher one comes after the lower ones with a
  chance of one in `fairness`, 10 by default. Its documentation states that
  the fairness "grants low-pri requests permission by 1/fairness chance even
  though high-pri requests exist to avoid starvation".

## Detailed design

### API

```go
// Priority orders the calls of a Limiter that wait for units. A waiting
// call of PriorityHigh takes its units before the waiting calls of
// PriorityNormal, apart from one grant in ten.
type Priority uint8

const (
	// PriorityNormal is the priority of WaitN.
	PriorityNormal Priority = 0

	// PriorityHigh is the priority of a call that other work waits for,
	// such as a flush.
	PriorityHigh Priority = 1
)

// Valid reports whether p is PriorityNormal or PriorityHigh.
func (p Priority) Valid() bool

// WaitNPriority takes n units at the priority p, and waits until the
// Limiter grants them. It returns the errors of WaitN, and ErrPriority,
// classified errs.Invalid, for a p that is not valid.
func (l *Limiter) WaitNPriority(ctx context.Context, n int64, p Priority) error
```

`WaitN(ctx, n)` is `WaitNPriority(ctx, n, PriorityNormal)`.

### Grants

The Limiter keeps one queue of waiting calls for each priority, each in the
order in which its calls started, and the bucket as one time `due`, as
today.

- A call that finds both queues empty and the bucket covering its units
  takes them at once, as `WaitN` does today.
- Another call joins the queue of its priority. The call that the Limiter
  grants next is the head. The head waits on a timer of the Limiter's Clock
  until the bucket covers its units, and each other call waits on a channel
  of the call.
- When the timer of the head ends, the head takes its units: `due` moves
  forward by their exact cost. The Limiter then chooses the next head, and
  grants it at once when the bucket already covers it.
- The next head is the oldest high call. It is the oldest normal call when
  no high call waits, and after nine grants in a row to high calls while a
  normal call waited. A counter of those grants makes the share exact and
  the order repeatable. The counter starts again at a grant to a normal
  call and whenever no normal call waits.
- A high call that starts while a normal call is the head becomes the head,
  and the former head waits on its channel again. A normal call that has the
  tenth grant keeps the head, so a stream of high calls that start while it
  waits does not starve it.
- A call whose context ends leaves its queue. When it was the head, the
  Limiter chooses the next head. The call took no units, so it gives none
  back, and the calls behind it wait for no units that it reserved.
- `AllowN` takes units only while both queues are empty and the bucket
  covers them, so a caller that polls does not take units ahead of a call
  that waits.

With the Rate and the Burst of the probe, the chunk of the flush returns
after 1 second: after the first chunk of a compaction, which took the full
bucket at once.

### Time and exactness

Every grant moves `due` by the exact cost of its units, in whole
nanoseconds and a remainder in Rate-ths of a nanosecond. The bucket never
has more than Burst units, and the Limiter admits at most Rate units per
second, as today. Only the timer of the head rounds, up to the next whole
nanosecond.

### Concurrency

The mutex of the Limiter guards `due`, the queues and the counter. A call
locks it while it joins or leaves a queue, takes its units or chooses the
next head, and never while it waits.

### Errors

| Error | Class | Cause |
|---|---|---|
| `ErrPriority` | Invalid | A priority other than `PriorityNormal` and `PriorityHigh` |
| `ErrUnits` | Invalid | A negative n, or an n above Burst |
| the error of ctx, unwrapped | as the context | A context that ended before the call or while it waited |

### Allocation contract

A call that does not wait does not allocate. A call that waits allocates
its place in the queue and its channel, and the head allocates its timer
through `clock.Clock.NewTimer`.

### Tests

- A stateful machine runs calls of both priorities in a `testing/synctest`
  bubble with core's fake clock. An exact model of the queues, the counter
  and the bucket gives the time of every grant, and every call returns at
  its time.
- The probe of the issue returns the flush after 1 second.
- A steady stream of high calls lets one normal call through in every ten
  grants.
- A cancelled head lets the next call take its units at once when the
  bucket covers them.
- The cases of ADR-0055 for the exact arithmetic of the bucket keep passing.
  A cancelled call took no units, so the cases of the refund of a cancelled
  reservation have no subject. The machine bounds the units of every window
  by the Rate and Burst alone.

## Alternatives considered

### A. Strict priority without a share

**Why not:** a steady stream of high calls starves the normal calls. In an
LSM tree the debt of the starved compactions then stalls the writers too.

### B. Weighted fair queueing without strict priority

**Why not:** a high call still waits behind its share of the normal calls
that started before it.

### C. Refills once per period, as RocksDB does

**Why not:** a call waits for the next refill even when the bucket would
cover it sooner, and a refill period of 100 ms adds up to 100 ms to a call.
The bucket as one time grants each call at the moment the bucket covers
it.

### D. A random share, as RocksDB uses

**Why not:** a test of the order of the grants would depend on the random
source. A counter gives the same share and the same order in every run.

### E. Reservations at the start, moved back for a high call

A high call would reserve ahead of the normal calls that wait, and move
their reservations back by its cost.

**Why not:** each moved call would need a new timer, so every high call
would wake every waiting normal call. A queue wakes one call for each
grant.

## Drawbacks

- The Limiter gains two queues, a timer for each head, and a channel per
  waiting call.
- A call that waits allocates its place in the queue and its channel, beside
  the timer of today.
- `WaitN` grants its calls when the bucket covers them, not when they start.
  A normal call can take its units after a high call that started after it,
  and RFC-0050 states the order of the starts for every call.
- `PriorityNormal` and `PriorityHigh` cover a flush and a compaction. A
  third class needs a new constant and a rule for its share.

## Open questions

None.

## Unresolved / future work

None.

## References

- Issue 55, <https://github.com/thesm-os/core/issues/55>.
- facebook/rocksdb, `include/rocksdb/rate_limiter.h` (`NewGenericRateLimiter`)
  and `util/rate_limiter.cc` (`RefillBytesAndGrantRequestsLocked` and
  `GeneratePriorityIterationOrderLocked`), <https://github.com/facebook/rocksdb>.
- RFC-0050, the rate limiter.
- ADR-0055, the Limiter counts units exactly.
