---
"go.thesmos.sh/core": patch
---

Fix `telemetry.RateLimitHandler` to measure the interval of an event with `clock.Clock.Elapsed`.
