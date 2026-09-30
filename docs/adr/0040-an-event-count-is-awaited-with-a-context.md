---
adr: 0040
title: An Event Count Is Awaited With a Context
status: Accepted
date: 2026-09-30
supersedes: none
superseded-by: none
---

# ADR-0040: An Event Count Is Awaited With a Context

## Status

Accepted

## Context

`epoch.Epoch` is core's type for an in-process monotonic counter, and
`epoch.Counter` issues the positions of a sequence. Core has no way to
wait until such a counter is at or above a position.

Each append to a storage engine that commits batches in groups takes a
position, and returns once the engine reports every position up to it
as durable. Other callers wait for a log's committed
size, a stream's head or a count of acknowledgements. The wait has to
honour the caller's context. It runs once per append, so it must not
allocate.

The standard library has no such wait:

- `sync.Cond.Wait` cannot return when a context ends.
- A goroutine or a `context.AfterFunc` that broadcasts when the context
  ends allocates on every wait. The Go documentation of
  `context.AfterFunc` warns that N waiters on one `sync.Cond` then cost
  O(N²) wakeups.
- A channel per waiter allocates the channel.

A shared channel that the signaller closes and replaces lets a waiter
select on it and on `ctx.Done()` without allocating. The signaller
allocates the replacement. The Go proposal to select on a condition
variable describes this pattern.

## Decision

We will add `epoch.EventCount`, a count that only rises and that
goroutines wait on with a context, because a caller that waits for a
position of `epoch.Counter` has no wait in the standard library that
honours a context without allocating:

- `Current` returns the count. `Advance(v)` raises the count to `v`, and
  a `v` at or below the count does nothing.
- `Wait(ctx, v)` returns nil once the count is at least `v`, the error
  that `Fail` recorded when the count fails first, and the context's
  error when the context ends first. A target that the count has met
  returns nil, even after `Fail`.
- `Fail(err)` ends every current and later `Wait` whose target the count
  has not met. Only the first `Fail` records its error. `Advance` after
  `Fail` still raises the count.
- Waiters share one wake-up channel. `Advance` closes it and installs a
  new one when a waiter took it. Every blocked waiter wakes, and each
  one whose target the count has not met waits again.
- `Wait` does not allocate. `Advance` allocates one channel when a
  waiter took the current one, and the first `Wait` on a zero
  `EventCount` allocates the first channel.
- The zero `EventCount` is ready to use, and an `EventCount` is safe for
  concurrent use.

## Alternatives Considered

### `sync.Cond` with `context.AfterFunc`

Each blocking `Wait` would register a function that broadcasts when its
context ends.

Rejected. On a prototype, a round of two blocking waits cost 5 to 6
allocations and about 480 ns. The shared channel cost 2 allocations and
about 350 ns. A broadcast for each ended context also wakes every other
waiter.

### One channel per target

etcd's `wait.WaitTime` keeps one channel per distinct target, and wakes
only the waiters whose target the value has met.

Rejected. The first `Wait` for each target allocates that target's
channel. A caller that waits once per append would allocate once per
batch in `Wait`.

### Pooled channels per waiter

Each waiter would take a channel from a pool, and `Advance` would wake
the waiters whose target the count has met, in order of target.

Rejected. A closed channel cannot be reopened, so `Advance` would send a
token to each waiter it wakes, under the lock. A waiter whose context
ends would have to leave the ordered set and drain a token it may have
received. The waiters of one group commit share a target, so the order
saves few wakeups.

### A package of its own with `uint64` values

The event count would be the type of a package of its own and count
`uint64` values.

Rejected. `epoch.Epoch` is core's type for an in-process monotonic
counter, and `EventCount` waits on the positions that `epoch.Counter`
issues. A second package would split one concept.

## Consequences

**Positive:**

- A caller waits for a durable or committed position without a
  goroutine or an allocation per wait. A `Wait` whose target the count
  has met takes one uncontended lock, about 10 ns.
- `Counter` issues positions, and `EventCount` reports progress through
  them. Reed and Kanodia describe this pair as a sequencer and an
  eventcount.

**Negative:**

- Every `Advance` wakes every blocked waiter. A caller with many
  waiters on distant targets wakes each of them on every `Advance`.
- `Advance` allocates a 112-byte channel on each call that finds a
  waiter.
- `Fail` is final. A caller that recovers from a failure builds a new
  `EventCount`.
- A context allocates its done channel on its first `Done`, so the first
  `Wait` with a new context allocates once, in the `context` package.

**Neutral:**

- A caller that counts sizes or acknowledgements converts them to
  `epoch.Epoch`.

## References

- RFC-0005, `Epoch`, an in-process monotonic counter.
- ADR-0010, one package name, one concept.
- D. P. Reed and R. K. Kanodia, "Synchronization with eventcounts and
  sequencers", Communications of the ACM 22(2), pages 115 to 123, 1979,
  <https://doi.org/10.1145/359060.359076>.
- Go issue 16620, "proposal: sync: mechanism to select on condition
  variables", <https://github.com/golang/go/issues/16620>.
- Go 1.27.1, the example of `context.AfterFunc` in
  `context/example_test.go`.
- etcd, `pkg/wait/wait_time.go`,
  <https://github.com/etcd-io/etcd/blob/main/pkg/wait/wait_time.go>.
