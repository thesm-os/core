---
rfc: 0063
title: Key Prefixes in Skip List Nodes
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-16
updated: 2026-10-16
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0058
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0063: Key Prefixes in Skip List Nodes

## Summary

We propose that each node of a `skiplist.List` stores the first 8 bytes of
its key in two words of the node arena. A search compares those words first,
and it reads the key from the data arena only when they are equal. A node
grows from 5 + h to 7 + h words, and its average size grows from 26.3 to 34.3
bytes. On random 16-byte keys, `Get` took 33-34% less time, and an `Add` with
one writer took 43-45% less.

## Motivation

A search of a `List` compares its key with each node that it visits. The
comparison reads the offset of the node's key from the node arena, and then
it reads the key from the data arena. The second read depends on the first.
The node arena and the data arena are separate arrays, so the key of a node
is never next to its links.

Pebble allocates a node and its key in one block of its arena, so a key
always follows its node in memory. Badger allocates the key in its arena
right after the node, and the two are adjacent unless another writer
allocates between them. Both read their arena through `unsafe`. Core does not
import `unsafe`, and without it a `[]uint32` cannot return the bytes of a key
as a `[]byte`.

A search that compares copies of the first bytes of two keys needs the keys
themselves only when those bytes are equal. PostgreSQL calls such copies
abbreviated keys and sorts with them. The B-tree layout of Müller, Benson and
Leis keeps the first 4 bytes of each key in its slot as the key's head.

### Measurements

A probe outside this module ran the `skiplist` of the last commit and two
variants with Go 1.27.2 on an AMD Ryzen 9 9950X3D. Its scope was capped at
four cores. Each comparison ran 6 rounds of 300 ms, and every other round ran
the variants in the reverse order. `Get` looked up random keys of a `List` of
1M entries. With one writer and with four writers, `Add` inserted 16-byte
keys with 100-byte values into a `List` that was sized for the run. The load
average of the machine was 3 to 5 during the runs, and the spreads of the
results were 5% to 42%.

| Keys | Operation | 5 + h words | 7 + h words, 8-byte prefix | 6 + h words, 4-byte prefix |
|---|---|---|---|---|
| Random | `Get` | 1,238-1,281 ns | 815-853 ns, −33% to −34% | 750 ns, −41% |
| Random | `Add`, 1 writer | 807-830 ns | 445-476 ns, −43% to −45% | 405 ns, −51% |
| Random | `Add`, 4 writers | 254-265 ns | 200-216 ns, −15% to −24% | 199 ns, −21% |
| First 4 bytes equal | `Get` | 1,247 ns | 847 ns, −32% | 1,428 ns, no difference at p < 0.05 |
| First 4 bytes equal | `Add`, 1 writer | 914 ns | 501 ns, −45% | 930 ns, no difference at p < 0.05 |
| First 4 bytes equal | `Add`, 4 writers | 260 ns | 198 ns, −24% | 259 ns, no difference at p < 0.05 |
| First 8 bytes equal | `Get` | 1,265-1,302 ns | 1,357-1,376 ns, no difference at p < 0.05 | not measured |
| First 8 bytes equal | `Add`, 1 writer | 856-873 ns | 918-950 ns, +11% in one of two runs | not measured |
| First 8 bytes equal | `Add`, 4 writers | 251-262 ns | 273-298 ns, no difference at p < 0.05 | not measured |

A test of the probe added 20,000 random keys of 0 to 20 bytes to each
variant. Both variants found and ordered the 15,131 distinct entries as the
last commit does.

## Detailed design

### The node

| Word | Contents |
|---|---|
| 0 | The offset of the key in the data arena |
| 1 | The length of the key |
| 2 | The length of the value |
| 3, 4 | The high and the low 32 bits of the sequence |
| 5, 6 | The high and the low 32 bits of the prefix of the key |
| 7 to 6 + h | One link per level |

The prefix of a key is its first 8 bytes as a big-endian integer, and a key
of fewer than 8 bytes is padded with zero bytes. The head node at offset 0
has 27 words, and its prefix words are 0.

Keys whose prefixes differ order as their prefixes order. Equal prefixes
leave the order open, because two keys can differ in a byte after the eighth
or in their length. "a" and "a\x00" have equal prefixes and different
lengths. Today `bytes.Compare` decides every comparison, and with the prefix
it decides only the comparisons of equal prefixes.

### The comparison

`Add` and the searches of `Get` and `Iter.SeekGE` compute the prefix of the
search key once. Each comparison then reads the two prefix words of the node
that the search visits.

```go
// compare returns -1, 0 or +1 for the order of the entry (key, seq) and the
// entry of the node nd: by key, then by sequence from the highest to the
// lowest. prefix is the prefix of key. compare reads the key of nd only when
// the two prefixes are equal.
func (l *List) compare(key []byte, prefix, seq uint64, nd uint32) int {
	np := uint64(l.nodes[nd+wordPrefixHigh])<<32 | uint64(l.nodes[nd+wordPrefixLow])
	if prefix != np {
		return cmp.Compare(prefix, np)
	}
	if c := bytes.Compare(key, l.key(nd)); c != 0 {
		return c
	}

	return cmp.Compare(uint64(l.nodes[nd+wordSeqHigh])<<32|uint64(l.nodes[nd+wordSeqLow]), seq)
}
```

### What changes for a caller

The proposal does not change a signature or the order of the entries. The
nodes exist in memory only, so no persisted format changes.

The docblock of `New` and the package comment state that a node takes 4 bytes
for each of its 7 words and its links. A node then takes 34.3 bytes on
average at the heights of e^-k. An engine that sizes its node arena by the
average of 26.3 bytes fills the arena at 77% of the entries that it planned
for, and `Add` then returns `ErrFull`.

### Tests

- The key fixtures add "a\x00" beside "a" and keys of 9 bytes that share
  their first 8 bytes. Their prefixes are equal, so the stateful machine and
  the property tests exercise the comparison of whole keys.
- The suites cover every statement, and dokimi-mutate-go detects every
  mutant that an annotation does not suppress. A mutant that replaces
  `prefix != np` with false is equivalent, because `bytes.Compare` returns
  the same order. An annotation suppresses it.
- The benchmarks measure `Get` and `Add` on random keys and on keys that
  share their first 8 bytes.

## Alternatives considered

### A. Keep 5 + h words

Each comparison reads the key from the data arena.

**Why not:** on random keys, `Get` takes 1.5 times as long as with the
prefix, and an `Add` with one writer takes 1.7 to 1.8 times as long. The
prefix costs 8 bytes per node.

### B. A prefix of 4 bytes in 6 + h words

**Why not:** it was the fastest variant on random keys. `Get` took 750 ns,
and `Add` took 405 ns. Keys that start with a table identifier of 4 bytes
have equal first 4 bytes, and the prefix of 4 bytes lost its whole gain on
them. The prefix of 8 bytes kept its gain there and costs 4 bytes more per
node.

### C. A copy of the key in the node arena after the links

A node would contain a copy of its key as words, and a comparison would read
the key from the node. Pebble and Badger also read a key next to its node.

**Why not:** `Get`, `Iter.Key` and `Iter.Value` return slices of the data
arena, so the data arena keeps the key too. For the keys of the probe, the
copy takes 16 more bytes in each node. A comparison of words also replaces
`bytes.Compare`. We have not measured it.

### D. A prefix of 16 bytes in 9 + h words

**Why not:** it costs 8 bytes per node more than the proposal, and it gains
only for keys that share 8 to 15 leading bytes. We have not measured it.

## Drawbacks

- A node takes 34.3 bytes on average against 26.3 bytes. An entry of a
  16-byte key and a 100-byte value grows by 5.6% from 142.3 to 150.3 bytes.
- Keys that share their first 8 bytes gain nothing, and URLs and paths below
  one directory are such keys. One of two runs measured an `Add` 11% slower
  on them, and the other did not find a difference at p < 0.05.
- An engine that sizes its node arena from the average of 26.3 bytes gets
  `ErrFull` at 77% of its planned entries.
- `Add` and every search compute the prefix of their key once. The prefix
  takes one load of 8 bytes, or a copy into an array for a key of fewer than
  8 bytes.

## Open questions

None.

## Unresolved / future work

None.

## References

- Marcus Müller, Lawrence Benson and Viktor Leis, "B-Trees Are Back:
  Engineering Fast and Pageable Node Layouts", Proceedings of the ACM on
  Management of Data 3(1), SIGMOD 2025,
  <https://doi.org/10.1145/3709664>. Section 3.2 describes the key heads,
  after Graefe's "poor man's normalized keys".
- PostgreSQL, `src/include/utils/sortsupport.h`, the abbreviated keys,
  <https://github.com/postgres/postgres/blob/master/src/include/utils/sortsupport.h>.
- cockroachdb/pebble at 70b50023f8fe, `internal/arenaskl/node.go`,
  `newRawNode`.
- dgraph-io/badger v4.9.6, `skl/skl.go`, `newNode`.
- RFC-0062, sorted tables and memtables for LSM trees.
