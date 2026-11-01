---
"go.thesmos.sh/core": patch
---

Fix `witness.Server` to measure the time of a commit and the age of its state with `clock.Clock.Elapsed`.
