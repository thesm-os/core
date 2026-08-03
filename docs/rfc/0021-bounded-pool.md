---
rfc: 0021
title: Bounded Pool
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-08-03
updated: 2026-08-03
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0021: Bounded Pool

## Summary

`pool.Bounded[T]` — a fixed-capacity pool that never evicts, for
pooling a scarce resource rather than optimising allocation. `Pool` and
`ResetPool` are backed by `sync.Pool`, which drops cached values under
GC pressure; that is correct for allocation reuse and wrong when the
pool's capacity *is* the contract. Also corrects `pool`'s
zero-allocation claim, which does not hold for a non-pointer `T`.

## Motivation

`sync.Pool` evicts under GC pressure, by design. For allocation reuse
that is exactly right: a dropped buffer costs one allocation to
recreate.

For a scarce resource it is wrong in a way that cannot be worked
around. When the pooled thing is a connection, a licence, or a buffer
held against a hard memory budget, the pool's capacity is the
constraint being enforced — and a pool that silently drops values under
GC pressure enforces nothing. Worse, it recreates them on demand, so
the limit is exceeded precisely when memory is already tight.

These are different data structures with different contracts, and a
foundation that ships one should ship both rather than leaving callers
to discover that the one it ships has the wrong eviction behaviour for
half of what pools are used for.

The second item here is a documentation defect. `pool`'s
zero-allocation claim is stated without qualification, and `Put` boxes
`T` into `any` as `sync.Pool` requires. Only a pointer `T` avoids the
allocation, so a caller following the documentation with a value type
regresses while believing the opposite.

## Detailed design

```go
// In package pool.

// Bounded is a fixed-capacity pool that never evicts.
//
// Unlike [Pool], which is backed by sync.Pool and may drop cached
// values under GC pressure, Bounded holds exactly what it is given up
// to cap. Use it when the pooled thing is a scarce resource rather
// than an allocation optimisation.
//
// # Concurrency
//
// Safe for concurrent use.
type Bounded[T any] struct{ ... }

// NewBounded creates a pool of at most cap values. cap must be > 0.
//
// newFn is called lazily, at most cap times, so a pool of expensive
// resources does not construct all of them at startup.
func NewBounded[T any](cap int, newFn func() T) (*Bounded[T], error)

// Get returns a value, blocking until one is available or ctx ends.
func (p *Bounded[T]) Get(ctx context.Context) (T, error)

// Put returns v to the pool. It never blocks and never discards: a
// Put beyond capacity cannot occur, because capacity is only ever
// released by a Get.
func (p *Bounded[T]) Put(v T)

// Len reports the number of values currently available to Get. It is
// a point-in-time observation, useful for a gauge and not for a
// decision.
func (p *Bounded[T]) Len() int

// Cap reports the pool's capacity: the ceiling passed to NewBounded.
func (p *Bounded[T]) Cap() int

// Created reports how many values newFn has produced so far, which
// rises to Cap under load and never falls. Cap minus Created is the
// headroom the pool has never needed; Cap equal to Created means the
// ceiling has been reached at least once and callers have waited.
func (p *Bounded[T]) Created() int
```

### `Get` blocks; `Put` does not

The asymmetry is the contract. `Get` blocking is how the bound is
enforced — a caller wanting the `cap + 1`th value waits rather than
receiving one that should not exist.

`Put` cannot block, because the only values that can be put back are
ones a `Get` handed out, so capacity for them is guaranteed. A `Put`
that could block would mean a caller returning a resource could be
stalled by the pool, which turns a cleanup path into a failure path.

Putting a value the pool never issued is a programmer error. It cannot
be detected for an arbitrary `T` without an identity map, so it is
documented rather than checked, and the consequence is a pool that
reports a capacity higher than it was constructed with.

### `Get` returns an error

`Get` blocks, so it can be cancelled, so it returns `ctx.Err()`. That
is the difference from `Pool.Get`, and it is why `Bounded` is a
separate type rather than an option on the existing one: the signature
cannot be shared.

### Lazy construction

`newFn` is called on `Get` when no value is available and fewer than
`cap` have been created. A pool of connections therefore opens
connections as load demands rather than all at startup, and a process
that never reaches peak concurrency never pays for the peak.

### `Bounded` is not `Resettable`-aware

There is no `ResetBounded`. The reset discipline that `ResetPool`
enforces exists to prevent state leaking between unrelated uses of a
recycled buffer. A scarce resource is usually stateful on purpose — a
connection has a session, a licence has a holder — and resetting it on
return would defeat the reason it is pooled.

A caller who needs both wraps `T` in a type whose `Put` path resets.

### Correcting the allocation claim

`pool`'s package documentation and `Pool.Put`'s doc comment currently
claim zero allocation without qualification. They gain the
qualification:

> Zero allocation on the warm path holds for `ResetPool[T]` and
> `Pool[T]` where `T` is a pointer type. For a non-pointer `T`, `Put`
> boxes the value into `any` as `sync.Pool` requires, which allocates.
> Pool a pointer.

This is a documentation change with no code change, and it is included
here rather than as a standalone PR because it is the same subject and
a reader comparing the two pool types needs both statements side by
side.

### Allocation contract for `Bounded`

`Get` and `Put` on a warm pool allocate nothing: the implementation is
a buffered channel of `T` plus a counter, and neither boxes. That is
one concrete advantage over `Pool` for a value-typed `T`, and it falls
out of not being backed by `sync.Pool`.

## Alternatives considered

### A. Add a "do not evict" option to `Pool`

**Why not:** `Get` must block to enforce a bound, so it must take a
context and return an error. That is a different signature, and an
option that changes a method's signature is a different type wearing a
disguise.

### B. Use a semaphore at the call site and keep one pool

Acquire a slot, then `Get` from the existing pool.

**Why not:** it is two objects whose consistency the caller maintains
by hand, and the failure mode — a slot acquired and never released
after an early return — is a slow leak that looks like load.

### C. Have `Put` discard beyond capacity, like `sync.Pool`

**Why not:** for a scarce resource, discarding is a leak. A connection
dropped rather than returned stays open on the remote side until it
times out, and the pool's own count no longer reflects reality.

### D. Leave it out; a buffered channel is five lines

**Why not:** the five lines are the easy part. Lazy construction, the
context-aware acquire, the capacity accounting, and the interaction
between them are where hand-rolled versions differ from one another —
and every one of them is a pool that reports a capacity it does not
enforce.

## Drawbacks

- Two pool types with similar names and opposite eviction behaviour.
  Choosing wrongly is silent: `Pool` for a scarce resource over-issues
  under GC pressure, and `Bounded` for allocation reuse pins memory
  that should have been reclaimed.
- Putting back a value the pool did not issue inflates its capacity
  permanently and cannot be detected.
- `Len` is racy by nature and will be used for decisions anyway.
- `Bounded` blocks, so it introduces a deadlock shape `pool` did not
  previously have: a caller holding one value and waiting for a second
  from the same pool, at `cap` concurrent holders, waits forever.
- The documentation correction admits a claim in the module has been
  wrong, which callers may have relied on.

## Open questions

None. The three questions this RFC previously carried are resolved.

`Get` takes no separate maximum wait. `context.WithTimeout` expresses
it in one line, it is the shape every Go caller already knows, and a
second timeout parameter would create two deadlines whose interaction a
reader has to work out.

`Created` is added alongside `Cap`, so the pair answers whether the
ceiling has ever been reached — which is the question a caller sizing
the pool actually has, and one `Len` alone cannot answer.

The health-check hook moves to future work. It is genuinely the first
thing a caller of a connection pool asks for, and it brings a policy
question — what to do with a value that fails the check, and whether
the check runs on `Get`, on `Put`, or in the background — that this RFC
would have to answer badly to answer at all.

## Unresolved / future work

- A health-check hook, so a stale pooled resource is discarded and
  replaced rather than handed out. It needs a policy for where the
  check runs and what replaces a failed value, and those are required
  parameters rather than defaults.
- Idle timeout and eviction of values unused for a period, which is
  policy for the same reason.
- A pool whose capacity can be adjusted at runtime.
