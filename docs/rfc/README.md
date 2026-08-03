# rfc/

Request for Comments for `core`. Design proposals under discussion,
plus the durable record of proposals that were accepted, rejected, or
withdrawn.

Where ADRs record the decisions `core` has enacted, RFCs record the
debates that produced them.

## Format

```
NNNN-short-name.md
```

- `NNNN` is a zero-padded monotonic counter. Never reused, never
  renumbered, even if the RFC is withdrawn.
- `short-name` is lowercase kebab, ~40 chars max.

## Status values

- `Draft` — author is still writing; not ready for review. PR may not
  be open yet.
- `Review` — open for comment. PR describing the RFC is open;
  reviewers are expected to read and comment.
- `Accepted` — merged as the agreed direction. One or more ADRs
  should follow (or the RFC itself doubles as the ADR for
  single-decision proposals).
- `Rejected` — merged with `status: Rejected`. The decision to reject
  is itself searchable; prevents re-litigating the same bad idea.
- `Withdrawn` — author pulled it before a decision was reached. Stays
  on disk with `status: Withdrawn` and a brief note on why.
- `Superseded` — a newer RFC obsoletes this one. Both stay on disk;
  cross-referenced via frontmatter.

## When to write an RFC

Write an RFC when:

- The proposal changes the contract shape (new interface, breaking
  method signature, new package).
- Multiple plausible approaches deserve side-by-side comparison.
- The change has cross-consumer implications (testkit + thesmos +
  space all have to agree).
- The motivation is load-bearing and will outlive the implementation
  (future contributors need to understand the WHY).

DO NOT write an RFC for:

- Implementation-detail choices inside one package.
- Bug fixes (PR description is enough).
- Naming / typo cleanups.
- Single-decision architectural calls with no credible alternatives
  (write an ADR directly).
- Repository hygiene and tooling changes (just PR them).

## Template

Start from [`docs/templates/RFC.md`](../templates/RFC.md).

## Index

| RFC | Title | Status |
|-----|-------|--------|
| [0000](0000-rfc-process.md) | RFC Process | Accepted |
| [0001](0001-clock-seam.md) | Clock Seam | Accepted |
| [0002](0002-rand-seam.md) | Randomness Seam | Accepted |
| [0003](0003-crypto-seam.md) | Cryptographic Hash Seam | Accepted |
| [0004](0004-telemetry-seam.md) | Telemetry Seam | Accepted |
| [0005](0005-epoch.md) | Epoch — In-Process Monotonic Counter | Accepted |
| [0006](0006-tag.md) | Tag — Snapshot-Immutable Key/Value Pairs | Accepted |
| [0007](0007-version.md) | Version — Opaque CAS Token | Accepted |
| [0008](0008-page.md) | Page — Pagination Request and Cursor Iteration | Accepted |
| [0009](0009-id.md) | ID — 128-bit Identifier Seam | Accepted |
| [0010](0010-pool.md) | Pool — Typed sync.Pool Wrappers | Accepted |
| [0011](0011-arena.md) | Arena — Bump-Allocator for Hot-Path Variable-Length Output | Accepted |
| [0012](0012-crypto-hmac-seam.md) | Cryptographic HMAC Seam | Accepted |
| [0013](0013-crypto-sign-seam.md) | Public-Key Signing Seam | Accepted |
| [0014](0014-binary-encoding-for-core-value-types.md) | Binary Encoding for Core Value Types | Accepted |
| [0015](0015-error-classification.md) | Error Classification | Accepted |
| [0016](0016-framed-domain-separation.md) | Framed Domain Separation | Accepted |
| [0017](0017-authenticated-encryption.md) | Authenticated Encryption | Accepted |
| [0018](0018-key-custody.md) | Key Custody | Accepted |
| [0019](0019-extendable-output-functions.md) | Extendable Output Functions | Draft |
| [0020](0020-trace-context-propagation.md) | Trace Context Propagation | Draft |
| [0021](0021-bounded-pool.md) | Bounded Pool | Draft |
| [0022](0022-keyed-storage.md) | Keyed Storage | Draft |
| [0023](0023-resilience-primitives.md) | Resilience Primitives | Draft |
| [0024](0024-request-coalescing.md) | Request Coalescing | Draft |

## Canonical first RFC

[RFC-0000](0000-rfc-process.md) is the meta-RFC that establishes the
RFC process itself.

## Lifecycle diagram

```
         ┌─────────┐
         │  Draft  │
         └────┬────┘
              │ author opens PR
              ▼
         ┌─────────┐
         │ Review  │
         └────┬────┘
     ┌────────┼────────┬───────────────┐
     ▼        ▼        ▼               ▼
┌─────────┐┌─────────┐┌───────────┐┌────────────┐
│Accepted ││Rejected ││Withdrawn  ││ Superseded │
└─────────┘└─────────┘└───────────┘│  (later)   │
                                   └────────────┘
```

Terminal statuses (Accepted / Rejected / Withdrawn / Superseded) all
stay on disk as the durable record.
