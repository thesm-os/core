---
"go.thesmos.sh/core": patch
---

Fix `cache.Pinned.Unpin` to evict down to the capacity at the last Unpin of an entry past it.
