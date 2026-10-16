---
adr: 0060
title: Table Blocks Have Their Checksums in Their Parents
status: Accepted
date: 2026-10-16
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0060: Table Blocks Have Their Checksums in Their Parents

## Status

Accepted

## Context

The trailer of each block of an `sstable` table contains the CRC32C of the
block. The trailer verifies the bytes of a block, and it does not verify
that the block is the one that the table expects at its offset. A data
block copied over another, or a filter block of another table of the same
layout, passed `Open`, and the reads of the table reported its keys as
absent without an error.

## Decision

We will store the CRC32C of every block in the structure that points to it,
because a read then detects a valid block at the wrong offset.

- The value of the index entry of a data block is the offset and the length
  of the block as varints, then its CRC32C in 4 bytes.
- The footer has 80 bytes. It contains the CRC32C of the index block after
  its length, and the CRC32C of the filter block after its length, or 0
  without a filter.
- A read compares the CRC of a block with the CRC of its parent after it
  checks the trailer, and returns `ErrCorrupt` when they differ.
- The layout of version 1 changes in place, because no release contains
  `sstable`.

This decision supersedes the index entry of two varints and the footer of
72 bytes that RFC-0062 describes.

## Alternatives Considered

### The trailer alone

Rejected. A misdirected data block and a foreign filter block pass it.

### A checksum with a context of the offset and of the table

Rejected. A block of another table fails only when the two tables have
different values, and a random value gives two writes of the same entries
different bytes.

### A check of the keys of a block against its bounds in the index

Rejected. A stale block whose keys lie inside the bounds passes, and the
check does not cover the filter block.

## Consequences

**Positive:**

- A read of a table detects a block that a misdirected or a lost write left
  at the offset of another block, for the data, the index and the filter.

**Negative:**

- The index grows by 4 bytes for each data block, 10.8% of the index of a
  table of 16,970 blocks, and the footer by 8 bytes.
- A table of the earlier layout fails `Open`, and the golden tables of the
  tests change.

**Neutral:**

- The API of `sstable` does not change, and the writer computes no other
  checksum.

## References

- RFC-0064, checksums of table blocks in their parents.
- RFC-0062, sorted tables and memtables for LSM trees, the section The table
  format.
- Issue 58, <https://github.com/thesm-os/core/issues/58>.
