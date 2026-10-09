---
"go.thesmos.sh/core": minor
---

Make `arena.Arena.Reset` zero every written byte, so `AppendVia` never exposes the bytes of a previous user.
