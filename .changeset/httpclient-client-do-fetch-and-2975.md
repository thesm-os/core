---
"go.thesmos.sh/core": patch
---

Close the request body on every path of `httpclient.Client.Do`, `Fetch` and `AppendFetch`.
