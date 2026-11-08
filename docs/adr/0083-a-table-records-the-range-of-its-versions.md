---
adr: 0083
title: A Table Records the Range of Its Versions
status: Accepted
date: 2026-11-08
supersedes: RFC-0081
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0083: A Table Records the Range of Its Versions

## Status

Accepted

Supersedes the end of the block of places at the width of the versions in
[RFC-0081](../rfc/0081-fragment-partitions-version-suffixes-and-checks.md).
The rest of RFC-0081 remains in force.

## Context

With the `SuffixBytes` of `WriterOptions`, the last bytes of each key of a
table are its version, and the start of each fragment ends with the version
of the fragment. `Summary` records the range of the keys and the range of
the sequences of a table. The versions do not follow the order of the keys,
so the first and the last key do not give the range of the versions.

An engine that keeps a version horizon refuses a write below it, because
its compactions drop each version below the horizon that a newer version
replaces. The engine needs the same check before it admits an ingest of
tables. That check needs the lowest version of each table, and without a
record in the table the engine scans every entry and every fragment.

A probe at v0.41.0 wrote a table of 68,657,471 bytes with 549,950 entries.
Each entry had a key of 16 bytes, a version of 8 bytes and a value of 100
bytes, in blocks of 4,096 bytes with Snappy, `FilterBits` 8 and
`PartitionSize` 4,096. The probe read the table from memory on one core of
an AMD Ryzen 9 9950X3D, in 3 runs of 20 iterations:

| Read of one table | Time |
|---|---|
| `Open` and `Summary` | 2.8 to 5.8 µs |
| The CRC-32C of the bytes of the table | 2.1 to 2.3 ms |
| `Open` and a scan of every entry for the lowest version | 7.0 to 7.7 ms |

An ingest of 1 TiB in tables of 64 MiB scans 16,384 tables, which takes
about 2 minutes of one core before any read from a disk.

## Decision

We will record the lowest and the highest version of the entries and the
fragments of a table with versions in its block of places. The Writer sees
each version once, and `Open` reads that block already.

```go
type Summary struct {
	SmallestKey      []byte
	LargestKey       []byte
	RangeDeleteStart []byte
	RangeDeleteEnd   []byte
	MinVersion       []byte // the lowest version of any entry or fragment, in the order of bytes.Compare
	MaxVersion       []byte // the highest version of any entry or fragment, in the order of bytes.Compare
	Entries          int64
	RangeDeletes     int64
	Size             int64
	MinSeq           uint64
	MaxSeq           uint64
	SmallestSeq      uint64
	LargestSeq       uint64
}
```

- The version of an entry is the last `SuffixBytes` bytes of its key, and
  the version of a fragment the last `SuffixBytes` bytes of its start. A key
  or a start of fewer bytes is a version as a whole, as `CompareWithSuffix`
  orders it.
- Bit 3 of the flags of the block of places states the range of the
  versions. The lowest and the highest version then follow the width, each
  as its length in a varint and its bytes. The Writer sets bit 3 for a
  table with versions that has an entry or a fragment.
- `Open` refuses bit 3 without bit 2, a version longer than the width, a
  lowest version after the highest version and a byte after the highest
  version. `Reader.Summary` returns both versions.
- A table without bit 3 has nil in both fields. Such a table has no
  versions, has neither entries nor fragments, or comes from an earlier
  Writer.
- `MaxSize` counts two versions of the full width for a table with versions
  that has an entry or a fragment.

## Alternatives Considered

### Properties that collectors of the caller compute

The Writer would take collectors of the caller and record the value of each
one under its name, as the table properties of RocksDB and the block
properties of Pebble do.

Rejected. The version is the only part of a key that the package
interprets, and `Summary` already records the range of the keys and of the
sequences. A collector adds an interface and a record of names to the
format for that one property.

### A range of the versions of each block

The index would record the range of the versions of each data block, as the
block properties of Pebble do. A scan would then skip the blocks outside a
range.

Rejected. The check of an ingest reads the range of a table. A range per
block adds two versions to each entry of the index of a table with
versions, and no read of the package skips a block by its versions.

### A scan at Open

`Open` would scan the entries and the fragments of a table with versions
for their range.

Rejected. The scan reads every data block. For the table of the probe it
takes 7.0 to 7.7 ms, against 2.8 to 5.8 µs for `Open`.

## Consequences

**Positive:**

- An engine reads the range of the versions of a table at the cost of
  `Open`.

**Negative:**

- A Reader of v0.41.0 refuses bit 3. It cannot open a table with versions
  that a newer Writer writes with an entry or a fragment.
- A table with versions of an earlier Writer has no range of versions. An
  engine that needs the range scans such a table.
- `Close` and `Open` each allocate the copies of the two versions of a
  table with versions.
- `Add` compares each version with the range. A probe added entries of a
  16-byte key and a 100-byte value, with a filter of 8 bits, on one core of
  an AMD Ryzen 9 9950X3D with Go 1.27.3, in four rotating rounds of three
  runs per build:

  | Entries | Before the change | After the change | Buffers at the end of the Writer |
  |---|---|---|---|
  | With versions of 8 bytes | 40.86 ns | 45.06 ns, 10.3% more | 44.01 ns, 7.7% more |
  | Without versions | 42.81 ns | 44.45 ns, 3.8% more | 43.48 ns, 1.6% more |

  The last column is a build with the two buffers of the range at the end
  of the Writer. It shows that most of the cost without versions comes from
  the offsets of the other fields, and `fieldalignment` sets the order of
  the fields. `Add` allocates nothing in each of the three builds.
- `MaxSize` counts versions of the full width, so a table whose versions
  are shorter than the width takes fewer bytes than its bound.

**Neutral:**

- The footer and the format version stay the same.

## References

- Issue 128, <https://github.com/thesm-os/core/issues/128>.
- RFC-0081, fragment partitions, version suffixes and checks of a table:
  the block of places and the versions of the keys.
- RFC-0078, range tombstones and prefix filters of a table: flags for the
  parts that a Reader must not skip.
- RocksDB at e591f5f, `db/table_properties_collector.h`:
  `TimestampTablePropertiesCollector`, the lowest and the highest timestamp
  of a table,
  <https://github.com/facebook/rocksdb/blob/e591f5f1ab40640a62ee5b4bd1f4e00c15c21423/db/table_properties_collector.h#L126>.
- Pebble at a995e89, `cockroachkvs/blockproperties.go` and
  `sstable/block_property.go`: `MVCCTimeInterval`, the interval of the MVCC
  timestamps of each block and of each table,
  <https://github.com/cockroachdb/pebble/blob/a995e890c06ef86c583927906e16e7f313083a3f/cockroachkvs/blockproperties.go#L15>.
