---
"go.thesmos.sh/core": patch
---

Fix a move of a closed `sstable.Iter` or `sstable.RangeDeleteIter` to set `sstable.ErrClosed` in place of a panic.
