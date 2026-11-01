---
"go.thesmos.sh/core": patch
---

Fix `resilience.Breaker` to time its open interval with `clock.Clock.Elapsed`.
