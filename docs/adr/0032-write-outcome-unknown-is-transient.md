---
adr: 0032
title: A Write Whose Outcome Is Unknown Classifies as Transient
status: Accepted
date: 2026-09-28
supersedes: none
superseded-by: none
---

# ADR-0032: A Write Whose Outcome Is Unknown Classifies as Transient

## Status

Accepted

## Context

A conditional write can fail without telling its caller whether it took
effect. etcd's API guarantees state that "the client may be uncertain
about the status of an operation if it times out, or there is a network
disruption between the client and the etcd member".

Package `version` names the two definite outcomes of a failed
precondition, `ErrMismatch` and `ErrExists`, and had no name for the
third outcome. A store that returned it declared a sentinel of its own,
which `errs.Classify` did not recognise, so a generic retry loop gave up
on a write that a retry with the same idempotency key settles.

## Decision

We will add `version.ErrOutcomeUnknown` for a write that may or may not
have taken effect, and make `errs.Classify` recognise it as `Transient`,
because a retry of the identical write with the same idempotency key
returns the original outcome when the store recorded one.

## Alternatives Considered

### A sentinel in each store

Each store would keep a sentinel of its own.

Rejected. `version` rejects that divergence for `ErrMismatch`, and
`errs.Classify` would recognise none of the stores' sentinels.

### Class `Conflict`

The sentinel would classify as `Conflict`.

Rejected. `Conflict` means that the state moved and the caller reads
again. After an unknown outcome, the state may not have moved, and the
caller retries the identical write instead of reading.

### Class `Unspecified`

The sentinel would have no class.

Rejected. A generic retry loop would give up on a write that a retry
with the same key settles safely.

## Consequences

**Positive:**

- Stores share one spelling for the third outcome of a conditional
  write, and `resilience.Do` retries it.

**Negative:**

- A retry is safe only when the store makes it safe, through an
  idempotency key or an idempotent write. A store that returns
  `ErrOutcomeUnknown` without that guarantee turns a retry into a second
  write.

**Neutral:**

- `version` still does not import `errs`. `errs.Classify` recognises the
  sentinel, as it recognises `ErrMismatch` and `ErrExists`.

## References

- RFC-0007, version.
- RFC-0015, error classification.
- ADR-0028, a joined error classifies as its branch of highest rank.
- etcd v3.6, "API guarantees", section Operation completed,
  <https://etcd.io/docs/v3.6/learning/api_guarantees/>.
- `version/errors.go`: `ErrOutcomeUnknown`.
- `errs/classify.go`: `Classify`.
