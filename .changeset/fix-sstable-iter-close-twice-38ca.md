---
"go.thesmos.sh/core": patch
---

Fix `sstable.Iter.Close` to return a cursor to the pool once when a caller closes it twice.
