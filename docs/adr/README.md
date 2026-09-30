# adr/

Architecture Decision Records for `core`. One file per architectural
decision significant enough to be remembered years later.

## Format

[Michael Nygard's ADR format][nygard], minimally extended with
frontmatter for searchability.

```
NNNN-short-name.md
```

- `NNNN` is a zero-padded monotonic counter. Never reused, never
  renumbered, never deleted. ADR-0042 stays ADR-0042 forever even if
  superseded or proven wrong.
- `short-name` is lowercase kebab, ~40 chars max. A human reading the
  filename should understand the decision it covers.

## Status values

- `Proposed` — drafted, not yet accepted. Rare in this flow (proposals
  typically live in `rfc/`; ADRs generally land with `Accepted` once
  the decision is made).
- `Accepted` — the current decision. Most ADRs live here.
- `Superseded` — a later ADR overrides this one. The superseded ADR
  stays on disk with its status updated and a pointer to the
  superseder in frontmatter.
- `Deprecated` — the decision no longer applies but no successor
  replaces it (the concern itself went away).

ADRs are NEVER edited in place once accepted. Supersede them with a
new ADR that explains the change; the original remains as historical
evidence of what was true at a point in time.

## Relationship to RFCs

An RFC that produces architectural decisions lands accompanied by one
or more ADRs. The RFC captures the debate and alternatives; each ADR
captures ONE decision crisply.

Decisions small enough to skip the RFC stage (single load-bearing
call, no plausible alternatives worth side-by-side comparison) go
straight to ADR.

## Template

Start from [`docs/templates/ADR.md`](../templates/ADR.md).

## Index

| ADR | Title | Status |
|-----|-------|--------|
| [0000](0000-record-architectural-decisions.md) | Record Architectural Decisions | Accepted |
| [0001](0001-stdlib-only-dependencies.md) | Stdlib-Only Dependencies | Superseded by [0006](0006-stdlib-only-scope-test-dependencies.md) |
| [0002](0002-single-module-layout.md) | Single Module Layout | Accepted |
| [0003](0003-apache-2-0-with-spdx-headers.md) | Apache 2.0 with SPDX Short-Form Headers | Accepted |
| [0004](0004-library-only-release-via-plain-tags.md) | Library-Only Release via Plain Tags | Accepted |
| [0005](0005-primitive-set-chosen-for-coherence.md) | The Primitive Set Is Chosen for Coherence | Accepted |
| [0006](0006-stdlib-only-scope-test-dependencies.md) | Stdlib-Only Scope: Test Dependencies | Superseded by [0015](0015-dependency-free-x-modules-in-production.md) |
| [0007](0007-zero-digest-is-valid-chain-genesis.md) | The Zero Digest Is a Valid Chain Genesis | Superseded by [0013](0013-tagged-tree-hashing-on-the-interface.md), [0014](0014-genesis-sentinel-is-deleted.md) |
| [0008](0008-core-defines-contracts-that-describe-io.md) | Core Defines Contracts That Describe IO | Accepted |
| [0009](0009-logging-is-log-slog.md) | Logging Is log/slog | Accepted |
| [0010](0010-one-package-name-one-concept.md) | One Package Name, One Concept | Accepted |
| [0011](0011-test-doubles-named-for-behaviour.md) | Test Doubles Are Named for Their Behaviour | Accepted |
| [0012](0012-storage-is-per-kind.md) | Storage Is Per-Kind; a Unified KV Seam Is Refused | Accepted |
| [0013](0013-tagged-tree-hashing-on-the-interface.md) | Tagged Tree Hashing on the Hasher Interface | Accepted |
| [0014](0014-genesis-sentinel-is-deleted.md) | The Genesis Sentinel Is Deleted, Not Documented | Accepted |
| [0015](0015-dependency-free-x-modules-in-production.md) | Dependency-Free golang.org/x Modules in Production Code | Accepted |
| [0016](0016-persisted-encodings-are-frozen.md) | Persisted Encodings Are Frozen Before 1.0 | Accepted |
| [0017](0017-task-panic-crashes-the-process.md) | A Task's Panic Crashes the Process | Accepted |
| [0018](0018-mechanisms-not-lifecycles.md) | Core Ships Mechanisms, Not Lifecycles | Accepted |
| [0019](0019-tlog-uses-rfc-6962-bytes.md) | Transparency Log Trees Use RFC 6962's Bytes | Accepted |
| [0020](0020-core-requires-go-1-27.md) | Core Requires Go 1.27 | Accepted |
| [0021](0021-key-destruction-is-scheduled.md) | Key Destruction Is Scheduled | Accepted |
| [0022](0022-decorators-expose-what-they-wrap.md) | Decorators Expose What They Wrap | Accepted |
| [0023](0023-verifiers-resolve-from-caller-table.md) | Verifiers Resolve From a Table the Caller Writes | Accepted |
| [0024](0024-specs-reject-states-that-cannot-finish.md) | A Spec With a Terminal State Rejects States That Cannot Finish | Accepted |
| [0025](0025-tlog-builds-tagged-trees.md) | tlog Builds Tagged Trees | Accepted |
| [0026](0026-tagged-trees-have-no-empty-root.md) | A Tagged Tree Over No Leaves Has No Root | Accepted |
| [0027](0027-btree-reset-keeps-nodes.md) | A Reset Map Keeps Its Nodes for Its Next Fill | Accepted |
| [0028](0028-joined-errors-classify-by-rank.md) | A Joined Error Classifies as Its Branch of Highest Rank | Accepted |
| [0029](0029-errors-report-a-retry-delay.md) | An Error Can Report a Retry Delay | Accepted |
| [0030](0030-a-class-encodes-as-its-name.md) | A Class Encodes as Its Name | Accepted |
| [0031](0031-classify-recognises-fs-sentinels.md) | Classify Recognises the File-System Sentinels | Accepted |
| [0032](0032-write-outcome-unknown-is-transient.md) | A Write Whose Outcome Is Unknown Classifies as Transient | Accepted |
| [0033](0033-every-sentinel-has-a-class.md) | Every Core Sentinel Has a Class or a Stated Reason | Accepted |
| [0034](0034-root-children-are-policy-parties.md) | The Root's Children Are the Parties of a Policy | Accepted |
| [0035](0035-core-imports-the-kanon-runtime.md) | Core Imports the kanon Runtime | Accepted |
| [0036](0036-core-types-have-frozen-kanon-forms.md) | Core Types Have Frozen kanon Forms | Accepted |
| [0037](0037-a-bound-instrument-is-released.md) | A Bound Instrument Is Released | Accepted |
| [0038](0038-a-gauge-declares-its-set-aggregation.md) | A Gauge Declares the Aggregation of Its Attribute Sets | Accepted |
| [0039](0039-emit-methods-do-not-panic.md) | Emit Methods Do Not Panic | Accepted |
| [0040](0040-an-event-count-is-awaited-with-a-context.md) | An Event Count Is Awaited With a Context | Accepted |

[nygard]: https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions
