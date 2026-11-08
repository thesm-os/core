---
"go.thesmos.sh/core": patch
---

Fix `sstable.Merger.Close` and `Merger.Reset` to clear the keys of their sources from the memory of the heap.
