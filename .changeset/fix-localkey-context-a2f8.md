---
"go.thesmos.sh/core": patch
---

Fix `localkey.Keeper.WrapAAD` and `localkey.Keeper.UnwrapAAD`, and with them `Wrap` and `Unwrap`, to return the error of a context that ended.
