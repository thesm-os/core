---
adr: 0076
title: Fragment Partitions Are Read Through the Cache
status: Accepted
date: 2026-10-31
supersedes: RFC-0078
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0076: Fragment Partitions Are Read Through the Cache

## Status

Accepted

Supersedes the verdict of
[RFC-0078](../rfc/0078-range-tombstones-and-prefix-filters.md) on its
alternative K, which kept the block of fragments of every table in the
memory of its Reader.
[RFC-0081](../rfc/0081-fragment-partitions-version-suffixes-and-checks.md)
designs the fragment partitions. It also changes two signatures of the API
of RFC-0078: the moves of `RangeDeleteIter` take a context, and
`NewFragmenter` takes the width of the versions. The rest of RFC-0078
remains in force.

## Context

A Reader of `sstable` keeps the block of fragments of its table in memory,
outside its block cache. Its `Footprint` grows by 49 bytes for each
fragment. The probe of issue 105 measured 49,039,947 bytes of fragments for
a table of 1,000,000 logs with one truncation each, against 39,903 bytes
for the same table without them. An engine bounds the memory of its tables
by the capacity of its block cache, and that capacity does not bound the
fragments. RFC-0078 kept the fragments in memory, because each check of the
fragments of a read would take a lookup of the cache.

## Decision

We will cut the fragments of a table in partitions into fragment
partitions. A Reader will read each partition through the block cache
under the `BlockRole` `FragmentBlock`, because the capacity of the cache
then bounds the memory of the fragments of every table in partitions. The
Reader keeps the top-level index of the fragment partitions in memory. A
table without partitions keeps its block of fragments in memory.

## Alternatives Considered

### Fragments in memory

The Reader would keep the block of fragments of every table in memory, as
RFC-0078 decided.

Rejected. The memory of the fragments grows by 49 bytes for each fragment,
and the cache does not bound it.

### Fragments through the cache for every table

A table without partitions would read its block of fragments through the
cache too.

Rejected. A Reader of a table without partitions keeps its index block and
its filter block in memory. Its fragments would take a lookup of the cache
for each check, and the index would still grow with the keys.

## Consequences

**Positive:**

- The block cache bounds the memory of the fragments of a table in
  partitions. The Reader keeps an entry of the top-level index for each
  fragment partition.
- A `blockcache.Cache` pins the fragment partitions of a pinned table as it
  pins its index partitions.

**Negative:**

- A check of the fragments of a table in partitions takes a lookup of the
  cache, and a miss reads a partition from the File.
- The moves of `RangeDeleteIter` take a context and can fail, so every
  caller changes.
- `Open` of a table in partitions with fragments reads the top-level index
  of the fragments and the first fragment partition.

**Neutral:**

- A table of v0.36.0 keeps its layout, and a Reader reads its block of
  fragments in memory as before.

## References

- Issue 105, <https://github.com/thesm-os/core/issues/105>.
- RFC-0078, range tombstones and prefix filters of a table, alternative K.
- RFC-0081, fragment partitions, version suffixes and checks of a table.
