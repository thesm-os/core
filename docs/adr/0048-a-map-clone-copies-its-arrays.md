---
adr: 0048
title: A Map Clone Copies Its Arrays
status: Accepted
date: 2026-10-12
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0048: A Map Clone Copies Its Arrays

## Status

Accepted

## Context

`idmap.Map` is a hash map for one goroutine, and its API has no copy. A
caller that hands a map to another goroutine needs a copy of it. Without a
copy, the caller rebuilds the map through `All` and `Set`. The rebuild
hashes every key and writes it into a new table.

A Map keeps its keys in three arrays of a power of two of slots: the tags,
the keys and the values. The slot of a key depends on its hash and on the
keys before it in its run. Its hash depends on the key and on the seed of
the map. A copy with the same seed has every key in the same slot as the
map.

On 1M keys of 16 bytes, a copy of the three arrays took 1.99-2.04 ms and
4 allocations. A rebuild through `All` and `Set` took 25.2-25.8 ms.
`maps.Clone` of a Go map of the same keys took 4.63-5.21 ms and 4,098
allocations. All three ran on four cores of an AMD Ryzen 9 9950X3D.

## Decision

We will add `Map.Clone`, which copies the three arrays and keeps the seed,
because a caller needs a copy of a map and the copy takes less than a
tenth of the time of a rebuild:

- A write to the copy does not change the map, and a write to the map
  does not change the copy.
- Clone copies each value by assignment, as `maps.Clone` does.
- Clone allocates the copy and its three arrays.

`Concurrent` has no Clone.

## Alternatives Considered

### A rebuild through All and Set

The caller builds a new map from the keys and the values of the old one.

Rejected. A rebuild of 1M keys took 25.2-25.8 ms, and the copy of the
arrays took 1.99-2.04 ms. A rebuild also draws a new seed, and the copy
does not need one.

### A Clone in O(1) that copies on the first write

`btree.Map.Clone` returns a copy in O(1), and a later write copies the
nodes on its path. A Map would share its arrays with the copy, and the
first write to either map would copy all three arrays.

Rejected. Every Set, Delete and Clear would check whether its map shares
the arrays. The first write would still copy the whole table, because a
hash table has no path of nodes to copy.

### A Clone of Concurrent

Rejected. A consistent copy needs every bucket at one moment, so a Clone
would make the sequence number of every bucket odd for the whole copy.
Every Load of the map would wait for the copy to finish.

## Consequences

**Positive:**

- A copy of a map of 1M keys takes about 2 ms and 4 allocations.
- A copy hashes no key.

**Negative:**

- Clone takes time in proportion to the slots of the map, not to its
  keys. A map that grew and then lost most of its keys copies every slot.
- A copy takes as much memory as the map.
- A pointer value of the copy points to the same object as the value of
  the map, so a change through the pointer shows in both maps.

**Neutral:**

- The copy shares the seed of the map, so a key has the same hash in
  both maps.

## References

- RFC-0058, fast maps for identifier keys. Its API does not list a Clone.
- `idmap/map.go`: `Map`, `Map.Clone`, `Map.All` and `Map.Set`.
- `btree/doc.go`, the section Clones: the copy on write of
  `btree.Map.Clone`.
- Go 1.27.2, `src/maps/maps.go:48-56`: `maps.Clone` sets the keys and
  the values of the copy by ordinary assignment.
