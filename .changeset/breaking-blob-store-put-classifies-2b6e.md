---
"go.thesmos.sh/core": minor
---

**Breaking:** Make `blob.Store.Put` refuse an invalid content type with `errs.Invalid` before it writes.
