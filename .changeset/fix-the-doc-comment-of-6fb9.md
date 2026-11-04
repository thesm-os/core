---
"go.thesmos.sh/core": patch
---

Fix the doc comment of `crypto.AppendOpen` to state that a failed open can overwrite the capacity of `dst`.
