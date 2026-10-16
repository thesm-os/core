---
rfc: 0064
title: Checksums of Table Blocks in Their Parents
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-16
updated: 2026-10-16
discussion: https://github.com/thesm-os/core/issues/58
supersedes: none
superseded-by: none
produces-adr: ADR-0060
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0064: Checksums of Table Blocks in Their Parents

## Summary

We propose that every block of an `sstable` table has its CRC32C in the
structure that points to it, beside the CRC in the trailer of the block. The
index entry of a data block gains the CRC of the block, and the footer gains
the CRCs of the index block and the filter block. A read compares the CRC of
the block that it read with the CRC that the parent states, and returns
`ErrCorrupt` when they differ. A valid block at the offset of another block
then fails its read. The index grows by 4 bytes for each data block, and the
footer by 8 bytes.

## Motivation

The trailer of a block contains the CRC32C of the block. It verifies the
bytes of the block, and it does not verify that the block is the one that
the table expects at its offset. A probe outside this module wrote tables of
48 entries at 454d6db, with 8-byte keys and 100-byte values in blocks of
1,799 bytes:

| Change of the table | `Open` | Reads |
|---|---|---|
| The third data block copied over the second | `nil` | `Get` of a key of the second block finds nothing and returns `nil`. An iteration yields 48 entries of 32 distinct keys and returns `nil` |
| The filter block of another table of the same layout copied over the filter block | `nil` | `Get` finds none of the 48 keys and returns `nil` |

A device produces such tables:

- A misdirected write puts a valid block at the offset of another block.
- A lost write, which the device reports as done, leaves the bytes that were
  at the offset before. They can be a valid block of an earlier table.

### Prior art

- ZFS keeps the checksum of each block in the block pointer of its parent.
  The checksum hierarchy forms a Merkle tree, and "with this mechanism, ZFS
  is able to detect silent data corruption, such as bit rot, phantom writes,
  and misdirected reads and writes".
- RocksDB's format version 6 adds a value to the checksum of each block. The
  value derives from a value of the file in the footer and from the offset of
  the block. It makes "SST data misplaced within or between files ... as
  likely to fail checksum verification as random corruption". The value of
  the file derives from the unique ID of the file, or is random.
- The block handles of LevelDB and Pebble contain an offset and a length.
  The checksum of a block is in its trailer alone.

## Detailed design

### The index entry

The value of the index entry of a data block has three fields:

| Field | Encoding |
|---|---|
| Offset of the stored block | varint |
| Length of the stored block, without its trailer | varint |
| CRC32C of the stored block and its compression byte | 4 bytes |

The CRC equals the CRC in the trailer of the block. A value with other bytes
after the CRC is malformed.

### The footer

The footer has 80 bytes:

| Offset | Bytes | Field |
|---|---|---|
| 0 | 8 | Offset of the index block |
| 8 | 8 | Length of the index block, without its trailer |
| 16 | 4 | CRC32C of the index block, as in its trailer |
| 20 | 8 | Offset of the filter block, or 0 |
| 28 | 8 | Length of the filter block, or 0 |
| 36 | 4 | CRC32C of the filter block as in its trailer, or 0 |
| 40 | 8 | Number of entries |
| 48 | 8 | Lowest sequence |
| 56 | 8 | Highest sequence |
| 64 | 4 | Format version, 1 |
| 68 | 4 | CRC32C of bytes 0 to 67 |
| 72 | 8 | Magic number 0xf65ba2ce515d9bd3 |

A table without a filter has 0 in the three fields of the filter.

### Writes

The `Writer` computes the CRC of each block for its trailer, as it does
today. It writes the same CRC into the index entry of a data block, or into
the footer for the index block and the filter block. The writer computes no
other checksum.

### Reads

- `Open` checks the trailers of the index block and the filter block, and
  then compares their CRCs with the CRCs of the footer. A table without a
  filter whose footer states a filter CRC other than 0 is malformed.
- A cursor that reads a data block checks its trailer, and then compares its
  CRC with the CRC of its index entry. A block from the `BlockCache` was
  compared when the cursor added it to the cache, so a cache hit compares
  nothing.
- A CRC that differs from the CRC of the parent returns `ErrCorrupt` from
  `Open`, `Reader.Get` and the moves of an `Iter`, as a failed trailer does.

The API of `sstable` does not change.

### Format version

The table format freezes at the first release that contains `sstable`. No
release contains it, so the layout of version 1 changes in place. The golden
tables of the tests change with it.

### Tests

- A table whose second data block is a copy of its third returns
  `ErrCorrupt` from `Get` of a key of the second block and from an
  iteration.
- A table whose filter block, or whose index block, is the block of another
  table of the same layout returns `ErrCorrupt` from `Open`.
- A table with a changed CRC in an index entry, or in the footer, returns
  `ErrCorrupt`. The test seals each changed block again with a valid
  trailer, so only the comparison with the parent detects the change.
- The golden tables pin the bytes of the new layout.

## Alternatives considered

### A. The trailer alone

Each block keeps its CRC in its trailer only.

**Why not:** the probe read a misdirected data block and a foreign filter
block without an error. Every lookup of such a table can report a key of the
table as absent.

### B. A check of the keys of a block against its bounds in the index

The last key of the block before a block and the last key of the block
itself bound its keys. A read would refuse a block with a key outside them.

**Why not:** it detects a block whose keys lie outside the bounds, as in the
probe. A stale block of an earlier table whose keys lie inside the bounds
still passes, and the check does not cover the filter block.

### C. The offset of the block in its trailer

**Why not:** a block of another table at the same offset passes, as does a
filter block of another table of the same layout.

### D. A checksum with a context, as RocksDB's format version 6

The checksum of each block would include its offset and a value of the
table in the footer.

**Why not:** a block of another table fails only when the two tables have
different values. RocksDB takes the value from the unique ID of the file or
from a random number. A random value gives two writes of the same entries
different bytes, so the golden tables would need the caller to supply the
value in a new option. The CRCs in the parents do not need a value from the
caller, and they cost 4 bytes for each data block.

## Drawbacks

- The index grows by 4 bytes for each data block. A table of 560,000 entries
  of 16-byte keys and 100-byte values in blocks of 4 KiB has 16,970 data
  blocks and an index of 627,384 bytes. Its index grows by 67,880 bytes, or
  10.8%, and the table of 71.8 MB grows by 0.09%.
- The footer grows from 72 to 80 bytes. A table that the code before this
  change wrote fails `Open`.
- The checks bind the blocks of a table to each other. A whole earlier table
  at the place of a table passes the checks of its blocks. An engine compares
  the summary of a table with the record of the table in its manifest.

## Open questions

None.

## Unresolved / future work

None.

## References

- Issue 58, <https://github.com/thesm-os/core/issues/58>.
- Yupu Zhang, Abhishek Rajimwale, Andrea C. Arpaci-Dusseau and Remzi H.
  Arpaci-Dusseau, "End-to-end Data Integrity for File Systems: A ZFS Case
  Study", <https://research.cs.wisc.edu/adsl/Publications/zfs-corruption-fast10.pdf>.
- facebook/rocksdb, `include/rocksdb/table.h`, the documentation of
  `format_version`, and `table/format.h`, `ChecksumModifierForContext`,
  <https://github.com/facebook/rocksdb>.
- google/leveldb, `table/format.h`, `BlockHandle`,
  <https://github.com/google/leveldb>.
- cockroachdb/pebble at 70b50023f8fe, `sstable/block/block.go`, `Handle`.
- RFC-0062, sorted tables and memtables for LSM trees.
