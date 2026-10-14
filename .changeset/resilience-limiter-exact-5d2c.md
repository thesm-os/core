---
"go.thesmos.sh/core": patch
---

Fix `resilience.Limiter` to count units exactly at any Rate, and to admit an n of zero while a call waits.
