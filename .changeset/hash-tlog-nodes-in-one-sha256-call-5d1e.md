---
"go.thesmos.sh/core": patch
---

Hash each `tlog` node with one call to its hasher, and each input of up to 128 bytes to `sha256.Hasher.HashTagged` with one call to SHA-256.
