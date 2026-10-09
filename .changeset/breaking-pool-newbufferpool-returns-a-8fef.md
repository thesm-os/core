---
"go.thesmos.sh/core": minor
---

**Breaking:** Make `pool.NewBufferPool` pool `*pool.Buffer`, whose `Reset` zeroes the bytes of the previous user.
