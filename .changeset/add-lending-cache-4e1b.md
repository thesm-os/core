---
"go.thesmos.sh/core": minor
---

Add `sstable.Block`, `sstable.NewBlock` and `sstable.LendingCache` so that a `Reader` fills a block cache without allocating a block for each miss.
