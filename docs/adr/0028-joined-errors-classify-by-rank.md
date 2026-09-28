---
adr: 0028
title: A Joined Error Classifies as Its Branch of Highest Rank
status: Accepted
date: 2026-09-28
supersedes: none
superseded-by: none
---

# ADR-0028: A Joined Error Classifies as Its Branch of Highest Rank

## Status

Accepted

## Context

`errs.Classify` returned the class of the first `Classifier` it found,
and it tried the branches of an `errors.Join` in order. The order of the
branches decided the class of a joined error.

`task.Quorum` joins its failures in the order it records them, which is
the order in which the calls finish when more than one runs at once. A
1-of-2 quorum over the same two failures, one `Transient` and one
`Integrity`, classified as `Transient` when the transient failure was
recorded first and as `Integrity` in the other order. `errs.Retryable`
returned true in the first case, so `resilience.Do` around such a quorum
retried through an `Integrity` failure. RFC-0015 defines `Integrity` as
a class that is never retried.

## Decision

We will classify an error whose `Unwrap` returns `[]error` as the class
of highest rank among its branches, in the order `Integrity`, `Denied`,
`Invalid`, `Unsupported`, `NotFound`, `Conflict`, `Transient`, with each
branch classified as it would be alone and a branch without a class
ignored, because the class of a join must not depend on the order of its
branches.

## Alternatives Considered

### Keep the first classified branch

`Classify` would keep taking the class of the first branch that has one.

Rejected. The class of a quorum failure would still depend on which call
finished first, and a retry would still run through an `Integrity`
failure whenever a transient failure came before it.

### Fix only `task.Quorum`

`Quorum` would tag its joined error with the class of highest rank.

Rejected. Every `errors.Join` outside `task` would keep the order
dependence, and each producer of a join would write the same rank.

### Report every class of the tree

`errs` would return the set of classes that a tree contains.

Rejected. RFC-0015 chose one class per error over a set, because a set
needs a precedence rule in every consumer. This decision states that
rule once.

### Rank an unclassified branch above `Transient`

A branch without a class would count as `Unspecified`, and any such
branch would make the join non-retryable.

Rejected. `task.ErrNoQuorum` has no class by design and is a branch of
every quorum failure, so no quorum failure would be retryable.

## Consequences

**Positive:**

- The class of a join does not depend on the order of its branches.
- `errs.Retryable` reports true for a join only when every branch with a
  class is `Transient`.
- `Classify` still does not allocate.

**Negative:**

- A breaker's `TripOn` sees the class of highest rank, so a join of a
  `Transient` and an `Invalid` failure no longer counts as a dependency
  failure.
- `Classify` visits every branch of a join.
- A value outside the eight classes ranks with `Unspecified` inside a
  join, while on a chain `Classify` returns it unchanged.
- An error that wraps a join and matches a recognised sentinel through
  its own `Is` method takes the class of the join.

**Neutral:**

- On a chain, the outermost `Classifier` still decides the class, so a
  layer can still reclassify an error with `errs.WithClass`.
- A join without a classified branch is `Unspecified`, as before.

## References

- RFC-0015, error classification.
- RFC-0037, periodic and quorum tasks.
- `errs/classify.go`: `Classify`, `classOf`, `joined` and `joinRank`.
- `task/quorum.go`: `record`.
