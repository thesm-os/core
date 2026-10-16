---
"go.thesmos.sh/core": minor
---

**Breaking:** Change `sstable.NewWriter` and `sstable.Writer.Reset` to write an `sstable.Sink` in place of an `io.Writer`.
