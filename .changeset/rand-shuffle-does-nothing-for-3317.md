---
"go.thesmos.sh/core": patch
---

Fix the overflow of `rand.Shuffle` for an n of `math.MinInt`.
