---
"go.thesmos.sh/core": minor
---

**Breaking:** Start a call of `task.Quorum` only while the calls in flight and the calls that succeeded are fewer than the larger of k and `limit`.
