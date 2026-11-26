---
adr: 0095
title: An Interval Map Keeps the Latest End Under Each Child
status: Accepted
date: 2026-11-26
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0095: An Interval Map Keeps the Latest End Under Each Child

## Status

Accepted

## Context

A storage engine wakes the watches of keys and of key ranges when it
publishes a record that writes into them. It keeps the watches of ranges in a
slice in the order of their lower bounds. A write of a key wakes each watch
whose range contains the key, so the engine walks every watch whose range
starts at or before the key and compares the upper bound of each. A record of
one delete that woke none of them took 15.96 to 16.50 µs beside 8,192 watches
of ranges, and 125.9 to 133.0 µs beside 65,536.

No container of core finds the intervals that contain a key in less time than
that walk. `btree.MapFunc` orders its keys by one comparison, so it finds the
intervals that start at or before a key. The intervals among them that end
after the key can be at any position.

## Decision

We will add `btree.IntervalMap`, a map from intervals to values whose internal
nodes keep the interval with the latest end under each child. A query can then
skip each child whose intervals all end before the query starts.

```go
type Bound uint8

const (
	Excluded Bound = iota
	Included
	Unbounded
)

type Interval[P any] struct {
	Start    P
	End      P
	EndBound Bound
}

func NewIntervalMap[P, V any](cmp func(a, b P) int) *IntervalMap[P, V]

func (m *IntervalMap[P, V]) Containing(p P) iter.Seq2[Interval[P], V]
func (m *IntervalMap[P, V]) Overlapping(q Interval[P]) iter.Seq2[Interval[P], V]
```

- An interval contains its Start and the points after it up to its End.
  `Excluded`, the zero Bound, leaves the End out, and `Included` takes it in.
  `Unbounded` makes an interval without an end. Rust's `std::ops::Bound`
  names the three kinds of a bound the same way.
- The map orders intervals by Start, and the intervals of one Start by their
  ends. Get, Has, Set, Update, Delete, Len, Clear, Reset and All have the
  contract of the methods of `MapFunc`.
- The tree of an IntervalMap is the tree of `Map`, `MapFunc` and `Set`. Each
  internal node of an IntervalMap keeps an array with the max end of each
  child: the interval under the child whose end is the latest. The array
  shares the allocation of the child array.
- An insert raises the max ends on its path. A delete computes again each max
  end on its path that ends where the removed interval ends. The tree then
  refers to no interval that it removed. A split, a refill and a merge move
  the max ends with their children, and compute the max ends of the nodes
  that they change.
- A query descends from the root. It skips each child whose max end comes
  before the start of the query. It ends at the first interval that starts
  after the end of the query.
- After a write during a query, the query continues after the last interval
  that it yielded. The iterators of `Map` continue the same way.

## Alternatives Considered

### A B+ tree of its own for intervals

CockroachDB keeps its spans in an interval B-tree that is a tree of its own,
with the upper bound of each node.

Rejected. It repeats the inserts, splits, refills, merges, free lists and
walks of the tree: 1,161 lines of `insert.go`, `delete.go`, `node.go` and
`cursor.go` at the commit before this one. Each fix of one copy then needs
the other copy.

### A max end in every node

Each node would keep the max end of its own subtree, as Cormen et al. and
CockroachDB keep it.

Rejected. A field in each leaf grows the leaf of a `Map[int64, int64]` from
1,024 bytes to 1,032, past its size class of 1,024 bytes into the class of
1,152, in every map of the package. An array in the parent costs a map
without intervals one nil pointer in each internal node.

### A type parameter for the data of a node

The nodes of every tree would carry a value of a type parameter, with an
empty type for the maps without intervals.

Rejected. Each write of a `Map` would call the methods of the type parameter
through its dictionary. Every type of the package would also take one more
type parameter.

### A priority search tree

McCreight's priority search tree finds the intervals that contain a point in
O(log n + m) steps for m results.

Rejected. It is a second balanced tree with rotations of its own beside the
B+ tree of the package, without its free lists and its iterators.

### Unbounded intervals in a tree of their own

The map would keep the Unbounded intervals in a second tree by Start, and a
query would merge the two in order.

Rejected. The merge adds a second descent and a comparison of each result to
every query, also in a map without Unbounded intervals. A caller with many
Unbounded intervals reverses this verdict, because they raise the max ends of
the leaves where they start.

### Clone and the navigation of Map

The map would have `Clone`, `Min`, `Max`, `Floor`, `Ceil`, `Range` and the
other methods of `MapFunc`.

Rejected. No caller clones a set of intervals or walks it by Start. A clone
would also copy the array of max ends of each internal node that a write
copies.

## Consequences

**Positive:**

- A query of a point after the end of each of 65,536 intervals takes 58.18 ns
  on one core.
- Get of one of 65,536 intervals takes 153.2 ns, and a Delete with a Set of
  the same interval takes 511.8 ns.
- The queries, Get, and a Set after a Delete do not allocate.

**Negative:**

- A query compares the max end of each child that it reaches before the
  first interval that starts after the end of the query, also of each child
  that it skips.
- A long or Unbounded interval raises the max end of the leaf where it
  starts, so a query of a later point scans that leaf. A third of the 65,536
  random intervals of the benchmarks are Unbounded, and a query of a point
  that 2,380 of them contain takes 112.7 µs.
- A delete of an interval that ends where a max end ends compares up to 63
  intervals of its leaf, and up to 64 max ends in each internal node of its
  path whose max end ends there.
- The writes of a `Map` take up to 1.5% longer, and a Get 2.1% longer
  although it runs none of the new code. The geometric mean of the seven
  benchmarks of `Map` and `MapFunc` rises by 0.5%. Two layout variants of the
  commit before this one, one with an unused field in each internal node and
  one with an unused field in the tree, measured +0.03% and -0.13% in the
  geometric mean.

Six alternating rounds of three runs per build, in a rotating order with the
collector off, measured the times of `Map`. Six runs measured the times of
`IntervalMap`. Both ran on one core of an AMD Ryzen 9 9950X3D with Go 1.27.4.

**Neutral:**

- Two Unbounded intervals of one Start are one interval, whatever their End.
- An empty interval contains no point, and a query does not yield it.

## References

- Issue 160, <https://github.com/thesm-os/core/issues/160>.
- Thomas H. Cormen, Charles E. Leiserson, Ronald L. Rivest and Clifford Stein,
  *Introduction to Algorithms*, third edition, §14.3, Interval trees.
- etcd's `adt.IntervalTree`, whose `Stab` finds the range watches of a key,
  <https://github.com/etcd-io/etcd/blob/main/pkg/adt/interval_tree.go>.
- CockroachDB's interval B-tree with the upper bound of each node,
  <https://github.com/cockroachdb/cockroach/blob/master/pkg/util/interval/generic/internal/interval_btree_tmpl.go>.
- Edward M. McCreight, "Priority Search Trees", SIAM Journal on Computing,
  1985.
- Rust's `std::ops::Bound`, <https://doc.rust-lang.org/std/ops/enum.Bound.html>.
