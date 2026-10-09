---
rfc: 0049
title: Bounded Cache
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-10-02
updated: 2026-10-02
discussion: none
supersedes: none
superseded-by: none
produces-adr: tbd
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0049: Bounded Cache

## Summary

We propose `cache`, a bounded map whose entries leave by eviction,
expiry or removal. A `cache.Cache` bounds the sum of the costs of its
entries, where a cost is 1 per entry or a size that the caller computes.
It evicts with S3-FIFO, which we chose by simulation. At 10,000 entries,
S3-FIFO missed 1.4% to 18.0% fewer requests than LRU on eight of nine
synthetic traces, and 11.7% more under the fastest drift of the hot set.
SIEVE missed more requests than LRU on three of the nine.

A lookup that finds its entry takes no lock and does not allocate. A
pinned entry remains in the cache until its holder unpins it. An entry can
expire at a time that the caller's `clock.Clock` measures. A callback
receives every entry that leaves, once, outside the cache's lock.

## Motivation

### Core has bounded maps to keep and no cache

These designs in core keep values by key, and each must bound the
memory that the keys take:

- `crypto/tsp` keeps the verified chains of the certificates of
  time-stamp authorities, so a token of a known certificate skips the
  parse and the chain verification of `crypto/x509`.
- `telemetry.RateLimitHandler` remembers the time of the last record of
  each event, and a burst of distinct events must not grow it without a
  bound.
- `tlog` lists "a cache of verified tiles for readers" as future work,
  in the design of transparency log trees.

Go code usually builds such a map with `container/list` for the order of
use. It allocates a list element per entry, takes one lock per hit,
and has no pins, costs or expiry. Each user would also choose an
eviction policy without measuring it.

### A cache is a mechanism

Core provides mechanisms, from which a caller builds its own lifecycles.
A cache with pins, expiry and a callback is the mechanism under a table
of open handles, a cache of unwrapped keys whose callback zeroes a key,
or a cache of builders whose callback writes their partial output. Its
expiry reads `clock.Clock`, so core's fake clock controls it in tests.

## Detailed design

### API

```go
package cache

// Config configures a Cache. Clock and Capacity are required.
type Config[K comparable, V any] struct {
    // Clock reads the time against which entries expire.
    Clock clock.Clock

    // Cost returns the cost of a value. A nil Cost costs 1 per entry. A
    // negative cost counts as 0.
    Cost func(V) int64

    // Evicted receives the key and the value of every entry that leaves
    // the cache, once, outside the cache's lock. A nil Evicted is not
    // called.
    Evicted func(k K, v V)

    // Capacity bounds the sum of the costs. It must be positive.
    Capacity int64
}

// New returns a Cache of cfg, or ErrConfig for a Config without a Clock
// or with a Capacity that is not positive.
func New[K comparable, V any](cfg Config[K, V]) (*Cache[K, V], error)

func (c *Cache[K, V]) Get(k K) (V, bool)
func (c *Cache[K, V]) Pin(k K) (Pinned[K, V], bool)
func (c *Cache[K, V]) Set(k K, v V, expires time.Time) // the zero time never expires
func (c *Cache[K, V]) Delete(k K) bool
func (c *Cache[K, V]) Len() int
func (c *Cache[K, V]) Cost() int64

// Pinned is an entry with one pin, which the cache does not evict before
// Unpin. The caller uses its value until Unpin.
type Pinned[K comparable, V any] struct{ /* the cache and the entry */ }

func (p Pinned[K, V]) Value() V
func (p Pinned[K, V]) Unpin()
```

`Set` cannot fail. A value whose cost exceeds the capacity is not stored:
`Set` removes the entry of its key and passes the value to `Evicted`.

### Eviction

The cache evicts with S3-FIFO, which orders the entries in two queues
and remembers keys in a ghost:

- A small FIFO queue of a tenth of the capacity admits each new entry.
- A main FIFO queue keeps the entries that were hit.
- A ghost remembers the keys that the small queue evicted, up to the
  capacity of the main queue.

A hit adds 1 to the hits of its entry, up to 3, with a compare-and-swap
and without a lock. The evictor examines the oldest entry of the small
queue while the entries of that queue cost a tenth of the capacity or
more, and the oldest entry of the main queue otherwise:

| Queue | Entry with hits | Entry without hits |
|---|---|---|
| small | moves to the main queue | leaves the cache, and the ghost remembers its key |
| main | loses one hit and moves to the head of the main queue | leaves the cache |

A `Set` of a key that the ghost remembers admits its entry to the main
queue. An expired entry leaves when the evictor examines it, whatever
its hits.

One eviction examines at most `(3+4)*n` entries, n the number of entries
in the queues, so it ends while other goroutines hit entries. From step
`(3+2)*n`, it removes the entry that it examines whatever its hits,
unless the entry is pinned or is the new entry of the Set.

### Lookups without a lock

An index of open addressing maps the hash of a key to its entry, in
slots of `atomic.Pointer`. `Get` and `Pin` read the slots with atomic
loads and take no lock. `Set` and `Delete` take one mutex per Cache and
write the slots. When the index fills, they build a larger table and
publish it with one atomic store.

Each Set allocates a new entry. No code reuses an entry, because a
lookup may have loaded an entry that has since left. That lookup reads
the key and the value of its own entry. A Set could reuse the entry of a
key that no lookup has loaded, but only with a count of readers per
entry: an atomic add and subtract on every Get. A cache serves more Gets
than Sets, so the Set keeps its allocation and the Get stays at one
atomic load.

The hash is `hash/maphash.Comparable`. It allocates nothing for a string
or for a key without pointers.

The cache does not count its hits and misses. A count that every Get
updates is one cache line that the readers of a hot cache write in
turn. In the benchmarks of `telemetry`, a shared atomic counter took
5.3 ns per add with four goroutines, against 2.8 ns for a whole Get by
four goroutines here. A caller that needs the counts adds them around Get
with a `telemetry.ShardedCounter`, whose cells do not share a cache line.

### Pins, expiry and callbacks

- A pin keeps an entry in the cache. The sum of the costs exceeds the
  capacity only while pinned entries fill the cache. A Set then keeps
  its new entry past the capacity, and a later Set evicts down to the
  capacity again.
- `Set` and `Delete` still remove a pinned entry from the index.
  `Evicted` receives the entry at its last `Unpin`, on the goroutine of
  that Unpin, when nobody uses the value.
- An expired entry is a miss. The next Get or Pin that finds it removes
  it, and so does the evictor that examines it.
- `Evicted` runs outside the lock, after the operation that removed the
  entries, so a callback can call the cache. It receives a replaced
  entry before the entries that the same Set evicts.

### Choosing S3-FIFO

We simulated six policies on nine synthetic traces of 2,000,000
requests over 100,000 keys, with caches of 1,000 and 10,000 entries of
cost 1. The traces mix a Zipf distribution with scans of 20,000 keys
that are not read again, loops of 2,000 and 20,000 keys, a hot set that
moves by one rank every 20 or 200 requests, and a hot set that moves to
new keys every 250,000 requests. The table shows the miss ratio at
10,000 entries:

| Trace | OPT | LRU | CLOCK | SIEVE | S3-FIFO | `cache` |
|---|---|---|---|---|---|---|
| Zipf 0.8 | 0.3137 | 0.5315 | 0.5194 | 0.4558 | 0.4488 | 0.4488 |
| Zipf 1.0 | 0.1519 | 0.2636 | 0.2554 | 0.2185 | 0.2161 | 0.2161 |
| Zipf 1.2 | 0.0496 | 0.0842 | 0.0812 | 0.0704 | 0.0695 | 0.0695 |
| Zipf 1.0 with scans | 0.2886 | 0.4010 | 0.3971 | 0.3454 | 0.3415 | 0.3414 |
| Zipf 1.0 with a loop of 2,000 | 0.0871 | 0.1446 | 0.1404 | 0.1219 | 0.1210 | 0.1210 |
| Zipf 1.0 with a loop of 20,000 | 0.4423 | 0.6905 | 0.6835 | 0.6287 | 0.6172 | 0.6172 |
| Drift every 20 | 0.1792 | 0.2986 | 0.2980 | 0.3861 | 0.3336 | 0.3336 |
| Drift every 200 | 0.1543 | 0.2666 | 0.2591 | 0.2688 | 0.2300 | 0.2300 |
| Shift every 250,000 | 0.1869 | 0.2717 | 0.2654 | 0.3339 | 0.2678 | 0.2678 |

OPT is Belady's clairvoyant policy. S3-FIFO is a reference
implementation of the paper's algorithm, with hits counted up to 3 and
one hit enough to move an entry of the small queue to the main queue.
`cache` is this package, which matches the reference within 0.0002 at
both sizes on every trace.

- S3-FIFO missed fewer requests than LRU on every trace but one: the
  drift of one rank every 20 requests, where it missed 0.035 more at
  10,000 entries. At 1,000 entries it missed fewer on the same eight
  traces.
- SIEVE missed more than LRU under both drifts and the shift at 10,000
  entries, by up to 0.088, and under the drift every 20 at 1,000
  entries.
- CLOCK was within 0.013 of LRU on every trace at both sizes.

### Allocation contract and cost

Measured with Go 1.27.1 on an AMD Ryzen 9 9950X3D, `GOMAXPROCS=4`, with
string keys:

| Call | Time | Allocations |
|---|---|---|
| `Get` of a hit | 7.9 ns | 0 |
| `Get` of a miss | 7.1 ns | 0 |
| `Get` of a hit with an expiry time | 16.9 ns | 0 |
| `Get` of a hit by 4 goroutines in parallel | 2.8 ns per call | 0 |
| `Pin` and `Unpin` | 12.6 ns | 0 |
| `Set` that replaces an entry | 74 ns | 1, the entry |
| `Set` of a new key that evicts an entry | 135 ns | 1, the entry |
| `Delete` of an absent key | 11.5 ns | 0 |

The index and the ghost allocate when they grow. A Set that evicts more
than eight entries allocates the list of their callbacks.

### Errors

`New` returns `ErrConfig`, classified Invalid, for a Config without a
Clock or with a Capacity that is not positive. No other call returns an
error.

## Alternatives considered

### A. LRU over a map and `container/list`

The policy of `github.com/hashicorp/golang-lru` and of most Go caches.

**Why not:** every hit moves its element to the front of the list under
the cache's lock, so the readers of a hot cache serialize. The
simulation measured LRU at 0.0039 to 0.0827 more misses than S3-FIFO on
eight of the nine traces at 10,000 entries.

### B. CLOCK

One bit per entry that a hit sets without a lock, and a hand that clears
the bits and evicts the first entry without one.

**Why not:** CLOCK has the lock-free hit of S3-FIFO and the miss ratio of
LRU, within 0.013 on every trace. A scan evicts the hot entries of a
CLOCK, and the small queue of S3-FIFO admits a scanned key without
touching the main queue.

### C. SIEVE

A FIFO queue whose hand keeps the entries that were hit, published in
2024 as simpler than LRU.

**Why not:** the simulation measured SIEVE behind LRU on three traces,
by up to 0.088 under a drift of one rank every 20 requests. A cache in
front of a moving working set, such as the tiles of a growing log,
meets that pattern.

### D. W-TinyLFU

The admission policy of Caffeine, and the TinyLFU admission of
`github.com/dgraph-io/ristretto`: a count-min sketch estimates the
frequency of each key, and a candidate enters only when it is more
frequent than its victim.

**Why not:** the sketch costs a hash and four counter updates per
access, beside the queues. Core does not admit a module outside the
standard library, so the sketch would be core's own code to test against
its own traces. We did not simulate W-TinyLFU.

### E. Shards of a map with a mutex each

A fixed number of maps, each under its own lock, selected by the hash of
a key.

**Why not:** a hit still takes a lock. The index of `cache` takes none
for a hit, and its parallel Get measured 2.8 ns per call with four
goroutines.

## Drawbacks

- `cache` adds 1,085 lines of source and 1,783 lines of tests.
- A Set allocates one entry, also when it replaces the value of a key,
  because a lookup may have loaded the entry that the Set replaces.
- The eviction from step `(3+2)*n` on removes entries whatever their
  hits. A single goroutine runs an eviction that long only when every
  entry left is pinned or is the entry of the calling Set. No test can
  observe the boundary of that step, and the mutation score of `cache`
  is 99 for that reason.
- `maphash.Comparable` allocates for a key with pointers, so a lookup of
  such a key allocates.

## Open questions

None.

## Unresolved / future work

- This proposal has no loading function and no coalescing of the misses
  of one key. `batch` coalesces requests, and a caller combines it with
  a cache.
- This proposal has no shards. One mutex per Cache serializes the writes.

## References

- Juncheng Yang, Yazhuo Zhang, Ziyue Qiu, Yao Yue and Rashmi Vinayak,
  "FIFO queues are all you need for cache eviction", SOSP 2023.
- Yazhuo Zhang, Juncheng Yang, Yao Yue, Ymir Vigfusson and K. V.
  Rashmi, "SIEVE is Simpler than LRU", NSDI 2024.
- L. A. Belady, "A study of replacement algorithms for a virtual-storage
  computer", IBM Systems Journal 5(2), 1966.
- Gil Einziger, Roy Friedman and Ben Manes, "TinyLFU: A Highly Efficient
  Cache Admission Policy", ACM Transactions on Storage 13(4), 2017.
- `github.com/hashicorp/golang-lru` and `github.com/dgraph-io/ristretto`.
