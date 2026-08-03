---
adr: 0008
title: Core Defines Contracts That Describe IO
status: Accepted
date: 2026-08-03
supersedes: none
superseded-by: none
---

# ADR-0008: Core Defines Contracts That Describe IO

## Status

Accepted

## Context

`core` began as value types and pure seams: a clock, a randomness
source, a hash function. Every one of them is a local computation, and
none of them can fail in a way a caller must handle.

Two additions break that pattern. A durable storage seam and a key
custody seam both describe operations that cross a process boundary,
take a `context.Context`, and return an `error` the caller must act on.
Neither is a local computation.

The question is whether `core` may define such a contract at all, or
whether IO-shaped interfaces belong one layer up in whatever module
ships an implementation.

The precedent already exists in the module and was set without being
named. `page.Cursor.Seq` takes a `context.Context` because draining a
cursor may hit a network. `telemetry.Counter.Add` takes one because
emission correlates with an active span. Both were accepted; neither
was argued as a general rule.

The standard library settles the shape of the answer. `io.Reader`
describes IO and ships no transport. `fs.FS` describes a filesystem and
ships no disk driver. `database/sql` describes a database and ships no
database. In each case the contract lives in the standard library and
every implementation lives outside it.

## Decision

`core` defines contracts that describe IO. It ships no transport, no
driver, no network code, and no implementation that opens a socket or
a file.

A contract in `core` may take a `context.Context` and return an
`error`. Where it does, the doc comment states what the context governs
— cancellation, deadline, or observability correlation — because those
are different obligations and a reader cannot infer which applies.

Implementations that perform IO live in consumer modules, where the
dependency cost of a driver is local and opt-in (ADR-0001). The one
exception is an in-memory implementation shipped alongside a seam to
give its conformance suite a subject; those perform no IO by
construction.

## Alternatives Considered

### Restrict `core` to pure value types and local computation

Rejected. The restriction is already violated by `page.Cursor` and
`telemetry.Counter`, both accepted. Enforcing it now would mean
withdrawing them, and the alternative to an IO contract in `core` is
not "no IO contract" — it is one per consumer, diverging, with the
divergence written into persisted data.

### Ship the contracts together with implementations

Rejected by ADR-0001. A storage seam that shipped a driver would drag
that driver's dependency tree into every module that imports `core`.
The seam is the part everyone needs; the driver is the part one caller
needs.

### Put IO contracts in a sibling module

Rejected. The seams that describe IO are the ones that most need
`core`'s own vocabulary — the CAS token, the pagination shape, the
error classification. A sibling module would either duplicate that
vocabulary or depend on `core` and fragment the import graph for no
gain, since `core` has no heavy dependency to isolate (ADR-0002).

## Consequences

**Positive:**

- The layer model has a stated edge: `core` describes, consumers
  implement. "Does this belong here" has an answer that does not
  depend on the author.
- Storage and custody contracts can use `core`'s own vocabulary
  directly rather than restating it.
- Conformance suites can assert against a contract that no
  implementation in the module can quietly satisfy by accident.

**Negative:**

- `core` now defines interfaces it cannot fully exercise. An in-memory
  implementation proves the contract is satisfiable, not that it is
  satisfiable over a network, and latency, partial failure and
  reconnection are exactly what an in-memory subject cannot test.
- Error semantics become contract text rather than compiler-enforced
  shape. "An absent key classifies as not-found" is a sentence a
  conformance suite checks, not a type the compiler checks.
- The `context.Context` obligation differs per method and must be
  documented per method, which is repeated prose the compiler will not
  keep honest.

**Neutral:**

- Nothing about the existing pure seams changes. This ADR names a
  practice already in the module rather than introducing one.
