---
"go.thesmos.sh/core": minor
---

**Breaking:** Make `sstable.Reader.NewIter` and `Reader.RangeDeletes` return a new cursor that the caller keeps after `Close`.
