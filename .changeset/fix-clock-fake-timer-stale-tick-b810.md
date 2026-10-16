---
"go.thesmos.sh/core": patch
---

Fix the timers of `clock/fake` to drop an unreceived tick in `Stop` and `Reset`, as `time.Timer` does.
