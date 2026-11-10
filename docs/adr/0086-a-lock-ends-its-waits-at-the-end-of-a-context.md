---
adr: 0086
title: A Lock Ends Its Waits at the End of a Context
status: Accepted
date: 2026-11-10
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0086: A Lock Ends Its Waits at the End of a Context

## Status

Accepted

## Context

A storage engine runs critical sections that wait on a disk, such as the
admission of the writes of a volume and the changes of the segments of a
write-ahead log. Some of these sections must run one at a time. Under a
`sync.Mutex`, a goroutine that waits for such a section behind a call that
hangs waits until the kernel returns the call. The failure of the disk cannot
end that wait. The engine cancels a context of each disk at its failure, and
each waiter must then return the error of the disk.

The lock of such a section needs four properties:

- A wait ends at the end of the context of the caller.
- A wait ends at the end of the context of the owner of the lock, such as
  the context of a disk.
- A wait is a wait on a channel. A `testing/synctest` bubble counts such a
  wait as durable, and it does not count a wait for a `sync.Mutex` as
  durable. A simulation of the engine then becomes quiescent behind a call
  that hangs.
- A lock and an unlock do not allocate, because the admission of writes
  takes the lock once for each batch.

`resilience.Bulkhead` with a `Limit` of 1 ends a wait at one context. Its
`Acquire` allocates the release of each permit, and it refuses a caller with
`ErrFull` once its queue is full. `golang.org/x/sync/semaphore.Weighted`,
which core may import, ends a wait at one context too. It allocates a channel
and a list element for each caller that waits.

## Decision

We will add the mutual exclusion lock `resilience.Lock` because a select on a
channel of one slot and on two `Done` channels ends a wait at the end of
either context without an allocation. A `testing/synctest` bubble counts that
select as durable.

```go
func NewLock(owner context.Context) *Lock
func (l *Lock) Lock(ctx context.Context) error
func (l *Lock) TryLock() bool
func (l *Lock) Unlock()
```

- `Lock` checks ctx and then the owner before it takes a free lock. It
  refuses a call whose context ended before the call. `Acquire` of x/sync
  refuses such a call too. Then `Lock` sends into the slot and waits while
  the slot is full.
- `Lock` returns the `context.Cause` of the context that ended. The waiters
  of a failed disk then return the error of the disk.
- `TryLock` does not wait, so it does not check the contexts.
- `Unlock` receives from the slot without a wait. An `Unlock` of a free Lock
  does nothing.
- The zero Lock has no slot and is never free.

## Alternatives Considered

### A Bulkhead with a Limit of 1

`BulkheadConfig` would gain the context of an owner, and the engine would
take a Bulkhead of one permit as its lock.

Rejected. `Acquire` returns the release of each permit as a function, which
costs two allocations for each grant. Its queue has a fixed size and refuses
the callers beyond it with `ErrFull`, while a lock queues every caller.

### A semaphore of x/sync with a weight of 1

The engine would call `Acquire` of `semaphore.Weighted` with a context that
ends at the end of the caller or of the owner.

Rejected. A wait of `Acquire` ends at the end of one context, so each call
needs a context that merges the two, which allocates. Each caller that waits
also allocates its channel and its list element.

### A sync.Mutex with a sync.Cond

A waiter would wait on a condition variable while the section runs.

Rejected. `sync.Cond.Wait` returns only after a `Signal` or a `Broadcast`.
Each wait then needs a `context.AfterFunc` that broadcasts at the end of its
context, and `AfterFunc` allocates. A broadcast wakes every waiter of the
lock for the end of one context.

### A free lock for a caller whose context ended

`Lock` would take a free lock without a check of the contexts, and check them
only while it waits.

Rejected. After the failure of a disk, a free lock would admit new callers
into a section of the failed disk. `Acquire` of x/sync also refuses a context
that ended before the call.

## Consequences

**Positive:**

- The waiters behind a call that hangs return the cause of the owner at the
  end of the owner.
- A simulation in a `testing/synctest` bubble becomes quiescent while
  callers wait for a Lock that the bubble created.
- `Lock`, `TryLock` and `Unlock` do not allocate, also for callers that
  contend.

**Negative:**

- A lock and an unlock of a Lock take longer than those of a `sync.Mutex`:

  | Lock and unlock | `sync.Mutex` | `resilience.Lock` |
  |---|---|---|
  | Free, on one core | 7.6 to 8.0 ns | 29.6 to 30.2 ns |
  | 4 goroutines that contend, on 4 cores | 19.6 to 23.3 ns | 161.0 to 168.6 ns |

  Each range covers 4 runs of 200 ms on an AMD Ryzen 9 9950X3D with Go
  1.27.3.

  A waiter of a Lock parks until `Unlock` passes it the slot, while a
  `sync.Mutex` spins before it parks.
- A Lock does not report a second `Unlock`. An `Unlock` of a free Lock does
  nothing, while an `Unlock` of a free `sync.Mutex` ends the process with a
  fatal error.
- A Lock is not reentrant. A goroutine that calls `Lock` while it has the
  lock waits until a context ends.

**Neutral:**

- A Lock is not associated with a goroutine, as a `sync.Mutex` is not.
- The waiters take the lock in the order of the send queue of the channel.
  The Go runtime keeps that queue in the order of the sends, and the Go
  specification does not state an order.

## References

- Issue 140, <https://github.com/thesm-os/core/issues/140>.
- RFC-0023, resilience primitives: the Bulkhead.
- ADR-0015, dependency-free x modules in production: x/sync.
- golang.org/x/sync at v0.25.0, `semaphore/semaphore.go`: `Acquire`, which
  refuses a context that ended before the call and allocates a channel for
  each waiter,
  <https://github.com/golang/sync/blob/v0.25.0/semaphore/semaphore.go#L41-L113>.
- Go, the package documentation of `testing/synctest`: the operations that
  block a goroutine durably, <https://pkg.go.dev/testing/synctest#hdr-Blocking>.
- Go 1.27.3, `src/runtime/select.go:169-203`: a select whose channels belong
  to a bubble counts as durable, and a nil channel is not a case of the poll.
