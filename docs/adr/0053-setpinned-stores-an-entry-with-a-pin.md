---
adr: 0053
title: SetPinned Stores an Entry With a Pin
status: Accepted
date: 2026-10-14
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0053: SetPinned Stores an Entry With a Pin

## Status

Accepted

## Context

A caller that stores a value with `Cache.Set` and then pins it with
`Cache.Pin` can miss its own entry. The entry of a Set has no hit and no
pin when Set returns. The evictor of a Set on another goroutine can
examine the entry before the Pin. The evictor removes an entry without a
hit when it examines it.

A probe ran 8 goroutines on the code of `cache` at v0.19.0. It ran with
Go 1.27.2 at `GOMAXPROCS=4`. Each goroutine stored 100,000 keys of its
own and pinned each key right after its Set. In three runs the Pin
missed the new entry this often:

| Capacity | Pin calls of 800,000 that missed |
|---|---|
| 16 | 252 to 325 |
| 256 | 34 to 60 |
| 4,096 | 4 to 16 |

A miss costs more when `Evicted` closes a file or releases another
resource of the value. The evictor passes the new entry to `Evicted`.
`Evicted` closes the file before the caller reads it. The caller can
only repeat the open, the Set and the Pin. It repeats them for as long as
other goroutines evict its new entry.

## Decision

We will add `Cache.SetPinned`. It stores a value as `Set` does and
returns the new entry with one pin:

- The entry has its pin before the cache publishes it. The evictor does
  not evict the entry until `Unpin`, so the caller uses the value until
  then.
- A Set or a Delete of the key still removes the entry from the cache.
  `Evicted` then receives the entry at its last Unpin.
- SetPinned counts no hit on the entry, because a store is not a lookup.
  An entry without a lookup leaves the cache at the first examination
  after its Unpin.
- A value whose cost exceeds the capacity is not stored. SetPinned then
  removes the entry of the key and returns a Pinned of the value. The
  Unpin of that Pinned passes the value to `Evicted`.
- Set and SetPinned both panic for an expiry time other than zero in a
  cache without a Clock.

Set and Pin keep their contracts.

## Alternatives Considered

### A pin in Set

Set would return the new entry with a pin.

Rejected. Every caller of Set would have to call Unpin. A caller that
does not would keep its entry in the cache through every eviction.

### A loader in Pin

Pin would take a function that builds the value when the cache has no
entry of the key.

Rejected. The function would run under the lock of the cache or race
with a concurrent load of the same key. The caller already decides when
it builds a value. RFC-0049 leaves the coalescing of the loads of one
key to `batch`.

### A hit on the entry of Set

Set would count one hit on its new entry, so the evictor keeps the entry
at its first examination.

Rejected. A second examination before the Pin still evicts the entry, so
the race remains. Every new entry would also move to the main queue at
its first examination. An entry that only its first caller uses would
move there too.

### A hit on the entry of SetPinned

SetPinned would count a hit. A Pin after a Set counts one.

Rejected. The main queue would then admit every entry of SetPinned at
its first examination. An entry that only its first caller uses would
take a place in the main queue until the evictor examines it there.

## Consequences

**Positive:**

- A caller stores a value and pins it in one call. No eviction can remove
  the entry before the caller uses the value.
- `Evicted` receives the value of a SetPinned only after its Unpin, so a
  callback can release a resource of the value.
- The probe also ran the same workload through SetPinned. Its `Evicted`
  marked each value as released. In three runs at each of the three
  capacities, `Evicted` released none of the 800,000 values before their
  use.

**Negative:**

- The caller must call Unpin once after each SetPinned. A Pin has the
  same rule. A SetPinned without its Unpin keeps the entry in the cache
  through every eviction, and the sum of the costs can then exceed the
  capacity.
- The cache has two methods that store a value.

**Neutral:**

- An entry of SetPinned that the evictor examines in the small queue
  while it is pinned moves to the main queue without a hit. Every pinned
  entry moves this way.
- Set and SetPinned each allocate one entry.

## References

- Issue 46, <https://github.com/thesm-os/core/issues/46>.
- RFC-0049, the bounded cache, and its section Pins, expiry and
  callbacks.
- `cache/cache.go`: `Set`, `SetPinned` and `Pin`. `cache/policy.go`:
  `examine`. `cache/entry.go`: `evict`, `leave` and `unpin`.
- LevelDB, `include/leveldb/cache.h:48-58` at bb74ef7. `Cache::Insert`
  returns a handle of the new mapping, and the caller releases the handle
  when it no longer needs the mapping.
