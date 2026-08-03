---
adr: 0007
title: The Zero Digest Is a Valid Chain Genesis
status: Accepted
date: 2026-08-03
supersedes: none
superseded-by: none
---

# ADR-0007: The Zero Digest Is a Valid Chain Genesis

## Status

Accepted

## Context

`crypto.Digest.IsZero` documents the zero `Digest` as "the conventional
sentinel for 'no digest computed' — for example, the predecessor anchor
of the genesis entry in a hash chain."

Every `Combine` rejects it. Each implementation panics unless both
operands report the hasher's own width, and the zero `Digest` reports a
width of zero. The documented use of the documented sentinel panics.

Two accepted RFCs bear on the resolution. RFC-0004 cites `Combine`'s
panic as precedent — "the same precondition-violation discipline as the
cryptographic seam". RFC-0012 §F declines to panic on short keys partly
under a no-panic-in-production policy. Read together they are not
contradictory; they are drawing a line neither states.

The line is **programmer error versus documented sentinel**. A 17-byte
digest reaching `Combine` is a programmer error, and panicking surfaces
silent audit-chain corruption at the moment it is introduced. The zero
value is not that. It is a value the type documents, that `IsZero`
exists to detect, and that a caller reaches deliberately. Panicking on
it makes the documentation a trap.

A second question is decided by the same sentinel and must be settled
with it. If the zero `Digest` is a first-class value, what is its wire
form? An encoding that emits `Size()` bytes gives it a zero-length
representation, and a decode path that accepts zero-length input turns
any truncated read, missing column, or absent field into a genesis
anchor — silently, inside a hash chain.

## Decision

`Combine` admits the zero `Digest` as either operand, zero-padded to
the receiving hasher's digest width. Every other size mismatch
continues to panic.

The zero `Digest` is in-memory only. It has no wire form: marshalling
it returns an error, and every decode path rejects zero-length input.
Encoding the absence of a digest is the containing format's
responsibility, as it is for any other optional field.

## Alternatives Considered

### Keep `Combine` strict and delete the sentinel sentence

Rejected. Every caller then writes the same three-line branch — if the
predecessor is zero use the leaf, otherwise combine — and each writes
it slightly differently. That is per-caller divergence in the one place
divergence is written into a persisted chain and cannot be corrected
later without invalidating what was already signed.

### Frame the operands with a presence tag

A leading `0x00` for a zero operand and `0x01 || left` otherwise makes
the two cases distinguishable by construction. Rejected. It pushes the
input past 64 bytes and destroys the single-block property `Combine` is
built around — the concatenation of two 256-bit digests is exactly one
compression block, and the tag doubles the invocations on a hot path.
What the tag defends against is a collision between the sentinel and a
genuine all-zeros predecessor, and producing the latter requires a
preimage of the all-zeros digest. That is not an attack anyone can
mount.

### Give the zero `Digest` a zero-length wire form

Rejected. A truncated read or an absent field then decodes to the
genesis anchor and returns no error. The failure mode is silent and it
lands in an audit chain, which is the one place a silent failure is
least recoverable.

### Ship a genesis-digest constant

Rejected. RFC-0003 §C forbids domain constants in `core`, and a genesis
marker is necessarily a domain string belonging to a protocol `core`
does not know.

## Consequences

**Positive:**

- The documented sentinel behaves as documented. The genesis case needs
  no per-caller branch and no per-caller spelling.
- `Combine` keeps its single-block, zero-allocation contract on the
  hot path.
- Every wire-representable `Digest` is exactly 32, 48, or 64 bytes,
  which leaves the binary encoding free of a special case.
- Panic discipline is preserved for what it was introduced for: actual
  programmer error.

**Negative:**

- `Combine`'s precondition is no longer "both operands are the hasher's
  width" but that plus one admitted exception. Every hasher's
  conformance suite must cover it, and every hasher added later
  inherits the obligation.
- `Combine(zero, x)` and `Combine(allZerosDigest, x)` produce the same
  digest. The second is unreachable without a preimage of the all-zeros
  digest, but the ambiguity must be stated on `Combine` rather than
  left for a reader to derive.
- A caller that needs to persist "no digest" must encode absence in its
  own format, which is one more thing the seam does not do for it.

**Neutral:**

- No digest produced from two non-zero operands changes. Chains written
  before this decision verify unchanged.
