---
"go.thesmos.sh/core": patch
---

Fix `resilience.Retrier` to move its budget window with `clock.Clock.Elapsed`.
