---
adr: 0057
title: Bump Reserves Values Without Locks
status: Accepted
date: 2026-10-16
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0057: Bump Reserves Values Without Locks

## Status

Accepted

## Context

The memtable of RFC-0062 keeps its node words in one fixed array and its
keys and values in another. Many goroutines insert at once, and each insert
reserves a range of each array without a lock. The arrays never grow or
move, so a reserved range keeps its position until the memtable is reset.

`arena` has no type for this. `Arena`, `List`, `Slabs` and `RefSlabs` are
for one goroutine, and `List` and `Slabs` grow by allocating. Without a
type in `arena`, the memtable would implement the same reservation twice,
once for each array.

Prior art keeps the reservation in an arena type:

- Pebble's `internal/arenaskl` reserves with `atomic.Uint64.Add`. It returns
  `ErrArenaFull` when the end passes the capacity.
- Badger's `skl` reserves with `atomic.Uint32.Add`. It panics when the end
  passes the capacity.
- RocksDB's `ConcurrentArena` takes a spinlock for its main arena and keeps
  small caches per core.

A probe outside this module measured reservations of 30 values with Go
1.27.2 on a Ryzen 9 9950X3D, three runs each:

| Reservation | 1 goroutine | 4 goroutines that only reserve |
|---|---|---|
| `atomic.Uint64.Add` | 3.83-3.85 ns | 6.45-7.55 ns |
| A compare-and-swap loop | 3.79-3.87 ns | 15.5-17.7 ns |

## Decision

We will add `arena.Bump`, a fixed array of values that goroutines reserve
in ranges with a compare-and-swap of its end, because the memtable needs two
such arrays and `arena` is the package of allocation primitives:

```go
// Bump is a fixed array of values of type T that goroutines reserve in
// ranges without locks.
type Bump[T any] struct{ /* unexported */ }

// NewBump returns an empty Bump with room for capacity values.
func NewBump[T any](capacity int) *Bump[T]

// Reserve reserves the next n values and returns the index of the first.
// It reports false when fewer than n values remain.
func (b *Bump[T]) Reserve(n int) (int, bool)

// Values returns every value of the array, reserved or not.
func (b *Bump[T]) Values() []T

// Len returns the number of reserved values.
func (b *Bump[T]) Len() int

// Reset zeroes the reserved values and ends every reservation.
func (b *Bump[T]) Reset()
```

- `Reserve` moves the end only with a compare-and-swap that keeps it at or
  below the capacity. A `Reserve` that does not fit leaves the end where it
  is, so a smaller `Reserve` after it can still succeed.
- A goroutine writes only the values that its `Reserve` returned. A
  goroutine that reads the values of another synchronizes with it first,
  such as through an atomic store and load.
- `Reset` zeroes the reserved values, so `Reserve` returns zero values.
  `Reset` is not safe concurrently with `Reserve`.
- `NewBump` counts a capacity below 0 as 0, and the zero `Bump` has a
  capacity of 0. `Reserve` reports false for an n below 0.
- `NewBump` allocates the `Bump` and its array. The other methods do not
  allocate.
- The memtable keeps its keys and values in a `Bump[byte]` and its node
  words in a `Bump[uint32]`.

## Alternatives Considered

### The arrays and their counters inside the memtable

Pebble and Badger keep their arena in the package of the skip list.

Rejected. The memtable would implement the reservation twice, and `arena`
is the package of allocation primitives.

### An atomic add that can pass the capacity

`Reserve` would add n to the end with one atomic add, and report false when
the end passes the capacity.

Rejected. A failed `Reserve` leaves the end past the capacity, so every
later `Reserve` fails, also one that would fit. The add is 2.3 times faster
only while goroutines do nothing but reserve. A memtable insert reserves
twice and spends most of its time in the search, so two writers rarely
reserve at the same moment.

### Shards per core

`Bump` would give each processor a cache of values, as RocksDB's
`ConcurrentArena` does.

Rejected. Shards split a fixed capacity, so one shard fills while another
has room. RocksDB backs its shards with a lock.

### A growing allocator under a mutex

The memtable would take `List` or `Slabs` under a mutex.

Rejected. The mutex serializes the inserts that the lock-free list runs in
parallel, and the growth allocates.

## Consequences

**Positive:**

- The reservation of the memtable has one contract with one spec in
  `arena`. The specs of the memtable test its order and its links.
- Another structure that goroutines fill in parallel reserves its memory
  without a counter of its own.

**Negative:**

- `arena` has a fifth type, with its documentation and its tests.
- `Values` returns the whole array, so a caller can write values that it
  did not reserve. Such a write breaks the contract, and no check detects
  it.
- A compare-and-swap retries when two goroutines reserve at the same
  moment. Under four goroutines that only reserve, a reservation took
  15.5-17.7 ns against 6.45-7.55 ns for an atomic add.
- `Reset` takes time in proportion to the reserved values, because it
  zeroes them.

**Neutral:**

- `Arena`, `List`, `Slabs` and `RefSlabs` do not change.

## References

- RFC-0062, sorted tables and memtables for LSM trees.
- ADR-0056, `RefSlabs` allocates regions by reference.
- cockroachdb/pebble v2.1.7, `internal/arenaskl/arena.go`, `Arena.alloc`.
- dgraph-io/badger v4.9.6, `skl/arena.go`, `Arena.putNode`.
- facebook/rocksdb, `memory/concurrent_arena.h`,
  <https://github.com/facebook/rocksdb/blob/main/memory/concurrent_arena.h>.
