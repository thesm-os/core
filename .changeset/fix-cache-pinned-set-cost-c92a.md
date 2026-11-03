---
"go.thesmos.sh/core": patch
---

Fix the eviction of `cache.Cache` to examine each pinned entry once per pin while pinned entries fill the cache.
