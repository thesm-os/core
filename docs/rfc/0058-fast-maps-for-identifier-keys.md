---
rfc: 0058
title: Fast Maps for Identifier Keys
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-12
updated: 2026-10-12
discussion: none
supersedes: none
superseded-by: none
produces-adr: tbd
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0058: Fast Maps for Identifier Keys

## Summary

We measured hash maps for one goroutine, ordered maps and concurrent maps
against Go's map, core's `btree` and the Go libraries. The keys were
integers and the fixed-size identifiers of RFC-0057, and the maps were
bounded or growing. Go's map remains the hash map for keys of any
comparable type. No library is consistently faster, and Go's map does not
allocate for lookups or for a workload of constant size.

For the fixed-size identifiers, we propose a package `idmap` with two
maps that do not allocate once they have their size:

- `idmap.Map`, for one goroutine, hashes the words of an identifier with a
  seeded multiplication. It looks up 1M keys 35% faster than Go's map, and
  fills a presized map 62% faster.
- `idmap.Concurrent`, for many goroutines, keeps integer values inline in
  buckets under a sequence lock. Its readers do not lock. It is faster than
  xsync, the fastest Go library, for reads and for stores to present keys,
  and it does not allocate on a store. xsync is faster for a delete and an
  insert, and it allocates on each.

We also propose word comparisons in `ID128.Compare`, `ID160.Compare` and
`ID256.Compare`. They make ascending inserts into a `btree.MapFunc` keyed by
`ID128` 24% faster.

## Motivation

### Method

All figures come from one probe, outside this module, that runs core's
`btree` at the working tree, Go's map, and these libraries:
cockroachdb/swiss at 3334444, dolthub/swiss v0.2.1, tidwall/btree v1.8.1,
google/btree v1.1.3, plar/go-adaptive-radix-tree v2.0.4,
puzpuzpuz/xsync v4.5.0, alphadose/haxmap v1.4.1 and
orcaman/concurrent-map v2.0.1. The probe also contains three prototypes of
this design.

- Go 1.27.2, pinned to CPUs 8 to 11, four cores of the complex with 32 MB
  of L3 on an AMD Ryzen 9 9950X3D, as RFC-0043 measured.
- Each benchmark and each implementation run in a process of their own.
  The order of the implementations rotates over three rounds, and each
  range runs from the lowest to the highest of the rounds.
- The keys are random `int64` values and random 16-byte identifiers. A
  map of 1M keys has 1,000,000 of them.
- Every call goes through a function value, so no implementation gains
  from inlining.
- The identifier keys are 16 bytes long. The probe did not measure
  20-byte or 32-byte keys.
- A concurrent benchmark runs four goroutines, and its figure is the wall
  time per operation of all four.

### Hash maps on one goroutine

For `int64` keys, Go's map is the fastest at lookups in a map that fits the
cache, and at misses:

| Operation | Go map | cockroachdb/swiss | dolthub/swiss | Prototype, seeded hash of any key |
|---|---|---|---|---|
| Get, 1M keys | 27.4-32.8 ns | 22.5-36.3 ns | 22.3-45.8 ns | 38.9-47.5 ns |
| Get, 10k keys | 5.47-5.67 ns | 6.20-6.51 ns | 9.38-9.68 ns | 10.9-11.3 ns |
| Miss, 1M keys | 14.9-21.5 ns | 23.3-26.7 ns | 29.4-33.0 ns | 18.5-18.7 ns |
| Fill of a presized map, per key | 32.3-36.3 ns | 28.2-32.1 ns | 24.6-26.6 ns | 24.6-25.6 ns |
| Delete, then insert | 86.8-116 ns | 70.0-83.0 ns | 114-162 ns | 93.8-114 ns |

For identifier keys, the cost of the hash separates the maps. Go's map
hashes every byte of the key with its general hash. A hash of the identifier's words
with one seeded 64×64→128-bit multiplication, as wyhash mixes 16 bytes, is
faster:

| Operation | Go map | cockroachdb/swiss | swiss, unseeded hash | Prototype, unseeded hash | Prototype, seeded hash |
|---|---|---|---|---|---|
| Get, 1M keys | 45.5-47.6 ns | 35.1-39.6 ns | 34.9-38.5 ns | 26.9-28.5 ns | 29.8-30.6 ns |
| Get, 10k keys | 8.85-8.93 ns | 8.23-8.41 ns | 6.00-6.33 ns | 7.95-8.02 ns | 7.75-7.83 ns |
| Miss, 1M keys | 25.0-30.9 ns | 19.1-28.5 ns | 20.5-23.4 ns | 16.7-17.2 ns | 17.3-17.6 ns |
| Fill of a presized map, per key | 52.0-54.2 ns | 36.9-39.4 ns | 34.9-37.5 ns | 19.3-20.1 ns | 19.4-20.6 ns |
| Delete, then insert | 129-132 ns | 108-115 ns | 84.3-88.5 ns | 83.9-94.7 ns | 97.4-103 ns |

The unseeded hash takes the last eight bytes of the identifier, which are
random in a ULID or a UUIDv4, and mixes in a seed with one multiplication.
The prototype is an open-addressing table with linear probing that is at
most 62.5% full.

A presized fill of 1M identifiers allocates 4,103 times in Go's map, 519 times in cockroachdb/swiss and 9
times in the prototype. A growing variant of the prototype doubles its
table when it is full. In a later run of three rounds, it filled an empty
map with 1M identifiers in 33.9-35.1 ns per key and 60 allocations. Go's
map took 67.2-67.4 ns and 8,200 allocations in the same run, and swiss with
the unseeded hash took 47.8-49.3 ns and 536 allocations.

No map in either table allocates for a lookup, or for a delete and an
insert at constant size.

### Ordered maps

For `int64` keys, core's `btree.Map` is the fastest ordered map at lookups
and at inserts:

| Operation, 1M keys | core `btree.Map` | tidwall `Map` | google `BTreeG` |
|---|---|---|---|
| Get | 141-186 ns | 164-242 ns | 248-276 ns |
| Fill in random order, per key | 119-176 ns | 154-203 ns | 173-239 ns |
| Fill in ascending order, per key | 23.9-24.7 ns | 32.1-34.6 ns | 49.6-55.9 ns |
| Allocations of the random fill | 23,750 | 69,349 | 69,181 |

`btree.Map` takes keys of `cmp.Ordered` types, so a map keyed by `ID128`
is a `btree.MapFunc` with a comparison function:

| Operation, 1M identifiers | `MapFunc`, `bytes.Compare` | tidwall `BTreeG` | google `BTreeG` | adaptive radix tree |
|---|---|---|---|---|
| Get | 339-406 ns | 460-521 ns | 482-546 ns | 247-330 ns |
| Fill in random order, per key | 263-326 ns | 452-476 ns | 418-439 ns | 230-260 ns |
| Fill in ascending order, per key | 51.6-54.6 ns | 272-274 ns | 265-275 ns | 93.3-95.1 ns |
| Allocations of the random fill | 23,894 | 69,056 | 68,899 | 5,362,249 |
| Allocations of one full scan, beyond the probe's closure | 0 | 0 | 0 | 283,155 |

The adaptive radix tree looks keys up fastest, but it allocates more than
five objects for each key that it inserts, and its scan allocates.

In a run of three rounds, an ascending fill of a `MapFunc` took
50.9-51.1 ns per key with `bytes.Compare`. It took 38.4-39.4 ns with a
comparison of the two big-endian words of the key, and 30.7-32.3 ns in a
prototype of `btree` with that comparison inline in its search. Lookups in 1M identifiers took 223-307 ns in every variant, because
their cache misses vary more between rounds than the comparisons differ.

### Concurrent maps

The prototype keeps keys and values inline in buckets under a sequence
lock, and its readers retry a bucket that changed during their read:

| Workload, 1M identifiers | Prototype | xsync | sharded `RWMutex` maps | `sync.Map` | one `RWMutex` |
|---|---|---|---|---|---|
| 100% reads | 15.8-16.6 ns | 18.6-19.9 ns | 20.9-21.6 ns | 47.1-51.4 ns | 22.7-24.5 ns |
| 90% reads | 15.8-18.3 ns | 22.0-26.1 ns | 64.9-66.9 ns | 51.5-58.3 ns | 91.2-107 ns |
| 50% reads | 19.1-23.1 ns | 26.3-30.5 ns | 78.1-81.8 ns | 68.2-81.3 ns | 101-124 ns |
| 90% reads of 1,000 hot keys | 3.33-4.15 ns | 3.72-5.20 ns | 17.8-19.0 ns | 6.80-9.31 ns | 18.7-19.2 ns |
| Delete, then insert | 65.4-67.4 ns | 28.3-37.5 ns | 57.7-86.1 ns | 58.5-63.9 ns | 144-155 ns |
| Allocations of a delete and insert | 0 | 1 | 0 | 2 | 0 |

The writes of the read workloads store a new value under a present key.
xsync allocates 2 bytes per operation in the workload of 90% reads, and 12
in the workload of 50% reads. The prototype and the lock-based maps do not
allocate. For `int64` keys, xsync is the fastest library in every workload,
and `sync.Map` allocates on its stores: twice for a delete and an insert.

### Prior art

- Go's map is a swiss table. A group contains 8 slots and a control word,
  and a map selects one of its tables by extendible hashing over the
  upper bits of the hash (`internal/runtime/maps/map.go:24` and
  `:100-105` in Go 1.27.2). A table contains at most 1,024 slots
  (`table.go:20`). The runtime hashes with AES where the processor has it
  (`runtime_fast64.go:68`). Its fallback hash for 64-bit platforms is
  "inspired by wyhash" (`runtime_hash64.go:5-6`).
- cockroachdb/swiss is a swiss table with options for a hash function, an
  allocator and a bucket capacity (`options.go:34`, `:49` and `:87` at
  3334444).
- xsync's `Map` is "a modified version of Cache-Line Hash Table (CLHT)",
  with immutable entries in place of atomic snapshots
  (`map.go:92-103` at v4.5.0). A bucket points to its entries
  (`map.go:154-160`). Each store allocates an entry.
- CLHT, by David, Guerraoui and Trigonakis, keeps each bucket in one cache
  line and updates it in place. Most of its operations transfer at most
  one cache line.
- F14, Meta's open-addressing table, counts in each chunk the keys that
  probed past it. An erase decrements the counts on its probe path. The
  counts replace tombstones, and a long-lived table of inserts and erases
  does not need a rehash.
- Boehm analyses implementations of sequence locks under the C++11 memory
  model. Their readers race with writers by design. In Go, a sequence lock
  without data races reads every protected word with an atomic load.

## Detailed design

### Components

| Component | Responsibility |
|---|---|
| `idmap.Key` | The type set `id.ID128 \| id.ID160 \| id.ID256` |
| `idmap.Map[K, V]` | A hash map for one goroutine. Open addressing, linear probing, deletion by backward shift, growth by doubling |
| `idmap.Concurrent[K, V]` | A concurrent hash map of a fixed capacity from identifiers to integers. Readers without locks, writers under a lock per home bucket |
| `idmap.ErrFull` | The error of a store of a new key into a full `Concurrent` |
| `ID128.Compare`, `ID160.Compare`, `ID256.Compare` | The order of `bytes.Compare`, computed from big-endian words |

`idmap` imports `go.thesmos.sh/core/id`, `go.thesmos.sh/core/errs` and the
standard library: `encoding/binary`, `hash/maphash`, `math/bits`, `runtime`
and `sync/atomic`.

### API

```go
// Key is the type set of the fixed-size identifiers.
type Key interface {
    id.ID128 | id.ID160 | id.ID256
}

// Map is a hash map from identifiers to values, for one goroutine.
type Map[K Key, V any] struct{ /* unexported */ }

// New returns an empty map with room for capacity keys.
func New[K Key, V any](capacity int) *Map[K, V]

func (m *Map[K, V]) Get(key K) (V, bool)
func (m *Map[K, V]) Set(key K, value V) (V, bool)
func (m *Map[K, V]) Delete(key K) (V, bool)
func (m *Map[K, V]) Len() int
func (m *Map[K, V]) Clear()
func (m *Map[K, V]) All() iter.Seq2[K, V]

// Word is the type set of the values of a Concurrent map. A value is one
// word, so a reader loads it atomically.
type Word interface {
    ~int64 | ~uint64 | ~int32 | ~uint32
}

// Concurrent is a hash map of a fixed capacity from identifiers to
// integers, safe for concurrent use.
type Concurrent[K Key, V Word] struct{ /* unexported */ }

// NewConcurrent returns an empty map for up to capacity keys.
func NewConcurrent[K Key, V Word](capacity int) *Concurrent[K, V]

func (c *Concurrent[K, V]) Load(key K) (V, bool)
func (c *Concurrent[K, V]) Store(key K, value V) error
func (c *Concurrent[K, V]) Delete(key K) bool
func (c *Concurrent[K, V]) Len() int

// ErrFull is returned by Concurrent.Store for a new key when the map
// contains capacity keys. It classifies as errs.Transient.
var ErrFull = errs.WithClass(errors.New("idmap: map is full"), errs.Transient)
```

`Set` and `Delete` return the previous value and whether the key was
present, as `btree.Map` does. A caller stores a pointer or a struct in a
`Map`. A caller of `Concurrent` stores an integer, such as the index of a
record in a slice of its own.

### Hashing

Each map takes a random seed from `hash/maphash.MakeSeed` when it is
created, as Go's map takes a seed from the runtime. The hash combines the
first word of the key with the seed and a constant, and the second word
with another constant. It multiplies the two into 128 bits, and combines
the halves by exclusive or. For an `ID160` and an `ID256`, the bytes past
the first 16 enter a second multiplication of the same form.

`New` and `NewConcurrent` select the hash function of the key's size once,
by a type assertion of the concrete function to `func(uint64, K) uint64`.
A hash that is generic over the type set must read the key by indexing,
because core imports no `unsafe`. In the probe, the generic form took
9.76-9.81 ns for a lookup in 10k identifiers, against 8.04-8.28 ns for the
concrete one.

### Map

- A table has a power of two of slots, at least capacity × 8/5, so it is at
  most 62.5% full. A slot has a tag byte, its key and its value, in three
  arrays: 0 marks an empty slot, and 0x80 with seven bits of the hash marks
  a full one.
- A lookup starts at the slot of the hash and walks forward. It compares
  the key only in a slot whose tag matches, and it stops at an empty slot.
- A delete moves each later key of the run back into the hole, unless the
  home of that key lies between the hole and the key. The table does not
  keep tombstones, so a workload of constant size never rehashes.
- An insert into a full table doubles the slots and moves every key.

### Concurrent

- A bucket has a sequence number, a home lock, an overflow count, a tag
  word and four slots. Every field is a word of `sync/atomic`.
- A reader loads the sequence number, waits while it is odd, reads the
  tags and the slots it needs, and loads the sequence number again. It
  retries the bucket when the number changed. It continues to the next
  bucket while the overflow count of the bucket is above zero.
- One writer at a time adds or removes the keys of a home, under the home
  lock of that bucket. A writer changes a slot under the sequence lock of
  the slot's bucket. It takes that lock by changing the number from even to
  odd.
- An insert counts its probe past each full bucket before it writes its
  key, so a reader finds every key that a reader before it found. A delete
  removes its key before it decrements the counts on its path.
- The table never grows. A store of a new key into a map of capacity keys
  returns `ErrFull`, and the map is at most half full.

### Identifier comparisons

`ID128.Compare` compares the two big-endian words of each identifier, and
`ID160.Compare` and `ID256.Compare` compare their words in the same way.
Each returns what `bytes.Compare` of the bytes returns, as RFC-0057
specifies. `btree.NewMapFunc(id.ID128.Compare)` then orders identifiers
with the faster comparison.

### Invariants

- A key of a `Map` is reachable from its home slot without an empty slot
  on the way.
- The overflow count of a `Concurrent` bucket equals the number of stored
  keys whose home precedes the bucket in the probe order and that are
  stored after it.
- `Load`, `Store` and `Delete` of `Concurrent` are linearizable.
- `Compare` of each fixed-size type returns `bytes.Compare` of the bytes.

### Bounds

| Quantity | Bound |
|---|---|
| Memory of a `Map[ID128, int64]` of 1M keys | 2^21 slots of 25 bytes, 52.4 MB, against 55.9 MB that a presized Go map allocated |
| Memory of a `Concurrent[ID128, int64]` for 1M keys | 2^19 buckets of 120 bytes, 62.9 MB |
| Work of a growth of `Map` | One allocation of each array, and one insert per key |
| Wait of a `Concurrent` reader | The write of one slot by a writer under the sequence lock of the bucket |

### Failure handling

| Condition | Function | Result |
|---|---|---|
| A new key in a full `Concurrent` | `Store` | `ErrFull`, the map unchanged |
| A capacity below 1 | `New`, `NewConcurrent` | A map with room for one key |
| A writer preempted under a sequence lock | `Load` of that bucket | The reader yields until the writer finishes |

### Allocation contract

| Call | Objects |
|---|---|
| `New` | The map and its three arrays |
| `Get`, `Delete`, `Len`, `Clear`, a range over `All` | 0 |
| `Set` | 0, plus the three arrays of a growth |
| `NewConcurrent` | The map and its bucket array |
| `Load`, `Store`, `Delete`, `Len` | 0 |

### Tests

| # | Guarantee |
|---|---|
| 1 | Random operations on a `Map` and on a `Concurrent`, over key spaces of three times the capacity, return what a Go map returns |
| 2 | Every overflow count of a `Concurrent` is zero once the map is empty again |
| 3 | Concurrent histories of `Load`, `Store` and `Delete` are linearizable against a sequential spec, under the race detector |
| 4 | A reader of a stable key whose value is a function of the key never sees another value while writers change other keys of its buckets |
| 5 | `Compare` of each fixed-size type equals `bytes.Compare` over generated pairs |
| 6 | `TestMapAllocs`, `TestConcurrentAllocs` and the benchmarks state the counts of the allocation contract |
| 7 | The suites cover every statement, and dokimi-mutate-go kills every mutant of `idmap` and of the comparisons |

## Alternatives considered

### A. A hash map for keys of any comparable type

Core would implement a swiss table over any comparable key.

**Why not:** Go's map is already a swiss table, and it is the fastest map
for `int64` keys in a map that fits the cache. The libraries fall within
its noise for 1M keys. Go's map does not allocate for lookups or for a
workload of constant size, and `make(map[K]V, n)` presizes it.

### B. The unseeded identifier hash

The maps would take the last eight bytes of an identifier as its hash.

**Why not:** a caller that stores identifiers from untrusted input lets an
attacker choose keys of one home, and every lookup then walks one long
run. The seeded hash takes 29.8-30.6 ns for a lookup in 1M identifiers,
against 26.9-28.5 ns without the seed.

### C. Import cockroachdb/swiss or xsync

**Why not:** under ADR-0015, production code imports only the standard
library, core and golang.org/x modules without requirements.

### D. Entries that a store replaces, as in xsync

`Concurrent` would publish each key and value in an entry of its own, and
a store would swap the entry.

**Why not:** xsync deletes and inserts faster, 28.3-37.5 ns against
65.4-67.4 ns, but every store allocates an entry. The request is a map
that does not allocate.

### E. Sharded maps under read-write locks

**Why not:** they do not allocate, but they are three to four times slower
than the prototype for every workload with writes.

### F. Growth for `Concurrent`

**Why not:** during a resize, a reader would follow two tables. A reader
that neither locks nor allocates can do that only with entries of their
own, and alternative D rules those out. A caller sizes the map for its
largest set of keys.

### G. A bounded mode for `Map`

`Map` would refuse a new key at its capacity.

**Why not:** a `Map` presized by `New` does not allocate up to its
capacity, and its one goroutine checks `Len` before `Set`.

### H. An adaptive radix tree for ordered identifiers

**Why not:** it allocates more than five objects for each key that it
inserts, and its scan allocates.

### I. Ordered map types with the identifier comparison inline

`btree` would declare one map type for each identifier size, with the
comparison in its search.

**Why not:** the inline comparison takes 30.7-32.3 ns for an ascending
insert against 38.4-39.4 ns for the word comparison through `MapFunc`. The
gain does not justify three exported types, and the word comparison serves
every caller of `Compare`.

## Drawbacks

- The module adds a package with two map types, two constraints and one
  sentinel.
- `Concurrent` stores integers only, and it has no iteration.
- A writer that the scheduler preempts under a sequence lock delays the
  readers of that bucket.
- A `Map` fills at most 62.5% of its slots, and its memory doubles at each
  growth, as the memory of Go's map does.

## Open questions

None.

## Unresolved / future work

None.

## References

- RFC-0043, B-tree ordered maps, for the method of the measurements.
- RFC-0057, fixed-size identifiers, for `ID128`, `ID160` and `ID256`.
- ADR-0015, dependency-free golang.org/x modules in production code.
- Go 1.27.2, `src/internal/runtime/maps/map.go:24` and `:100-105`,
  `table.go:20`, `runtime_fast64.go:68` and `runtime_hash64.go:5-6`.
- cockroachdb/swiss at 3334444, `options.go:34`, `:49` and `:87`.
- puzpuzpuz/xsync v4.5.0, `map.go:92-103` and `:154-160`.
- T. David, R. Guerraoui and V. Trigonakis, "Asynchronized Concurrency:
  The Secret to Scaling Concurrent Search Data Structures", ASPLOS 2015,
  <https://dl.acm.org/doi/10.1145/2786763.2694359>.
- Meta, "Open-sourcing F14 for memory-efficient hash tables", 2019,
  <https://engineering.fb.com/2019/04/25/developer-tools/f14/>.
- H.-J. Boehm, "Can seqlocks get along with programming language memory
  models?", MSPC 2012,
  <https://research.google/pubs/can-seqlocks-get-along-with-programming-language-memory-models/>.
- wyhash, <https://github.com/wangyi-fudan/wyhash>.
