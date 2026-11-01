---
"go.thesmos.sh/core": patch
---

Fix `resilience.Limiter` to measure its time with `clock.Clock.Elapsed`.
