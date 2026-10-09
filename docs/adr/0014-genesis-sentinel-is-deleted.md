---
adr: 0014
title: The Genesis Sentinel Is Deleted, Not Documented
status: Accepted
date: 2026-08-15
supersedes: ADR-0007
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0014: The Genesis Sentinel Is Deleted, Not Documented

## Status

Accepted

Supersedes the sentinel decision in
[ADR-0007](0007-zero-digest-is-valid-chain-genesis.md). ADR-0007's
*second* decision — that the zero `Digest` has no wire form — is **not**
superseded. It is restated below and remains in force: it guards
against a truncated read decoding as a valid digest, and that hazard is
independent of whether any operation admits the value in memory.

## Context

ADR-0007 admitted the zero `Digest` into `Combine`, zero-padded to the
hasher's width, so that the sentinel `Digest.IsZero` documents would
behave as documented rather than panic.

RFC-0029 showed the value's meaning is not recoverable from its type. A
reader meeting `IsZero` and `Size() == 0` reads zero bytes and
implements `H(leaf)`; the implementation computes `H(0…0 ‖ leaf)`. The
readings diverge at a chain's first entry and both are recomputable —
over `l0 = Hash({"act":"infer","id":1})`, the implemented form is
`8005ae21d8790c4d…` and the documented form is `d753451184425020…`. An
independent verifier built from the type's own documentation rejects
honest history.

The padding rule lived only in an implementation comment. That is not a
prose failure better prose repairs: a rule that must be read somewhere
other than the type it governs will be missed again, which is the same
reasoning [ADR-0013](0013-tagged-tree-hashing-on-the-interface.md)
applies to role disjointness.

ADR-0013 deletes `Combine`, so no operation admits the value any
longer. What remains to decide is what the zero `Digest` now means.

## Decision

The zero `Digest` is the uninitialised value, and it is valid nowhere.

`CombineTagged` refuses it as an operand and panics with its own
diagnostic, distinct from the width-mismatch message: a consumer who
trips it has reached for the retired sentinel, and being told "operands
must be full-width digests" would send them to the wrong conclusion
about a value whose meaning was never readable from its type.

Genesis is expressed in the role vocabulary instead. A chain's first
link is a unary role over one operand — `HashTagged(genesis, leaf)` —
not a combine with an absent one, so there is no padding rule to state
and none to get wrong.

`Digest.IsZero` survives as the zero-value predicate; `CombineTagged`'s
own guard uses it. Its documentation is rewritten to that single
meaning, losing the genesis-anchor claim, the zero-padding rule and the
ADR-0007 citation it currently carries.

ADR-0007's wire-form decision stands unchanged, restated here so it
does not vanish with its parent: the zero `Digest` has no wire form.
Marshalling it returns an error and every decode path rejects
zero-length input, so a truncated read, a missing column or an absent
field cannot decode to a digest. Encoding absence is the containing
format's responsibility, as it is for any other optional field.

## Alternatives Considered

### Keep the sentinel and document it better

Amend `Digest.IsZero` and ADR-0007 to state the zero-padding rule
explicitly, leaving the value admitted.

Rejected. The reading that fails is the one the type invites, and every
reader who has tried has implemented `H(leaf)`. Annotating a value
whose meaning contradicts its type moves the trap without removing it.

### Keep the sentinel for tagged genesis only

Let `CombineTagged` admit the zero `Digest` for a chain's first link,
as `Combine` did, and reject it everywhere else.

Rejected. It reintroduces the case the role vocabulary deletes, on the
method whose whole premise is that a construction is legible from its
bytes. A genesis link would be `H(r ‖ 0…0 ‖ leaf)`, indistinguishable
in shape from a link over an all-zeros predecessor — the ambiguity
ADR-0007 had to record as a negative consequence, carried forward into
a design that no longer needs it.

### Give the zero `Digest` a zero-length wire form

Encode it as `Size()` bytes, as ADR-0007 also considered.

Rejected, for ADR-0007's reason, which this decision does not disturb:
a truncated read or an absent field then decodes to a valid value and
returns no error, silently, inside an audit chain.

### Delete `IsZero` along with the sentinel

If the value is valid nowhere, drop the predicate that detects it.

Rejected. The predicate is not the sentinel. Detecting an uninitialised
value is ordinary, and `CombineTagged`'s guard needs it to produce the
diagnostic that makes the retirement legible. What dies is the meaning
attached to the value, not the test for it.

## Consequences

**Positive:**

- The value's meaning is readable off its type. A verifier built from
  the documentation and one built from the implementation now compute
  the same chain, which is exactly the property that failed.
- Genesis needs no per-caller branch and no padding rule — the
  divergence ADR-0007 was written to prevent is prevented by having no
  case rather than by a rule about one.
- A consumer who reaches for the retired sentinel gets a diagnostic
  that names it, rather than a width error that sends them hunting for
  a carve-out that no longer exists.

**Negative:**

- Code that deliberately reached for the sentinel — the documented use
  — must be rewritten to the unary-role form. That is a real behaviour
  change for the one caller shape ADR-0007 was written to serve.
- Every chain whose first link was computed as `Combine(zero, leaf)`
  produces a different digest under the new construction and must be
  regenerated. This lands with the regeneration ADR-0013 already
  requires, but it is a distinct reason for it.
- The zero `Digest` moves from documented value to programmer error, so
  a mistake that previously produced a wrong-but-quiet digest now
  panics. That is the intended trade and it is still a trade.

**Neutral:**

- The wire-form rule is unchanged in substance and only in custody.
- `Digest.IsZero` keeps its signature and its cost; only its
  documentation changes.
