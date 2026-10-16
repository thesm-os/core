---
rfc: 0069
title: The Largest Cost of a Skip List Entry
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-16
updated: 2026-10-16
discussion: https://github.com/thesm-os/core/issues/63
supersedes: none
superseded-by: none
produces-adr: ADR-0067
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0069: The Largest Cost of a Skip List Entry

## Summary

We propose `skiplist.MaxCost`. It returns the most bytes that the `Add` of
an entry reserves in the data arena and in the node arena of any `List`,
whatever its seed. Engines whose lists have other seeds then compute the
same bound for a batch.

## Motivation

`List.Cost` is exact for one List. The height of a node derives from the
entry and the seed of the List, so two Lists of other seeds report other
costs for one entry. Issue 63 measured at v0.23.0:

- One entry of a 16-byte key and a 100-byte value takes 32 to 76 node bytes
  over 100,000 seeds.
- A batch of 1,000 such entries takes 149,940 to 150,732 bytes over 1,000
  seeds. A limit of 150,336 bytes admits the batch under 533 seeds and
  refuses it under 467.

An engine that refuses a batch above a limit before the batch enters its
log needs a bound that every replica computes alike, because every replica
and every replay of the log take each batch that the log contains. That
bound is the cost of the entry in a node of the largest height. The
documentation of `Config` states its 108 node bytes, and `skiplist` exports
no function that returns it.

### Prior art

- Pebble's `arenaskl.MaxNodeSize(keySize, valueSize uint32) uint64` returns
  "the maximum space needed for a node with the specified key and value
  sizes".
- Badger exports `skl.MaxNodeSize`, "the memory footprint of a node of
  maximum height".

## Detailed design

```go
// MaxCost returns the most bytes that Add of an entry with a key of keyLen
// bytes and a value of valueLen bytes reserves in the data arena and in the
// node arena of any List, whatever its seed. It is the cost of the entry in
// a node of the largest height. List.Cost of such an entry returns the same
// dataBytes and at most nodeBytes. The results have the order of the fields
// of Config.
//
// A keyLen or a valueLen below 0 counts as 0. A keyLen or a valueLen above
// 2^32 - 1 counts as 2^32 - 1, because an arena has at most 2^32 - 1 bytes.
//
// # Allocation contract
//
// Zero-alloc.
func MaxCost(keyLen, valueLen int) (dataBytes, nodeBytes int)
```

- `dataBytes` is keyLen plus valueLen, as `Cost` computes it.
- `nodeBytes` is 4 bytes for each of the 7 words of the header and each of
  the 20 links of the tallest node: 108 bytes.
- `MaxCost` does not read a List, so it is a function of the package and
  safe for concurrent use.

### Tests

- For random entries, seeds and lengths of values, `Cost` returns the
  `dataBytes` of `MaxCost` and at most its `nodeBytes`.
- `MaxCost` returns 108 node bytes for every length.
- A length of -1 counts as 0, and a length of 2^32 counts as 2^32 - 1.
- `TestListAllocs` and `BenchmarkList` state a ceiling of 0 allocations.

## Alternatives considered

### A. A constant of 108 bytes

**Why not:** the constant bounds the node arena alone, and a caller adds the
bytes of the data arena itself. A function in the form of `Cost` keeps the
two results of an entry together.

### B. The bound in the engine

**Why not:** the engine repeats the layout of a node. A change of the layout
makes the copy wrong without a change of an API. The key prefix in each node
was such a change, from 5 + h to 7 + h words.

### C. Heights from the sequence alone

**Why not:** the seed of the hash makes the heights unpredictable to a caller
that does not know the seed, so such a caller cannot choose entries of one
height. Heights from the sequence alone would let a caller choose them.

## Drawbacks

- `skiplist` gains a function, its tests and its benchmark.
- A bound of `MaxCost` reserves 108 node bytes for each entry, where a node
  takes 34.3 bytes on average.

## Open questions

None.

## Unresolved / future work

None.

## References

- Issue 63, <https://github.com/thesm-os/core/issues/63>.
- cockroachdb/pebble at a7b8321, `internal/arenaskl/node.go`
  (`MaxNodeSize`).
- dgraph-io/badger at 78b99b8, `skl/skl.go` (`MaxNodeSize`).
- RFC-0063, key prefixes in skip list nodes.
- RFC-0065, the cost of a skip list entry.
