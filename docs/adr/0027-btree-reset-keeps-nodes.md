---
adr: 0027
title: A Reset Map Keeps Its Nodes for Its Next Fill
status: Accepted
date: 2026-09-27
supersedes: none
superseded-by: none
---

# ADR-0027: A Reset Map Keeps Its Nodes for Its Next Fill

## Status

Accepted

## Context

A `btree.Map` that a caller fills, empties and fills again allocates its
nodes again on every fill. `Clear` drops every node but the free lists,
and a free list keeps at most 64 nodes, the ones that merges free.

A memtable, a per-batch index and any buffer that a caller reuses across
generations follow that cycle. A map of 2.7 million items has 43,000 to
87,000 leaves at 31 to 63 items each. Every generation allocates those
leaves again, and the garbage collector marks and frees the previous
generation's. A caller whose writes must not allocate cannot reuse such a
map.

The cycle of fills is the caller's lifecycle. ADR-0018 keeps lifecycles
out of core, and core provides the mechanisms that they are built on.

## Decision

We will add `Reset` to `Map`, `MapFunc` and `Set`. `Reset` removes every
item and puts every node that the map may change in place on a free
list, zeroed. Inserts take nodes from the free lists before they
allocate. Each map keeps one free list for each kind of node: leaves,
parents of leaves and parents of internal nodes. `Clear` trims each free
list to 64 nodes.

## Alternatives Considered

### A larger bound on the free lists

Every map would keep the nodes that its merges free, up to the new bound.

Rejected. A map would keep those nodes even when its caller does not fill
it again. After it shrinks, such a map would keep the memory of its
largest size. `Reset` keeps the nodes only when the caller asks for it.

### A `Clear` that keeps every node

`Clear` would keep the nodes, as Go's `clear` keeps the buckets of a Go
map.

Rejected. Nothing short of dropping the map would then release its
memory. With `Reset` and `Clear`, the caller chooses.

### One free list for both kinds of internal node

A map would keep one free list for all its internal nodes, and `Reset`
would put both kinds on it.

Rejected. An insert that needs a parent of leaves would take a parent of
internal nodes whenever that node was last on the list, and allocate a
new child array for it. A refill would then allocate arrays in
proportion to the internal nodes that it takes.

### Lazy zeroing of kept nodes

`Reset` would put the nodes on the free lists as they are, and an insert
would zero a node when it takes the node.

Rejected. The old keys and values would remain reachable from the kept
nodes until the map reuses them. A node that the map never reuses would
keep them reachable until the map becomes garbage.

### A pool of maps

A caller would put emptied maps in a pool.

Rejected. `Clear` drops the nodes of a pooled map, so the pool keeps the
map header and not the nodes.

## Consequences

**Positive:**

- A map that `Reset` empties refills without allocating when the fill
  needs no more nodes than the map kept. On 65,536 random `int` keys, a
  `Reset` followed by a fill took 4.98 ms without allocating, against
  5.28 ms, 1.48 MiB and 1,545 allocations for a fill of a new map.
- The garbage collector no longer marks and frees the nodes of each
  generation.

**Negative:**

- A map keeps the nodes of its largest fill until `Clear` or until the map
  becomes garbage.
- `Reset` runs in O(n). It visits every node and zeroes the items of
  every node that it keeps.
- A fill allocates the nodes that it needs beyond the kept ones. Two
  fills of the same size can need different numbers of nodes, because
  random inserts leave leaves between half full and full, and ascending
  inserts fill them.
- `Reset` does not keep a node that the map shares with a clone, so a
  `Reset` right after `Clone` does not keep any node.

**Neutral:**

- Merges keep at most 64 nodes of each kind, so a map that is never
  reset keeps up to 192 free nodes, up from 128.
- `Map` and `MapFunc` have 25 methods each, and `Set` has 21.

## References

- RFC-0043, B-tree ordered maps.
- ADR-0018, core ships mechanisms, not lifecycles.
- `btree/delete.go`: `reset`, `keepAll` and `clear`.
- `btree/node.go`: `take`, `trim`, `keepLeaf` and `keepInner`.
- Go, `bytes.Buffer.Reset`, which empties a buffer and keeps its storage.
