---
"go.thesmos.sh/core": patch
---

Drop the bound of the leaf search of `btree.Map` that its comparison with the last key makes redundant.
