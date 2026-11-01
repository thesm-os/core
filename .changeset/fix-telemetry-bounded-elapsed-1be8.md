---
"go.thesmos.sh/core": patch
---

Fix `telemetry.BoundedHistogram` to measure the rate of each Flush with `clock.Clock.Elapsed`.
