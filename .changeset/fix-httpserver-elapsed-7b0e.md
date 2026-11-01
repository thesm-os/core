---
"go.thesmos.sh/core": patch
---

Fix `httpserver` to measure the duration of a request with `clock.Clock.Elapsed`.
