---
"go.thesmos.sh/core": patch
---

Fix `httpclient.Client` to measure the duration of a request with `clock.Clock.Elapsed`.
