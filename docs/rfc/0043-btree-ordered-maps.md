---
rfc: 0043
title: B-Tree Ordered Maps
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-09-27
updated: 2026-09-27
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0043: B-Tree Ordered Maps

## Summary

We propose a package `btree` with three ordered collections. Each one is
an in-memory B+ tree whose leaves contain up to 63 items:

- `Map[K cmp.Ordered, V any]` orders its keys by `cmp.Compare`.
- `MapFunc[K, V any]` orders its keys by a function `func(a, b K) int`
  that the caller passes to `NewMapFunc`.
- `Set[K cmp.Ordered]` is a `Map` without values.

Each collection offers:

- Point operations: `Get`, `Has`, `Set`, `Update` and `Delete`. A `Set`
  has `Add`, `Has` and `Delete`.
- Navigation: `Min`, `Max`, `Floor`, `Ceil`, `PopMin` and `PopMax`.
- Order statistics: `At` and `Rank` in O(log n).
- Iterators over the whole collection and over key ranges, in both
  directions, as `iter.Seq2` and `iter.Seq`. A write during an iteration
  neither ends it nor panics.
- `DeleteRange`, and `Clone` in O(1) with copy-on-write nodes.

When the keys and values contain no pointers, the garbage collector marks
a leaf without scanning it. Only leaves contain values, and a leaf has no
pointer fields. A map reuses the nodes that its merges free. A map of
constant size does not allocate.

We measured a prototype against tidwall/btree v1.8.1, the fastest Go
B-tree we measured, on 1M `int64` keys. The prototype inserts 22 to 36%
faster, looks up 29 to 41% faster and scans 22 to 49% faster. A clone
followed by one write is 14 to 20% faster. The prototype allocates 49 to
53% fewer bytes and 65 to 75% fewer objects, and `runtime.GC` with the map
live takes 28 to 39% less time.

## Motivation

### Core has no ordered map

`blob/memory.Store.List` (`blob/memory/memory.go:353`) copies every key
that matches the prefix into a slice and sorts the slice, for every page
it returns. One page over a store of n objects costs O(n log n), so a
walk over all n objects in pages of p costs O(n²/p × log n). An ordered
map returns a page in O(log n + p).

Any caller that needs keys in order, a key range, the nearest key or the
i-th key has two options today. It sorts a Go map on every read, or it
keeps a sorted slice whose insert costs O(n).

### The Go libraries

We measured tidwall/btree v1.8.1 and google/btree v1.1.3 against a
prototype of the layout that this RFC proposes. Each run stored `int64`
keys and `int64` values with Go 1.27.1, pinned to four cores of the core
complex with 32 MB of L3 on an AMD Ryzen 9 9950X3D. Each implementation
and workload ran in a process of its own, because a heap shared with
another tree slowed the later benchmark by up to 20%. The implementations
alternated within each of five rounds, and each range covers the five
runs. Another process loaded the machine during the runs, and the
alternation exposed every implementation to that load alike. The read
benchmarks call all three implementations through a function value.

| Operation, on 1M keys unless noted | tidwall `Map` | google `BTreeG` | Prototype |
|---|---|---|---|
| `Set`, keys in random order | 155.3-162.2 ns | 181.9-186.7 ns | 120.2-124.0 ns |
| `Set`, keys in ascending order | 32.7-33.8 ns | 53.6-55.0 ns | 21.3-22.2 ns |
| `Get` | 168.3-182.9 ns | 235.1-245.0 ns | 117.8-120.3 ns |
| `Get`, 10k keys | 59.4-60.9 ns | 70.6-71.9 ns | 50.0-50.3 ns |
| `Get`, keys inserted in ascending order | 171.9-181.1 ns | 240.2-260.3 ns | 104.2-107.9 ns |
| `Delete`, then `Set` of the same key | 333.9-363.0 ns | 393.7-413.4 ns | 261.7-272.1 ns |
| Full scan, per key | 2.79-3.03 ns | 4.38-4.85 ns | 1.97-2.34 ns |
| Full scan, keys inserted in ascending order | 2.15-2.25 ns | 2.91-3.26 ns | 1.12-1.16 ns |
| `Clone`, then one `Set` | 1,061-1,119 ns | 1,077-1,112 ns | 868-904 ns |
| `Clone`, then one `Set`, 10k keys | 482.7-493.9 ns | 476.7-487.6 ns | 401.2-415.2 ns |
| Bytes and allocations of `Clone` and one `Set` | 4,950 in 13 | 4,399 in 12 | 4,288 in 7 |
| `runtime.GC` with the map live | 0.62-0.72 ms | 0.82-0.85 ms | 0.44-0.49 ms |
| Bytes allocated by the inserts, random order | 47.4 MB | 36.2 MB | 24.0 MB |
| Bytes allocated by the inserts, ascending order | 35.1 MB | 52.4 MB | 16.5 MB |
| Allocations by the inserts, random order | 69,248 | 69,052 | 23,904 |

With 1M string keys of about 20 bytes, the prototype inserts in
442.4-466.1 ns against tidwall's 499.2-512.1 ns, and allocates 35.9 MB
against 71.2 MB.

The layout of tidwall's `Map` accounts for the difference:

- A node keeps its items in a slice that `append` grows, and an internal
  node keeps its children behind a pointer to a slice. A leaf costs 2
  allocations and an internal node 4.
- Keys and values alternate in one array, so a binary search reads every
  value it passes.
- Internal nodes contain values, so a write after a clone copies values
  with every internal node on its path.
- A full node splits in the middle, so ascending keys leave every node
  half full.
- A node that a merge frees goes to the garbage collector, and the next
  split allocates a new one.

tidwall's `BTreeG`, the variant with a comparator, calls the comparator
through a function value, and it takes a `sync.RWMutex` on every call
unless `NoLocks` is set. google/btree searches with `sort.Search` and a
closure. tidwall iterates from a key to the end but has no bounded range,
and neither library offers `iter.Seq` iterators, `Floor`, `Ceil` or
`Rank`. Both compare float keys with `<`, under which a NaN key equals
every key.

### Why core

- Core imports only the standard library and dependency-free golang.org/x
  modules, so it cannot use either library.
- `blob/memory` is a caller inside core.
- The package imports only `cmp`, `iter`, `slices` and `sync/atomic`.

## Detailed design

### The package

```go
// Package btree implements ordered maps and sets as in-memory B+ trees.
package btree

// Map is an ordered map from keys to values, ordered by cmp.Compare. Its
// zero value is an empty map, ready to use.
//
// # Concurrency
//
// Not safe for concurrent use. Any number of goroutines may read a map
// that no goroutine writes. A clone is a separate map, so one goroutine
// may read a clone while another writes the original.
type Map[K cmp.Ordered, V any] struct{ /* unexported fields */ }

// Get returns the value of key and whether key is present.
func (m *Map[K, V]) Get(key K) (V, bool)

// Has reports whether key is present.
func (m *Map[K, V]) Has(key K) bool

// Set sets the value of key, and returns the previous value and whether
// there was one. When the map contains a key equal to key, Set replaces
// its value and keeps the stored key, as a Go map does.
func (m *Map[K, V]) Set(key K, value V) (V, bool)

// Update sets the value of key to fn(old, exists) in one descent, and
// returns the new value.
func (m *Map[K, V]) Update(key K, fn func(old V, exists bool) V) V

// Delete removes key, and returns its value and whether it was present.
func (m *Map[K, V]) Delete(key K) (V, bool)

// DeleteRange removes every key in [lo, hi) and returns how many it
// removed.
func (m *Map[K, V]) DeleteRange(lo, hi K) int

// Len returns the number of keys.
func (m *Map[K, V]) Len() int

// Clear removes every key.
func (m *Map[K, V]) Clear()

// Min returns the smallest key, and Max the largest.
func (m *Map[K, V]) Min() (K, V, bool)
func (m *Map[K, V]) Max() (K, V, bool)

// Floor returns the largest key at or below key, and Ceil the smallest
// key at or above it.
func (m *Map[K, V]) Floor(key K) (K, V, bool)
func (m *Map[K, V]) Ceil(key K) (K, V, bool)

// PopMin removes and returns the smallest key, and PopMax the largest.
func (m *Map[K, V]) PopMin() (K, V, bool)
func (m *Map[K, V]) PopMax() (K, V, bool)

// At returns the key at index i in key order, for 0 <= i < Len.
func (m *Map[K, V]) At(i int) (K, V, bool)

// Rank returns the number of keys below key.
func (m *Map[K, V]) Rank(key K) int

// All, Keys and Values iterate in ascending key order, and Backward in
// descending key order.
func (m *Map[K, V]) All() iter.Seq2[K, V]
func (m *Map[K, V]) Keys() iter.Seq[K]
func (m *Map[K, V]) Values() iter.Seq[V]
func (m *Map[K, V]) Backward() iter.Seq2[K, V]

// Range iterates over the keys in [lo, hi) in ascending order. Ascend
// iterates over the keys at or above from, in ascending order, and
// Descend over the keys at or below from, in descending order.
func (m *Map[K, V]) Range(lo, hi K) iter.Seq2[K, V]
func (m *Map[K, V]) Ascend(from K) iter.Seq2[K, V]
func (m *Map[K, V]) Descend(from K) iter.Seq2[K, V]

// Clone returns a map with the same keys and values in O(1). The two
// maps share every node until one of them writes to a node, which the
// write copies.
func (m *Map[K, V]) Clone() *Map[K, V]

// MapFunc is an ordered map from keys to values, ordered by the function
// that NewMapFunc receives. It has every method of Map. The zero MapFunc
// has no order, so a caller creates a MapFunc with NewMapFunc.
type MapFunc[K, V any] struct{ /* unexported fields */ }

// NewMapFunc returns an empty map ordered by cmp. cmp returns a negative
// number when a sorts before b, zero when a and b are the same key, and a
// positive number when a sorts after b. NewMapFunc panics when cmp is nil.
func NewMapFunc[K, V any](cmp func(a, b K) int) *MapFunc[K, V]

// Set is an ordered set of keys, ordered by cmp.Compare. Its zero value
// is an empty set, ready to use.
type Set[K cmp.Ordered] struct{ /* unexported fields */ }

// Add adds key, and reports whether key was absent.
func (s *Set[K]) Add(key K) bool

// Has reports whether key is present.
func (s *Set[K]) Has(key K) bool

// Delete removes key, and reports whether key was present.
func (s *Set[K]) Delete(key K) bool

// Set also has the Len, Clear, Min, Max, Floor, Ceil, PopMin, PopMax, At,
// Rank, DeleteRange and Clone methods of Map. Its All, Backward, Range,
// Ascend and Descend methods return iter.Seq[K].
```

### Order

`Map` and `Set` order their keys by `cmp.Compare`. For floating-point
keys:

- A NaN sorts before every other value.
- All NaNs are one key.
- -0.0 and 0.0 are one key.

Searches call `cmp.Less`. Go 1.27.1 compiles it to one `CMPQ` for `int64`
and to one `runtime.cmpstring` call for `string`, the same code as `<`.
The NaN rule does not slow down integer or string keys.

`MapFunc` orders its keys by its function, which must define a total
order. A function that does not makes lookups miss keys and iterators
yield keys out of order. The map does not detect it.

### Custom orders

`Map`, `MapFunc` and `Set` keep their items in one unexported type,
`tree[K, V, O]`, and each of their methods calls one method of the tree.
`O` is the order of the keys, with three methods:

- `search` returns the index of a key among the keys of a leaf, and
  whether the key is there.
- `route` returns the child of an internal node whose subtree can contain
  a key.
- `less` compares two keys, for the bounds of a range.

`Map` and `Set` use `natural`, whose `search` and `route` are binary
searches that compare with `cmp.Less`. `MapFunc` uses `custom`, the
caller's function, whose `search` and `route` call
`slices.BinarySearchFunc`. `Set` keeps its keys in a tree with values of
type `struct{}`, which take no space, because an array of `struct{}` has
size 0.

A descent calls its order once for each node that it visits. Go calls a
method of a type parameter through the dictionary of the instantiation
and does not inline it, so a lookup in a map of 65,536 keys makes 3
indirect calls. Inside the methods of `natural`, the compiler inlines
`cmp.Less`, as alternatives B and C measure it for one search:

| Search over 63 keys | Time |
|---|---|
| `cmp.Less` in the loop | 2.33-2.37 ns |
| A function value for each comparison | 4.16-4.39 ns |
| A type parameter method for each comparison | 9.0-9.8 ns |

On 65,536 `int` keys, `Map.Get` took a median of 71.4 ns with the shared
tree against 72.3 ns with code of its own for each type. benchstat reports
no difference between the two (p = 0.49, 6 alternating runs). The methods
of `natural` contain the loop of `slices.BinarySearch`. The compiler does
not inline `slices.BinarySearch` into their GC shape instances, and the
second call on every level made `Map.Get` 3.9% slower.

### Nodes

```go
// leaf contains n items in key order.
type leaf[K, V any] struct {
    keys  [63]K
    vals  [63]V
    owner uint64 // the map that may change the leaf in place
    n     int    // items in the leaf
}

// inner routes a search to one of its n+1 children. Every key under child
// i sorts before keys[i], and every key under child i+1 sorts at or after
// keys[i].
type inner[K, V any] struct {
    leaves *[64]*leaf[K, V]  // the children, in the level above the leaves
    inners *[64]*inner[K, V] // the children, in every other level
    keys   [63]K
    count  int               // items in the subtree, for At and Rank
    owner  uint64
    n      int               // separators in keys
}
```

- A leaf stores its keys and its values in separate arrays, so a search
  reads keys only.
- A leaf has no pointer field. When `K` and `V` contain no pointers, the
  runtime allocates a leaf without a malloc header, and the collector
  marks it without scanning it.
- A leaf of `int64` keys and values is 1,024 bytes, the size of an
  allocation class. A leaf of `string` keys and `int64` values is 1,528
  bytes, and the 8-byte header of an object with pointers brings it to
  the 1,536-byte class.
- An internal node contains separator keys and no values. For `int64`
  keys, an internal node takes 576 bytes and its child array 512.
- The child arrays come first in an internal node and the keys next, so
  the collector scans 16 bytes of an internal node whose keys contain no
  pointers. A leaf and an internal node end with their counters, which
  contain no pointers, so the collector stops scanning at the end of the
  keys or values.
- A leaf is one allocation. An internal node is two: the node and its
  child array. Internal nodes are about 2% of the nodes.
- A leaf has room for 63 items. With 31, random inserts took 126.1-139.6
  ns against 118.3-124.1 ns, and lookups in 1M keys took 130.9-135.0 ns
  against 115.1-119.4 ns, in five alternating runs.
- A node other than the root and the right edge contains at least 31 keys
  or separators. Every node under the root's first child is such a node,
  so a tree of height h ≥ 2 contains at least 31 × 32^(h-2) keys. A tree
  of fewer than 2^63 keys is at most 13 levels deep, and every descent
  records its path in a fixed array of 16 entries on the stack.

### Writes

- `Set` descends once and inserts into a leaf. A full leaf splits into two
  leaves of 32 items, and the parent receives the first key of the right
  leaf as a separator. A full internal node splits in the middle and
  passes its middle separator to its parent. A split of the root makes a
  new root.
- An insert that appends at the right edge of the tree splits a full leaf
  differently. The left leaf keeps all 63 items, and the new right leaf
  takes the new item. A full internal node at the right edge keeps 62
  separators. Ascending keys then fill every leaf, as PostgreSQL fills the
  left page of a rightmost page split to its fillfactor.
- `Delete` descends once and removes the key from its leaf. It fixes an
  underfull node by borrowing from a sibling or by merging with it. A
  separator that equals a deleted key remains in place, because it still
  divides the keys of its two children.
- `Update` calls its function during the descent. When the function
  writes to the map, `Update` stores the result with a second descent
  instead of through its path.
- A merge puts the freed node on the map's free list, which keeps up to
  64 leaves and 64 internal nodes. A split takes a node from the free list
  before it allocates.
- Each node records the ID of the map that may change it in place.
  `Clone` gives both maps new IDs from an atomic counter. A write copies
  every node on its path that has another map's ID, and it copies only
  the occupied slots. A map puts only nodes with its own ID on its free
  list.

### Iteration

- The iterators walk the leaves with a cursor that records its path from
  the root in a fixed array, and call `yield` for each item of each leaf.
  A range loop over an iterator allocates nothing when the compiler
  inlines the iterator.
- A write during an iteration does not end the iteration. The map counts
  its writes. After each `yield`, the iterator compares the count with
  the value it read before the call. When the count changed, the iterator
  descends from the root to the first key after the last key it yielded,
  or to the last key before it in a descending iteration, and continues
  from there.
- Such an iteration yields each key at most once, in order. It yields a
  key that a write inserts ahead of its position, and it does not yield a
  key that a write deletes ahead of its position.
- In the prototype, a range loop over 1M keys took 2.68-2.81 ns per key
  with the check, 2.50-2.62 ns without it, and 2.01-2.14 ns with a
  callback. All three ran without allocating.

### Allocation contract

| Operation | Allocations |
|---|---|
| `Get`, `Has`, `Min`, `Max`, `Floor`, `Ceil`, `At`, `Rank`, `Len` | 0 |
| Iteration, including the descents after writes | 0 |
| `Set`, `Update`, `Add` | 0, plus 1 for each leaf split and 2 for each internal split that find the free list empty |
| `Delete`, `DeleteRange`, `PopMin`, `PopMax`, `Clear` | 0 |
| `Clone` | 1, the new map |
| The first write after `Clone` | 1 for each leaf and 2 for each internal node on the write's path |

### Tests

| # | Guarantee |
|---|---|
| 1 | The trees of `Map`, of `MapFunc` in reverse order and of `Set` each run 10^6 random operations against a Go map. Every operation returns what the Go map predicts, and every 10^4 operations the tree holds the items of the Go map in order. Phases of inserts alternate with phases of deletes, so each tree grows to two levels of internal nodes and shrinks to one |
| 2 | Keys and separators are in order, nodes outside the root and the right edge contain between 31 and 63 keys or separators, every subtree count is correct, every internal node has the child array of its level, and every leaf is at the same depth. The tests check this after every operation of the fuzz test and of the structural tests, and every 10^4 operations of the random test |
| 3 | Every lookup and every iterator of `Map` returns what `slices.BinarySearch` on a sorted key slice predicts, for every key from below the smallest to above the largest, including empty ranges and `lo >= hi` |
| 4 | An iteration whose loop body inserts and deletes keys yields each key at most once and in order, yields every key inserted ahead of it, and yields no key inserted behind it |
| 5 | NaN keys and -0.0 follow `cmp.Compare` |
| 6 | A write to a map never changes a clone of it, and a write to the clone never changes the map. The random test takes a clone every 10^3 operations and compares it with the items of the Go map at the time of the clone |
| 7 | Every method of `MapFunc` follows the order of its function, tested with the reverse order |
| 8 | A fuzz test runs operation sequences on the trees of `Map` and of `MapFunc` in reverse order against a Go map |
| 9 | Structural tests pin every split, refill, merge and change of root on trees of exact shapes with up to three levels of internal nodes |
| 10 | Benchmarks report 0 allocations for reads, iteration, and churn at a steady size |
| 11 | The suite covers every statement, and gremlins kills every mutant |

## Alternatives considered

### A. Import tidwall/btree

It is the fastest Go B-tree we measured, and it has `Map`, `Set` and a
comparator variant.

**Why not:** core does not import third-party modules. The prototype is
also faster than tidwall on every row of the preceding table. It
allocates less for the same keys.

### B. A comparator type parameter that compares two keys

`Map` and `MapFunc` would share one implementation over a comparator type
with a `Compare` method.

**Why not:** Go calls a method of a type parameter through the
instantiation's dictionary and does not inline it. A search over 63 keys
took 9.0-9.8 ns this way, against 2.33-2.37 ns with `cmp.Less`. The
design calls its order through the dictionary once for each node that a
descent visits.

### C. A comparator function value in `Map`

`Map` would take its order from a function value, as tidwall's `BTreeG`
takes `less`.

**Why not:** the same search took 4.16-4.39 ns, 1.8 times the direct
search. `MapFunc` uses a function value anyway, because a caller's order
has no comparison that the compiler can inline.

### D. A branchless in-node search

**Why not:** in one run, lookups took 131.6-133.6 ns against 120.4-123.9
ns in 1M keys, and 56.9-57.5 ns against 54.0-55.0 ns in 10k keys.

### E. Linked leaves

Each leaf links to the next, so a scan runs through the leaves without a
stack.

**Why not:** a copy-on-write copy of a leaf changes the link in its left
neighbour. The neighbour then needs a copy too, and so does every leaf to
its left, so a write after `Clone` copies O(n) leaves. We did not measure
it.

### F. A pull cursor

A cursor with a fixed path array, whose `Next` advances inside a leaf
without a call.

**Why not:** it scanned at 2.53-2.55 ns per key against 2.52-2.59 ns for
`iter.Seq2`, and `iter.Seq2` is the iteration form of the language.

### G. An internal lock

tidwall's `BTreeG` takes a `sync.RWMutex` on every call by default.

**Why not:** the owner of a map already serialises its writes, often
under a lock that guards more than the map, as the owner of an
`fsm.Machine` does. A lock inside the map would be a second lock under
the first.

### H. A sorted slice, a red-black tree or a skip list

**Why not:** an insert into a sorted slice moves O(n) items. A red-black
tree allocates one node per key, with three pointers each. A skip list
follows a pointer per step and allocates per key. We did not measure
them.

### I. A B-tree with values in every node

tidwall/btree and google/btree store items in internal nodes too, so a
lookup can end above the leaves.

**Why not:** a copy of an internal node then copies 63 values. With
pointer-free leaves in both layouts, a clone followed by one `Set`
allocated 5,992 bytes and took 1,129-1,143 ns, against 4,288 bytes and
868-904 ns for the B+ tree. Lookups in 1M keys took 131.1-139.0 ns
against 117.8-120.3 ns, in the same five rounds.

### J. One node type for leaves and internal nodes

Every node would have a child pointer, which is nil in a leaf.

**Why not:** the child pointer makes every leaf an object that the
collector scans. The pointer, the subtree count and the 8-byte malloc
header also move an `int64` leaf from the 1,024-byte class to the
1,152-byte class. In a B-tree with values in every node, `runtime.GC`
with 1M keys live took 0.85-0.86 ms with one node type, against 0.43 ms
with pointer-free leaves.

### K. Nodes in per-map chunks

A map would allocate its nodes in chunks of up to 64 and hand out the
slots of a chunk until it is full.

**Why not:** when a write after `Clone` replaces a node in a chunk, the
chunk remains allocated for as long as any other node in it is reachable.
A chunk with one reachable node keeps all 64 slots allocated. No map
learns when a replaced node becomes unreachable, so none can reuse its
slot. A cloned map with chunks can grow to 64 times the size of its
reachable nodes. A clone's first writes also allocate chunks of 1 and 2
nodes. With one node type, a clone followed by one `Set` on 10k keys
allocated 6,245 bytes with chunks and took 582-592 ns, against 4,453
bytes and 505-508 ns without them.

Chunks also cut the allocations of 1M random inserts from 23,484 to 384,
and `runtime.GC` with the map live from 0.83-0.85 ms to 0.29 ms, measured
with one node type. The package does not offer them as an option.
Pointer-free leaves bring `runtime.GC` to 0.44-0.49 ms, below tidwall's
0.62-0.72 ms, and an option would let a caller choose the growth after a
clone.

### L. `arena.List`

**Why not:** a `List` moves its elements until its first chunk has room
for 4,096, and a child pointer to a moved node would refer to the old
copy. Its chunks of 4,096 nodes of 1,024 bytes are 4 MB each. `Truncate`
drops only the last elements and keeps their chunks.

### M. A node pool shared by maps

A `sync.Pool` of nodes, as `pool.Pool` wraps it, would let one map reuse
the nodes that another map frees.

**Why not:** a map frees a node only in a merge, and its own free list
already reuses those nodes. A write after `Clone` cannot free the node
that it replaces, because the clone may still read it. The pool would
receive nodes only from `Clear`.

### N. A generated copy of `Map` for `MapFunc`

A generator in a test would copy the methods of `Map` into a file of
methods of `MapFunc`, with each `cmp.Less(a, b)` replaced by a call to the
function. Go's `slices` package generates `zsortanyfunc.go` from its
sort the same way.

**Why not:** the copy is a generated file of 566 lines in the package,
with a generator and a test that fails when the file differs from
`map.go`. The shared tree of the design measured no slower on `Map.Get`.

### O. A search function passed to each descent

`Map` would pass `slices.BinarySearch` to each method of the tree, and
`MapFunc` a closure over its function.

**Why not:** in a prototype with 65,536 keys, a lookup took 72.8-74.5 ns
this way against 71.6-73.2 ns with the order as a type parameter, in the
same 10 runs. Every descent and every iterator would carry the function.
The type parameter keeps the order in the tree, and the zero `Map` has its
order without a constructor.

## Drawbacks

- A leaf has 63 slots for keys and 63 for values, whatever the value
  size. A map with large values stores pointers to them.
- A node outside the root and the right edge can be half empty, so a map
  can use up to twice the memory of its items.
- A separator can equal a deleted key. For string keys, the separator
  keeps the deleted key's bytes reachable until a split or a merge
  replaces it. Internal nodes contain about one separator per leaf, about
  2% of the keys.
- `MapFunc.Get` takes 1.33 times as long as `Map.Get` on 65,536 keys,
  because every comparison calls the function.
- String keys gain little. Lookups in 1M string keys took 515.7-552.2 ns
  against tidwall's 545.6-574.6 ns, because most of their time goes to
  comparisons that read string bytes elsewhere on the heap.
- `iter.Seq2` adds 0.4 to 0.6 ns per key to a callback, and the write
  check adds 0.1 to 0.3 ns.
- A map keeps up to 64 free leaves and 64 free internal nodes after
  deletes.
- Leaves and internal nodes are two types, so a split, a borrow and a
  merge each have a leaf form and an internal form.
- The package adds three types. `Map` and `MapFunc` have 24 methods each,
  and `Set` has 20. Each method calls one method of the tree, so the
  three types repeat one another's method lists.

## Open questions

None.

## Unresolved / future work

- Loading sorted input in O(n) without searches.
- A left-edge split for descending keys.
- Inline key prefixes, so that a string search compares fewer bytes on
  the heap.
- A `SetFunc`, for sets with a custom order.
- A follow-up change that moves `blob/memory.Store.List` to a `Map`.

## References

- tidwall/btree v1.8.1, `map.go` and `btreeg.go`,
  <https://github.com/tidwall/btree>.
- google/btree v1.1.3, `btree_generic.go`,
  <https://github.com/google/btree>.
- PostgreSQL, `src/backend/access/nbtree/nbtsplitloc.c` and
  `src/include/access/nbtree.h`, the rightmost page split and the
  default leaf fillfactor of 90, <https://github.com/postgres/postgres>.
- Go 1.27.1, `src/cmp/cmp.go`, `Less` and `Compare`.
- Go 1.27.1, `src/slices/sort.go`, `BinarySearch` and
  `BinarySearchFunc`.
- Go 1.27.1, `src/runtime/malloc.go`, `mallocgc` and
  `mallocgcSmallScanHeader`, and `src/internal/runtime/gc/malloc.go` and
  `sizeclasses.go`: the 8-byte header of an object with pointers larger
  than 512 bytes, and the allocation size classes.
- ADR-0015, the dependency rule.
- `blob/memory/memory.go`, `Store.List`.
- `arena/list.go`, `List`.
