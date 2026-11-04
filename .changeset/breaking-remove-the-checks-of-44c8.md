---
"go.thesmos.sh/core": minor
---

**Breaking:** Remove the checks of `cryptotest` that `Algorithm`, `KeyID` and `PublicKey` of a `sign.Signer` and a `sign.Verifier` return the same value on every call.
