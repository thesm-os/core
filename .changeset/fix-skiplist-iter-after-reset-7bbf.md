---
"go.thesmos.sh/core": patch
---

Fix `skiplist.Iter` to be on no entry after `skiplist.List.Reset` until a First, a Last, a SeekGE or a SeekLT moves it.
