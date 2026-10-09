---
"go.thesmos.sh/core": patch
---

Stop `tsp.Verifier` from keeping a reference to the token, so a caller can reuse its buffer.
