---
"go.thesmos.sh/core": minor
---

**Breaking:** Change `sstable.Open`, `sstable.Reader.Get`, the moves of `sstable.Source`, the methods of `sstable.BlockCache`, `sstable.Writer.Add` and `sstable.Writer.Close` to take a `context.Context`.
