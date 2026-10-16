---
"go.thesmos.sh/core": patch
---

Speed up `filter.FuseBuilder.Append` by 13% to 23% for sets of 65,536 hashes or more, at a cost of 14% for a set of 1,000.
