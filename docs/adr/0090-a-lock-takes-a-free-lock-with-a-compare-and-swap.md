---
adr: 0090
title: A Lock Takes a Free Lock with a Compare-and-Swap
status: Accepted
date: 2026-11-12
supersedes: ADR-0086
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0090: A Lock Takes a Free Lock with a Compare-and-Swap

## Status

Accepted

Supersedes the channel of one slot of ADR-0086 and the order of its waiters.
The rest of ADR-0086 remains in force.

## Context

A `resilience.Lock` kept a token in a channel of one slot while a caller had
the lock. `TryLock` sent on the channel and `Unlock` received from it, so the
take and the free of a free lock locked the channel twice.

A storage engine takes a Lock for each record that it applies, and the lock
is free on nearly every call. On an AMD Ryzen 9 9950X3D with Go 1.27.4, a
take and a free of a free Lock took 29.6 to 30.2 ns on one core. The same
calls of a `sync.Mutex` took 7.6 to 8.0 ns.

## Decision

We will keep the state of a Lock in one atomic word and use a channel only
for the wakes of waiting callers, because a free lock then takes one
compare-and-swap to take and one to free, as a `sync.Mutex` does.

- The word is free, taken, or contended. A contended lock is taken while
  callers can wait. The zero word is the state of the zero Lock, which is
  never free.
- `TryLock` takes a free lock with a compare-and-swap from free to taken.
- `Lock` keeps its checks of ctx and of the owner first. It then tries the
  same compare-and-swap, and waits only when the lock is taken.
- A waiter swaps the state with contended before each wait, and a swap that
  finds the lock free takes it. This is the loop of the third mutex of
  "Futexes Are Tricky".
- The `Unlock` of a contended lock sends a wake on a channel with room for one
  value. A waiter that receives the wake tries the lock again. The channel
  keeps a wake until a waiter receives it, so a waiter that starts its wait
  after the send does not miss it.
- The Lock does not order its waiters. A caller that finds the lock free
  takes it ahead of a waiter.
- `Finally` waits through the same loop. `Unlock` keeps its one load of the
  mark of a section after the free.

## Alternatives Considered

### The channel of one slot

Each take and each free of a free lock would lock the channel.

Rejected. A take and a free of a free lock cost four times the calls of a
`sync.Mutex`.

### A queue of waiters in the order of their calls

Each waiter would wait on a channel of its own, and the `Unlock` of a
contended lock would pass the lock to the oldest waiter.

Rejected. A waiter would allocate its channel, and a Lock does not allocate
for callers that contend. A pool of such channels does not fit a
`testing/synctest` bubble, because the Go runtime ends the process when a
select of one bubble uses a channel of another bubble.

## Consequences

**Positive:**

- On an AMD Ryzen 9 9950X3D with Go 1.27.4, four alternating rounds of three
  runs per build measured these times per take and free:

  | Calls | Channel of one slot | Compare-and-swap |
  |---|---|---|
  | `TryLock` and `Unlock` of a free lock | 26.12 ns | 9.06 ns |
  | `Lock` and `Unlock` of a free lock | 31.80 ns | 14.74 ns |
  | `Lock` and `Unlock` of 4 goroutines that contend, on 4 cores | 173.75 ns | 27.62 ns |

- `Lock`, `TryLock` and `Unlock` do not allocate, also for callers that
  contend.
- A `testing/synctest` bubble counts a wait as durable, because a waiter
  waits on channels of the bubble.

**Negative:**

- The Lock does not order its waiters. A waiter can wait while newer callers
  take the lock, and a `sync.Mutex` hands its lock to the oldest waiter after
  a wait of 1 ms instead.
- A wake that a waiter receives after another caller took the lock costs the
  waiter one more pass of its loop.

**Neutral:**

- The Unlock of a free Lock and of the zero Lock does nothing, as before.

## References

- Issue 147, <https://github.com/thesm-os/core/issues/147>.
- ADR-0086, a lock ends its waits at the end of a context: `resilience.Lock`.
- Go 1.27.4, `src/internal/sync/mutex.go:63` and `:194`: `sync.Mutex` takes a
  free lock with one compare-and-swap and frees a lock without waiters with
  one addition. Line 55 sets the wait of 1 ms after which a mutex hands its
  lock to the oldest waiter.
- Go 1.27.4, `src/runtime/select.go:179-182`: a select of a goroutine of one
  bubble on a channel of another bubble ends the process.
- Ulrich Drepper, "Futexes Are Tricky", the third mutex,
  <https://www.akkadia.org/drepper/futex.pdf>.
