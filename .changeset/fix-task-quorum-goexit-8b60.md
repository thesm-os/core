---
"go.thesmos.sh/core": patch
---

Fix `task.Quorum` to start a worker in place of one that `runtime.Goexit` ends.
