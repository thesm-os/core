---
"go.thesmos.sh/core": patch
---

Cut the allocations of `id.ID.String` for 160-bit and 256-bit identifiers from 3 to 1.
