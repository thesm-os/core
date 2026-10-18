---
"go.thesmos.sh/core": patch
---

Fix `sstable.Writer` to keep every filter partition of more than one key within `PartitionSize`.
