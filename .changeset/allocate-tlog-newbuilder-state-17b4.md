---
"go.thesmos.sh/core": patch
---

Allocate only the `Builder` and its state in `tlog.NewBuilder`, beyond what `ReadTiles` allocates.
