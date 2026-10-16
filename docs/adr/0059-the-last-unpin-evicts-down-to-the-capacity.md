---
adr: 0059
title: The Last Unpin Evicts Down to the Capacity
status: Accepted
date: 2026-10-16
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0059: The Last Unpin Evicts Down to the Capacity

## Status

Accepted

## Context

A `cache.Cache` keeps a pinned entry through every eviction. A Set or
SetPinned that finds every other entry pinned keeps its new entry past the
capacity. The contract of the cache states that the sum of the costs exceeds
the capacity only while pinned entries fill the cache.

Set and SetPinned were the only callers of the evictor, so the sum remained
past the capacity after the last Unpin of every entry. It came down at the
next Set. A cache of capacity 2 that stored three entries with SetPinned and
unpinned all three kept a cost of 3 and three entries.

A caller that releases a resource of each value in Evicted, such as an open
file, then keeps more resources than the capacity allows. Each value that a
pin kept past the capacity keeps its resource after the pin ends, and the
values of later SetPinned calls add to them until the next Set evicts.

## Decision

We will evict down to the capacity at the last Unpin of an entry while the
sum of the costs exceeds the capacity, because the sum then exceeds the
capacity only while pinned entries fill the cache.

- The last Unpin of an entry reads the sum of the costs, for an entry in
  the cache and for one that has left. While the sum exceeds the capacity,
  Unpin locks the cache and runs the evictor of Set without an entry to
  keep. It passes the entries that leave to Evicted after it releases the
  lock.
- An Unpin that leaves a pin on its entry evicts nothing, because its entry
  is still pinned.
- An Unpin while the sum is at most the capacity takes no lock.
- An Unpin that evicts more than eight entries allocates the list of their
  callbacks, as a Set does.

The API of the Cache does not change. This decision supersedes the eviction
by a later Set alone that RFC-0049 describes for an overflow of pins.

## Alternatives Considered

### The next Set evicts, as before

Rejected. Without a later Set, the overflow of the pins remains in the
cache, and Evicted does not release its resources.

### An eviction at every Unpin past the capacity

Every Unpin past the capacity would evict, also one that leaves a pin on
its entry.

Rejected. An Unpin that leaves a pin does not make an entry evictable. The overflow
that it finds is the entry that a Set kept while every other entry was
pinned, and the next last Unpin or Set evicts it.

## Consequences

**Positive:**

- The sum of the costs exceeds the capacity only while pinned entries fill
  the cache. A caller that releases a resource of each value in Evicted
  keeps the resources of at most the capacity once its pins end.

**Negative:**

- The last Unpin of an entry past the capacity takes the lock of the cache
  and runs the evictor on the goroutine of its caller. It waits for a
  concurrent Set, and Evicted runs on that goroutine.
- An Unpin can allocate, when it evicts more than eight entries.
- A test locks the mutex of the cache to check that an Unpin within the
  capacity takes no lock.

**Neutral:**

- Get, Pin and an Unpin within the capacity take the same steps as before.

## References

- Issue 54, <https://github.com/thesm-os/core/issues/54>.
- RFC-0049, the bounded cache, the section Pins, expiry and callbacks.
- ADR-0053, SetPinned stores an entry with a pin.
- `cache/pinned.go`: `Pinned.Unpin`. `cache/cache.go`: `shrink`.
  `cache/entry.go`: `unpin`. `cache/policy.go`: `evict`.
