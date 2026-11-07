---
"go.thesmos.sh/core": minor
---

**Breaking:** Make `Keeper.Wrap` and `AADKeeper.WrapAAD` return `crypto.ErrKeySize` for an empty data key, in `localkey`, `kek` and the checks of `cryptotest`.
