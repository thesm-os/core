---
"go.thesmos.sh/core": minor
---

**Breaking:** Let a reader of `blob.Store.Get` fail with `version.ErrMismatch` once its version is replaced or deleted.
