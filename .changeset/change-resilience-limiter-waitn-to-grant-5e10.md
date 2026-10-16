---
"go.thesmos.sh/core": patch
---

Change `resilience.Limiter.WaitN` to take the units of a waiting call at its grant, so a cancelled call takes none.
