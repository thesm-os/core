---
"go.thesmos.sh/core": patch
---

Change the moves of `sstable.Iter` and `sstable.Reader.Get` to return the error of a context that ended before each block, also for a block of the cache.
