---
"go.thesmos.sh/core": patch
---

Fix `localkey.Keeper.GenerateKey` to zero the data key when the source or the wrap fails.
