---
adr: 0006
title: "Stdlib-Only Scope: Test Dependencies"
status: Accepted
date: 2026-08-03
supersedes: ADR-0001
superseded-by: none
---

# ADR-0006: Stdlib-Only Scope: Test Dependencies

## Status

Accepted

Supersedes [ADR-0001](0001-stdlib-only-dependencies.md). The
production constraint from ADR-0001 is unchanged and is restated here;
only its scope over test code is revised.

## Context

ADR-0001 states the stdlib-only constraint applies "including in test
files", and rejects an alternative titled "Allow non-stdlib only in
test files" on the grounds that test code is part of the published
module surface.

The module no longer matches that text. `go.mod` requires a test-only
dependency, test files import it, and the `depguard` configuration
carries `!$test` and `!**/coretest/**` exclusions with a separate rule
admitting the test dependency and a property-testing library.

Nothing is broken by the divergence: Go's module graph pruning keeps
test-only requirements of a dependency out of a consumer's build, so a
module importing `core` does not acquire them. The defect is that
ADR-0001 is cited as though its text were the enforced rule, and the
enforced rule is stricter in prose than in CI.

Two things changed since ADR-0001 was accepted, and both cut the same
way. The module's contracts are now enforced by conformance suites
rather than by review, and those suites are the mechanism that makes a
seam a contract instead of a suggestion. Writing them against
hand-rolled comparison was the alternative ADR-0001 assumed; in
practice it produced per-package assertion helpers that drifted from
one another and tested less.

## Decision

Production code in `core` imports nothing outside the Go standard
library and `core` itself. This is unchanged from ADR-0001 and remains
mechanically enforced by `depguard` in strict mode.

Test code may import a curated allow-list, currently the module's own
testing toolkit and a property-testing library. The list is closed:
adding to it requires a new ADR, not a judgement call in review.

The justification is module graph pruning. A test-only requirement of
`core` is not built, downloaded, or resolved by a module that imports
`core`, so the supply-chain argument that governs production code does
not reach test code. Where that argument does not reach, the constraint
does not apply.

## Alternatives Considered

### Restore ADR-0001's text and remove the test dependencies

Rejected. The conformance suites are the module's primary mechanism for
making a contract enforceable, and rewriting them against hand-rolled
comparison trades a closed, audited allow-list for per-package helpers
that are neither. The cost lands on correctness, and the benefit is a
supply-chain risk that pruning already eliminates.

### Leave the divergence in place

Rejected. An ADR whose text is stricter than the gate it describes is
worse than either rule alone, because contributors read the text and
reviewers enforce the gate. The two must say the same thing.

### Allow any dependency in test files

Rejected. ADR-0001's reasoning against an open allow-list is sound and
survives unchanged: there is no principled stopping point once one
exception exists on discretion. A closed list requiring an ADR to
extend keeps the stopping point explicit.

## Consequences

**Positive:**

- The ADR text and the `depguard` configuration state the same rule, so
  citing either is safe.
- Conformance suites keep the assertion vocabulary that makes them
  uniform across seams.
- The allow-list is closed and its extension procedure is an ADR, so
  growth is visible in the decision record rather than in a diff.

**Negative:**

- `core`'s `go.sum` is no longer near-empty, so "auditable at a glance"
  now means auditing a small test-only tree rather than none. The claim
  in ADR-0001's consequences no longer holds as written.
- Contributors must know that the constraint differs between production
  and test files, which is one more rule than "no dependencies".
- The module's own test code no longer demonstrates the stdlib-only
  discipline to consumers reading it as an example.

**Neutral:**

- Production import behaviour, CI enforcement mode, and the consumer
  build graph are unchanged.
