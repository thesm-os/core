---
"go.thesmos.sh/core": minor
---

**Breaking:** Return `clock.ErrOffset` from `clock.Clock.Update` for a peer instant further ahead than the max offset of the clock.
