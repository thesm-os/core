<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

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
| [0008](0008-core-defines-contracts-that-describe-io.md) | Core Defines Contracts That Describe IO | Superseded by [0045](0045-core-ships-io-built-on-the-standard-library.md) |
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
| [0041](0041-a-custodian-creates-wrapping-keys.md) | A Custodian Creates Wrapping Keys | Accepted |
| [0042](0042-a-content-type-is-bounded.md) | A Content Type Is Bounded | Accepted |
| [0043](0043-core-structs-have-canonical-codecs.md) | Core Structs Have Canonical kanon Codecs | Accepted |
| [0044](0044-a-blob-reader-may-fail-after-removal.md) | A Blob Reader May Fail Once Its Version Is Removed | Accepted |
| [0045](0045-core-ships-io-built-on-the-standard-library.md) | Core Ships IO Built on the Standard Library | Accepted |
| [0046](0046-a-marked-rule-is-a-party-at-any-depth.md) | A Marked Rule Is a Party at Any Depth | Accepted |
| [0047](0047-a-cache-without-a-clock-does-not-expire.md) | A Cache Without a Clock Does Not Expire | Accepted |
| [0048](0048-a-map-clone-copies-its-arrays.md) | A Map Clone Copies Its Arrays | Accepted |
| [0049](0049-the-idmap-hash-seeds-both-factors.md) | The idmap Hash Seeds Both Factors | Accepted |
| [0050](0050-idmap-keys-maps-by-identifier-types.md) | idmap Keys Maps by Identifier Types | Accepted |
| [0051](0051-serve-runs-functions-until-their-context-ends.md) | Serve Runs Functions Until Their Context Ends | Accepted |
| [0052](0052-slabs-has-eight-classes-per-power-of-two.md) | Slabs Has Eight Size Classes per Power of Two | Accepted |
| [0053](0053-setpinned-stores-an-entry-with-a-pin.md) | SetPinned Stores an Entry With a Pin | Accepted |
| [0054](0054-resetwritten-clears-the-bytes-that-the-caller-wrote.md) | ResetWritten Clears the Bytes That the Caller Wrote | Accepted |
| [0055](0055-the-limiter-counts-units-exactly.md) | The Limiter Counts Units Exactly | Accepted |
| [0056](0056-refslabs-allocates-regions-by-reference.md) | RefSlabs Allocates Regions by Reference | Accepted |
| [0057](0057-bump-reserves-values-without-locks.md) | Bump Reserves Values Without Locks | Accepted |
| [0058](0058-skip-list-nodes-store-a-key-prefix.md) | Skip List Nodes Store a Key Prefix | Accepted |
| [0059](0059-the-last-unpin-evicts-down-to-the-capacity.md) | The Last Unpin Evicts Down to the Capacity | Accepted |
| [0060](0060-table-blocks-have-their-checksums-in-their-parents.md) | Table Blocks Have Their Checksums in Their Parents | Accepted |
| [0061](0061-a-list-reports-the-cost-of-an-entry.md) | A List Reports the Cost of an Entry | Accepted |
| [0062](0062-cursors-move-in-both-directions.md) | Cursors Move in Both Directions | Accepted, the memtable cursor superseded by [0068](0068-a-memtable-cursor-moves-back-by-a-link.md) |
| [0063](0063-waiting-calls-of-a-limiter-take-units-by-priority.md) | Waiting Calls of a Limiter Take Units by Priority | Accepted |
| [0064](0064-digests-and-ids-encode-as-hexadecimal-text.md) | Digests and IDs Encode as Their Hexadecimal Text | Accepted |
| [0065](0065-reads-and-writes-of-a-table-take-a-context.md) | Reads and Writes of a Table Take a Context | Accepted, the context of a move without a read superseded by [0070](0070-a-cursor-checks-its-context-before-each-block.md) |
| [0066](0066-a-cursor-reads-without-filling-the-cache.md) | A Cursor Reads Without Filling the Cache | Accepted |
| [0067](0067-a-list-reports-the-largest-cost-of-an-entry.md) | A List Reports the Largest Cost of an Entry | Accepted |
| [0068](0068-a-memtable-cursor-moves-back-by-a-link.md) | A Memtable Cursor Moves Back by a Link | Accepted |
| [0069](0069-an-inserter-searches-from-its-last-entry.md) | An Inserter Searches from Its Last Entry | Accepted |
| [0070](0070-a-cursor-checks-its-context-before-each-block.md) | A Cursor Checks Its Context Before Each Block | Accepted |
| [0071](0071-a-writer-keeps-a-bounded-workspace.md) | A Writer Keeps a Bounded Workspace | Accepted |
| [0072](0072-a-cursor-reads-ahead-on-request.md) | A Cursor Reads Ahead on Request | Accepted |
| [0073](0073-a-limiter-changes-its-rate-in-place.md) | A Limiter Changes Its Rate in Place | Accepted |

[nygard]: https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions
