---
"go.thesmos.sh/core": patch
---

Fix `httpserver.Server.Run` to return `ErrServe` for a failure of serving during a drain that times out.
