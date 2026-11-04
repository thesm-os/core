---
"go.thesmos.sh/core": minor
---

**Breaking:** Remove the checks of `telemetrytest` that `Counter.Add`, `Gauge.Set`, `Gauge.Add`, `Histogram.Record` of a negative value, `Span.SetAttributes` and `Span.AddEvent` return without a panic for an attribute or a finite value.
