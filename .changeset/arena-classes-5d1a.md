---
"go.thesmos.sh/core": minor
---

Give `arena.Slabs` eight size classes between two powers of two so that a slice above `arena.MinClass` takes less than 1.125 times its length.
