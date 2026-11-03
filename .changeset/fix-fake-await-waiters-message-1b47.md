---
"go.thesmos.sh/core": patch
---

Fix the panic message of `fake.Clock.AwaitWaiters` to name a slow goroutine as a cause.
