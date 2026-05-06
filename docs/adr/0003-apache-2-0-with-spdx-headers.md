---
adr: 0003
title: Apache 2.0 with SPDX Short-Form Headers
status: Accepted
date: 2026-05-06
supersedes: none
superseded-by: none
---

# ADR-0003: Apache 2.0 with SPDX Short-Form Headers

## Status

Accepted

## Context

`core` is published as an open-source library and depended on by every
thesmos consumer (and, eventually, third parties). The license must be:

1. Permissive enough for unencumbered downstream redistribution
   (including in commercial proprietary products).
2. Compatible with the licenses of all consumer repos.
3. Unambiguous about patent grants — `core` ships interface contracts
   that every consumer implements, so the patent-grant clause matters
   more than for typical libraries.
4. Trivially identifiable by automated license scanners (FOSSA,
   ScanCode, REUSE).

Per-file headers also need a stance. The long Apache boilerplate
(`Licensed under the Apache License, Version 2.0...`) adds 11 lines
per `.go` file. A short SPDX identifier (`SPDX-License-Identifier:
Apache-2.0`) carries the same legal weight when paired with `LICENSE`
and `NOTICE` at the repo root, and is the SPDX-recommended form.

## Decision

`core` is licensed under Apache License 2.0. Three artifacts encode
this:

1. **`LICENSE`** — the unmodified Apache 2.0 full text.
2. **`NOTICE`** — required by Apache §4(d). Contains the project's
   own attribution. Empty of third-party notices because we ship no
   third-party code.
3. **Per-file SPDX header** applied by `palantir/go-license`:

   ```go
   // Copyright Thesmos {{YEAR}}
   // SPDX-License-Identifier: Apache-2.0
   ```

   `{{YEAR}}` is filled at apply time. `make license` applies the
   header; `make lint` runs `go-license --verify` and fails CI if any
   file is missing or wrong.

Contributions are accepted under Apache 2.0 by submission. Commits are
expected to be cryptographically signed (Verified on GitHub) so that
authorship is verifiable; no separate CLA or DCO sign-off is required.

## Alternatives Considered

### MIT (matching `testkit`)

Rejected. `testkit` is a development tool; `core` ships interfaces
that every consumer implements. Apache 2.0's explicit patent-grant
clause (§3) is meaningfully stronger protection for an interface-
contract library than MIT's silence on patents.

### BSD-3-Clause

Rejected. BSD-3 has no patent grant. Same reasoning as the MIT
rejection.

### MPL-2.0 / LGPL

Rejected. Both impose file-level copyleft obligations that downstream
consumers (especially commercial deployments of `thesmos/`) would have
to actively manage. Permissive licenses propagate cleanly.

### Long Apache boilerplate per file (instead of SPDX short form)

Rejected. SPDX is the modern convention, supported by every license
compliance scanner, and shorter to read. The long boilerplate adds
no legal weight when `LICENSE` and `NOTICE` exist at the repo root.

### CLA or DCO sign-off

Rejected. CLAs require a separate legal agreement; DCO requires
`Signed-off-by:` on every commit. Both add friction without meaningful
protection beyond what the LICENSE itself provides for a permissively-
licensed project. Verified commit signatures already prove authorship,
which is the practical concern.

## Consequences

**Positive:**

- Maximum downstream usability: commercial, GPL-compatible,
  redistribution-friendly.
- Patent grant explicitly covers the interfaces — consumers
  implementing `core.Reporter` are protected from patent-troll
  claims by the original contributors.
- SPDX short-form headers are machine-readable and add ~2 lines per
  file (vs. ~13 for long boilerplate).
- `go-license --verify` makes header drift impossible — CI fails on
  any missing or malformed header.

**Negative:**

- Apache 2.0 is incompatible with GPLv2-only code. Consumers that
  must link against GPLv2-only libraries cannot also link against
  `core`. Acceptable: no current or planned consumer is GPLv2-only.
- Verified-commit signing requires one-time GPG/SSH key configuration
  per contributor. The contributor guide walks through this once.

**Neutral:**

- `NOTICE` will need to be appended to whenever third-party code is
  ever vendored. The stdlib-only constraint (ADR-0001) makes this
  unlikely, but the file exists from day one.
