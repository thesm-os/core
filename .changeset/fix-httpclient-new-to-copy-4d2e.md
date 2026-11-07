---
"go.thesmos.sh/core": patch
---

Fix `httpclient.New` to give its transport a copy of the configuration of `WithTLS`.
