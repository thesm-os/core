---
"go.thesmos.sh/core": patch
---

Report the FIPS 140-only refusal of `aesgcm.New` as `errs.Unsupported` instead of `crypto.ErrKeySize`.
