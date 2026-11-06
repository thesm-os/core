---
"go.thesmos.sh/core": patch
---

Fix `witness.Server` to drop the Verifier of each log key that no call asked for between two snapshots.
