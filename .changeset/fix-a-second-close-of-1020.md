---
"go.thesmos.sh/core": patch
---

Fix a second `Close` of an `sstable.Iter` or `sstable.RangeDeleteIter` to leave the cursors of other goroutines unchanged.
