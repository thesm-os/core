---
adr: 0010
title: One Package Name, One Concept
status: Accepted
date: 2026-08-04
supersedes: none
superseded-by: none
---

# ADR-0010: One Package Name, One Concept

## Status

Accepted

## Context

ADR-0002 puts every package in `core` in a single module, so every
package shares one import namespace. Go binds the last element of an
import path as the local name, which means a repeated final element is
not a problem for the packages that repeat it — it is a problem for the
consumer that imports two of them and must invent an alias.

Five names already repeat in the module:

| name | packages |
|---|---|
| `sha256` | `crypto/sha256`, `crypto/hmac/sha256` |
| `sha3` | `crypto/sha3`, `crypto/hmac/sha3` |
| `sha512` | `crypto/sha512`, `crypto/hmac/sha512` |
| `crypto` | `crypto`, `rand/crypto` |
| `fixed` | `id/fixed`, `rand/fixed` |

The first three are not defects and a rule that forbade them would be
wrong. `crypto/sha256` and `crypto/hmac/sha256` both mean SHA-256; one
is the bare hash and the other is that hash under HMAC. A reader who
sees `sha256.New` and guesses "this is SHA-256" is right in both, and
the shared name is carrying real information.

`fixed` is different, and RFC-0025 is what surfaced it. That RFC
proposes `core/fixed` for fixed-point decimal arithmetic. The two
existing `fixed` packages mean *constant-output test double* — so
closely that `rand/fixed`'s own doc comment opens "provides a
constant-output `rand.Rand`", reaching past the package's name for the
word that describes it. A reader who sees `fixed.New` and guesses gets
it right half the time, and the failure is silent: both spellings
compile, and both produce a value.

The distinction those two rows draw is the decision. It is available
now and not later: `core` is pre-1.0 with no external consumer of
either package, and ADR-0005 records that the window in which coherence
is cheap is the one `core` is in.

## Decision

A package name may repeat within `core` only when the repeats denote
the same concept. Where two packages would claim the same final path
element for unrelated concepts, the claimant matching the ordinary
technical sense of the word keeps it and the others are renamed.

The test is what a reader guesses from the unqualified name. If every
package sharing the name satisfies that guess, the repeat stands. If
one of them defeats it, the name is overloaded and the collision is a
defect in `core`, not in the consumer that has to alias around it.

This is settled before the first external consumer of any package
involved, never after.

## Alternatives Considered

### Forbid repeated package names outright

Rejected. It condemns `crypto/sha256` and `crypto/hmac/sha256`, which
are correct as they stand — the repeated name is what tells the reader
they are the same algorithm. A rule the codebase already violates in
three places, for good reasons, would be ignored on its fourth.

### Tolerate the overload; path depth disambiguates

Rejected. Depth disambiguates in the import block and nowhere else. In
the body both are `fixed.New`, and godoc search, `grep`, stack traces
and compiler errors all show the final element alone. The one place the
distinction is visible is the one place nobody is reading when they get
it wrong.

### Push the cost to consumers as an import alias

Rejected. It is per-file, unenforceable, and nothing makes two authors
choose the same alias for the same package. The collision is created in
`core` and should be paid for in `core`, once.

### Rename the new package instead — `fixedpoint`, or `decimal`

Rejected, though it is the closest call. Both work, and `decimal` is a
good name. But `fixed` is the precise term for the technique, the
awkward spelling would land on a primitive intended for use across the
ecosystem in order to protect two doubles used in tests, and the doubles
have a better name available anyway (ADR-0011). The cost belongs on the
side with the cheaper alternative.

### Defer until a consumer actually imports both

Rejected. A collision only becomes visible at that moment, and that is
also the moment the fix stops being a pre-tag edit and becomes a
breaking change to a published import path. Deferring converts a free
decision into an expensive one and changes nothing else.

## Consequences

**Positive:**

- The unqualified package name is enough to identify what a symbol
  does, which is the assumption every reader, `grep`, and stack trace
  makes anyway.
- Consumers never alias a `core` import to disambiguate it against
  another `core` import.
- The rule ratifies the three SHA cases rather than grandfathering
  them, so it does not need an exception list.

**Negative:**

- It forces the renames in ADR-0011: two published import paths change,
  and anyone who has vendored a pre-tag commit is broken by it.
- The rule has teeth only pre-1.0. After the first tag, honouring it
  costs a major version, and it will then be in direct tension with
  compatibility. This ADR does not resolve that tension; it spends the
  window while it is open.
- "The ordinary technical sense of the word" is a judgement, not a
  test that can be mechanised. It narrows the argument to one question
  instead of settling it in advance.

**Neutral:**

- `rand/crypto` against the top-level `crypto` is the borderline case
  the rule tolerates rather than misses. Both denote cryptography — one
  the seam, one a `rand.Rand` backed by `crypto/rand` — so a reader's
  guess survives, if barely. It is recorded here as considered, not
  overlooked.
- `crypto.Framer.Fixed` is a third meaning of the word (a fixed-width
  field) and stays. It is a method on a type, cannot collide at an
  import site, and is not what this ADR governs.
