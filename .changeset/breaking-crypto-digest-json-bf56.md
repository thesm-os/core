---
"go.thesmos.sh/core": minor
---

**Breaking:** Encode `crypto.Digest` in JSON as the string of its lowercase hexadecimal in place of `{}`, and refuse the zero `Digest`.
