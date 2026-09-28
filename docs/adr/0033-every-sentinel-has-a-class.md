---
adr: 0033
title: Every Core Sentinel Has a Class or a Stated Reason
status: Accepted
date: 2026-09-28
supersedes: none
superseded-by: none
---

# ADR-0033: Every Core Sentinel Has a Class or a Stated Reason

## Status

Accepted

## Context

Core declares 78 exported sentinel errors in its production packages.
60 of them had a class under `errs.Classify`, through `errs.WithClass`
at the declaration or through recognition in `Classify`.
`task.ErrNoQuorum` stated why it has no class of its own. The other 17
had no class and no stated reason: 12 in `crypto`,
`kek.ErrKeyIDMismatch`, `epoch.ErrSize`, and `resilience.ErrConfig`,
`ErrFull` and `ErrWaitTimeout`.

An unclassified sentinel is `Unspecified`, and RFC-0015 requires a
retry loop to stop at it. Other code branches on the class: it raises an
alarm on `Integrity` and maps `NotFound` to its own not-found reply. For
such code, `Unspecified` means only that nobody has reasoned about the
error. The comment in `resilience` that left `ErrConfig` unclassified
stated that a class would invite a caller to retry a wiring mistake.
`Invalid`, the class of a wrong request, is not retryable.

RFC-0015 left open whether core packages declare sentinels that document
a class.

## Decision

We will give every exported sentinel of core a class under
`errs.Classify`, or state in its documentation why it has none. The class
follows the remedy of the failure:

| Failure | Class |
|---|---|
| A malformed argument, or a malformed encoding of a value type | `Invalid` |
| Stored data that fails a check of its structure or its binding | `Integrity` |
| A layout that this build does not know | `Unsupported` |
| A key identifier that names no key | `NotFound` |
| A key that the custodian refuses by policy | `Denied` |

A package that `errs` imports, `epoch` or `version`, cannot import
`errs`. `Classify` recognises the sentinels of such a package instead, so
`epoch.ErrSize` joins the recognised set.

This decision classifies these sentinels:

| Sentinel | Class |
|---|---|
| `crypto.ErrDigestSize` | `Invalid` |
| `crypto.ErrDigestZero` | `Invalid` |
| `crypto.ErrKeySize` | `Invalid` |
| `crypto.ErrAlgorithmMismatch` | `Invalid` |
| `crypto.ErrXOFSqueezing` | `Invalid` |
| `crypto.ErrChunkSize` | `Invalid` |
| `crypto.ErrChunkHeader` | `Invalid` |
| `crypto.ErrCiphertextShort` | `Integrity` |
| `crypto.ErrAlgorithmSize` | `Integrity` |
| `crypto.ErrEnvelopeVersion` | `Unsupported` |
| `crypto.ErrKeyID` | `NotFound` |
| `crypto.ErrKeyDestroyed` | `Denied` |
| `kek.ErrKeyIDMismatch` | `Integrity` |
| `epoch.ErrSize` | `Invalid` |
| `resilience.ErrConfig` | `Invalid` |

The documentation of each sentinel without a class states why it has
none:

- `task.ErrNoQuorum` is joined with the failures that made the quorum
  impossible, and the join classifies as the failure of highest rank, by
  ADR-0028.
- `resilience.ErrFull` and `resilience.ErrWaitTimeout` report a full
  bulkhead. In RFC-0023, a bulkhead bounds a dependency that is slow,
  and a breaker acts only on a dependency that has failed. As
  `Transient`, a rejection would count against a breaker that trips on
  `Transient`, and would open its circuit against a dependency that is
  slow but healthy. A caller that retries a rejection tags it with
  `errs.WithClass`.

## Alternatives Considered

### Leave the sentinels unclassified

`Unspecified` already stops a retry loop.

Rejected. The class of a corrupt envelope or a destroyed key would equal
the class of an error that nobody has reasoned about.

### Split `crypto.ErrKeyID`

An empty identifier would return a new sentinel that classifies as
`Invalid`. An unknown key would keep `ErrKeyID` and `NotFound`.

Rejected. The `Destroyer` contract in `crypto/keeper.go` requires
`ErrKeyID` for a key that the custodian does not have. A second sentinel
would change the contract of every custodian. An empty identifier does
not name a key either, so `NotFound` fits both cases.

### `Invalid` for a truncated envelope

`crypto.ErrCiphertextShort` and `crypto.ErrAlgorithmSize` would classify
as `Invalid`.

Rejected. `Open` reads an envelope from storage. A truncated envelope
has failed a check of its structure. `tlog` classifies a tile of the
wrong length and a malformed bundle as `Integrity` for the same reason.

### `Transient` for a full bulkhead

`resilience.ErrFull` and `resilience.ErrWaitTimeout` would classify as
`Transient`, as `ErrOpen` does.

Rejected for the reason in the decision.

## Consequences

**Positive:**

- `errs.Classify` returns a class for 75 of the 78 sentinels, and the
  documentation of each of the other 3 states why it returns
  `Unspecified`.
- A test asserts the class of each sentinel that this decision
  classifies. For `epoch.ErrSize`, the test is in `errs`, with the other
  recognised sentinels.

**Negative:**

- `crypto.ErrAlgorithmSize` classifies as `Integrity` on seal too, where
  it reports a defect in the `AEAD` and not damaged data.
- `crypto.ErrChunkHeader` classifies an unknown header version as
  `Invalid`, while `crypto.ErrEnvelopeVersion` classifies an unknown
  envelope version as `Unsupported`. One sentinel covers the three
  malformations of a chunk header, and two of them are malformed
  encodings.

**Neutral:**

- A sentinel that `errs.WithClass` tags is a comparable struct value, so
  `==` and `errors.Is` still match it.
- `crypto.Open` still returns the error of `cipher.AEAD` for a tag that
  fails to verify, unwrapped and `Unspecified`, as its documentation
  states.

## References

- RFC-0015, error classification.
- RFC-0023, resilience primitives.
- ADR-0028, a joined error classifies as its branch of highest rank.
- `crypto/errors.go`, `crypto/kek/errors.go`, `epoch/errors.go` and
  `resilience/errors.go`.
- `crypto/keeper.go`: `Destroyer`.
- `errs/classify.go`: `Classify`.
- `tlog/errors.go`: `ErrTileSize` and `ErrBundle`.
