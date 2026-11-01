---
"go.thesmos.sh/core": patch
---

Fix `hlc.Clock` and `fake.Clock` to move the wall forward by a nanosecond after Logical 2^32 − 1 in place of a wrap to 0.
