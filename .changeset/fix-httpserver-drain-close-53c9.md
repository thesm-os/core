---
"go.thesmos.sh/core": patch
---

Fix `httpserver.Server.Run` to join the error of `http.Server.Close` to the `ErrShutdown` of a drain that times out.
