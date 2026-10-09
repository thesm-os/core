---
"go.thesmos.sh/core": minor
---

Keep the objects of `blob/memory.Store` in a `btree.Map`, so `List` reads a page in O(log n + p).
