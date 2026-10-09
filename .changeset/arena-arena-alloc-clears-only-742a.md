---
"go.thesmos.sh/core": minor
---

Speed up `arena.Arena.Alloc` by clearing only the bytes that a rewind or a failed `AppendVia` left written.
