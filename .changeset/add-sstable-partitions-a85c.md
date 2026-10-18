---
"go.thesmos.sh/core": minor
---

Add `sstable.WriterOptions.PartitionSize` to cut the index and the filter of a table into partitions that a `Reader` reads through its block cache.
