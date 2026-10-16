---
rfc: 0065
title: The Cost of a Skip List Entry
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-16
updated: 2026-10-16
discussion: https://github.com/thesm-os/core/issues/57
supersedes: none
superseded-by: none
produces-adr: ADR-0061
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0065: The Cost of a Skip List Entry

## Summary

We propose `List.Cost`. Before an `Add`, it returns the bytes that the `Add`
of an entry reserves in the node arena and in the data arena. An engine that
admits a batch of entries into a memtable then reserves their exact size,
and applies the batch later without a path for a full memtable.

## Motivation

The bytes that `Add` reserves depend on the height of the node. The height
derives from a hash of the entry and the seed of the list, and `List`
reports neither the height nor the bytes before an `Add`. A probe outside
this module added 10,000 entries of a 16-byte key and a 100-byte value to
one list at 454d6db, and read the change of `Size` for each `Add`:

| Bytes of one `Add` | Entries |
|---|---|
| 148 | 6,270 |
| 152 | 2,355 |
| 156 | 862 |
| 160 | 316 |
| 164 | 125 |
| 168 | 46 |
| 172 | 15 |
| 176 | 6 |
| 180 | 4 |
| 184 | 1 |

An `Add` took 150.4 bytes on average. Without the cost, an engine reserves
the largest node of 27 words for each entry. With the 116 bytes of the key
and the value, each entry then reserves 224 bytes, so a memtable admits
32.9% fewer entries than fit in it.

## Detailed design

```go
// Cost returns the bytes that Add of the entry (key, seq), with a value of
// valueLen bytes, reserves in the data arena and in the node arena of l.
// The results have the order of the fields of Config. The height of the
// node derives from the entry and the seed of l, so Cost is exact: an Add
// that succeeds grows Size by dataBytes + nodeBytes.
//
// A valueLen below 0 counts as 0, and one above 2^32 - 1, the largest
// capacity of an arena, counts as 2^32 - 1.
//
// # Concurrency
//
// Safe for concurrent use with every method of l, Reset included. Cost
// reads only the seed of l.
//
// # Allocation contract
//
// Zero-alloc.
func (l *List) Cost(key []byte, seq uint64, valueLen int) (dataBytes, nodeBytes int)
```

- `dataBytes` is the length of the key plus `valueLen`.
- `nodeBytes` is 4 bytes for each word of the node: its header of 7 words
  and one link for each of its levels.
- The results have the order of `Config.DataBytes` and `Config.NodeBytes`,
  so a caller compares each with the capacity of the same name.
- `Cost` computes the height as `Add` does, with one `xxh3.Hash` of the key
  and one 128-bit multiplication. A hash of a 16-byte key took 1.47-1.48 ns
  in the comparison of `hash/xxh3` with its last commit.
- `Add` reserves the same words and bytes for the entry. An `Add` that
  returns `ErrExists` before it reserves anything takes less, and one that
  returns `ErrFull` for the data arena keeps the words of the node, as
  `Add` documents.

### Tests

- For random entries, `Cost` returns the growth of `Size` by the `Add` of
  each entry, in both arenas.
- A property checks that `Cost` returns the same values for an entry before
  and after other `Add` calls and a `Reset`.
- A valueLen of -1 and of 2^32 count as 0 and as 2^32 - 1.
- `TestListAllocs` and `BenchmarkList` state a ceiling of 0 allocations.

## Alternatives considered

### A. A reservation of the largest node

**Why not:** a memtable admits 32.9% fewer entries at entries of 116 bytes,
as measured above.

### B. Heights from the sequence alone

The height of a node would derive from the sequence of its entry, so the
cost would be exact without the seed.

**Why not:** the seed of the hash makes the heights unpredictable for a
caller that does not know the seed. Such a caller cannot choose entries of
one height and degrade the search to a scan.

### C. A method that returns the height

`List.Height(key, seq) int` would return the height, and the caller would
compute the bytes.

**Why not:** the caller would repeat the layout of a node, and a change of
the layout would change its arithmetic without a change of the signature.

### D. A reservation that a later Add consumes

`Reserve` would reserve the bytes of an entry at admission and return a
token for its `Add`.

**Why not:** the arenas reserve from their ends without a lock, so a token
would reserve a range that every other writer has to skip. A caller that
budgets with `Cost` against the capacities of its `Config` gets the same
guarantee without state in the list.

## Drawbacks

- `List` gains a method, its tests and its benchmark.
- An engine that calls `Cost` at admission and `Add` at apply hashes each
  key twice.
- The cost of an entry depends on the seed, so it differs between two lists
  of the same entries.

## Open questions

None.

## Unresolved / future work

None.

## References

- Issue 57, <https://github.com/thesm-os/core/issues/57>.
- RFC-0062, sorted tables and memtables for LSM trees.
- RFC-0063, key prefixes in skip list nodes.
