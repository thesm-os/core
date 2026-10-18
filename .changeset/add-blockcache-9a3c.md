---
"go.thesmos.sh/core": minor
---

Add package `sstable/blockcache` with a `LendingCache` over `cache.Cache` that returns the buffer of each block that it evicts to the pool of `sstable`.
