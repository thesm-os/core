---
"go.thesmos.sh/core": patch
---

Fix `batch.Loader.Load` to start a fresh batch after every caller of the batch of its key left.
