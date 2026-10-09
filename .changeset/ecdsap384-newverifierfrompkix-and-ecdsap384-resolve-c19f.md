---
"go.thesmos.sh/core": minor
---

Cut `ecdsap384.NewVerifierFromPKIX` and `ecdsap384.Resolve` from 50 allocations to 22.
