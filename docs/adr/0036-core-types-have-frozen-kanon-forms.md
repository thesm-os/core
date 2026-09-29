---
adr: 0036
title: Core Types Have Frozen kanon Forms
status: Accepted
date: 2026-09-29
supersedes: none
superseded-by: none
---

# ADR-0036: Core Types Have Frozen kanon Forms

## Status

Accepted

## Context

Consumers persist core types in kanon records. kanon chooses the
encoding of a field from the Go type of the field (kanon RFC-0002):

- A named type other than a struct that has `ValidateKanon` encodes as
  its underlying type.
- A struct with a kanon codec encodes through the codec.
- A type with its own binary, gob or text methods encodes as an opaque
  byte string through those methods.
- Any other type encodes as its underlying type, and a struct of another
  package without a codec encodes inline.

A change to the methods or to the Go type of a core type changes the
bytes of every record that contains the type. ADR-0016 freezes the
binary forms of core types, but not their forms in a kanon record. For
example, adding `ValidateKanon` to `epoch.Epoch` changes each field of
the type from the 10 bytes of its opaque form to a varint, and kanon's
check of field numbers does not detect the change.

RFC-0045 chooses the kanon form of each core type that a kanon record
can contain.

## Decision

We will treat the kanon form of each type in the following table as
frozen from the release that first contains it, as ADR-0016 treats the
binary forms, because a record that a consumer persists must decode
later.

| Type | kanon form |
|---|---|
| `epoch.Epoch` | varint of the value |
| `fixed.Fixed64` | zigzag varint of the raw value, which is never `math.MinInt64` |
| `errs.Class` | varint of the number, from `Unspecified` 0 to `Integrity` 7 |
| `sign.Signature` | message with the fields `Algorithm` 1, `Value` 2 and `KeyID` 3 |
| `crypto.Digest` | bytes of the binary form |
| `id.ID` | bytes of the binary form |
| `clock.Instant` | bytes of the 16-byte binary form |
| `crypto.Algorithm` | bytes of the name |
| `sign.KeyID` | the 16 bytes |

A type that the table does not list has no frozen kanon form. Adding a
type to the table takes a new ADR.

## Alternatives Considered

### Freeze only the binary forms

ADR-0016 would remain the only record of core's persisted layouts.

Rejected. The kanon form of a type depends on the methods of the type as
well as on its binary form. Adding `ValidateKanon` or a codec, or
removing a binary method, changes every record that contains the type.
Without the table, nothing would enumerate the forms that such a change
has to be checked against.

### The name as the kanon form of a class

A class would keep its opaque form in a kanon record: its name, through
the text methods of ADR-0030, in 8 to 13 bytes.

Rejected. ADR-0030 chose the name for text formats that people read.
Code reads a kanon record. The number takes 2 bytes there.
`errs/class.go` already requires a new class to take the number after
the last one, so the numbers do not change any more than the names do.

## Consequences

**Positive:**

- Consumers can persist the listed types in kanon records before 1.0.
- Four checks of core fail a change to a listed form:
  - The golden files of kanon's conformance suite.
  - A table of the class numbers.
  - A test of the bytes of a `Signature`.
  - CI's check of field numbers.

**Negative:**

- Core cannot add or remove binary, gob or text methods or
  `ValidateKanon` on a listed type. The underlying type of a listed type
  other than a struct cannot change either. Each field of `Signature`
  keeps its number, and a new field takes a new number.
- A class that a later version of core adds fails to decode in a
  consumer built with an earlier version. The text form of ADR-0030
  fails the same way.
- An epoch of 2⁶³ or more, and a `Fixed64` whose raw value is 2⁶² or
  more or less than −2⁶², take 11 bytes, 1 more than the opaque form.

**Neutral:**

- The binary forms of ADR-0016 and the text form of ADR-0030 do not
  change. Code that signs or hashes them is unaffected, and JSON and
  `log/slog` still write a class as its name.
- The Go API around a listed type can change before 1.0 when its kanon
  form does not.

## References

- ADR-0016, persisted encodings are frozen before 1.0.
- ADR-0030, a class encodes as its name.
- ADR-0035, core imports the kanon runtime.
- RFC-0045, the kanon forms of core types.
- kanon RFC-0001, the wire format.
- kanon RFC-0002, generated Go codecs, their runtime and their public
  interface.
- kanon ADR-0022, a named type other than a struct with `ValidateKanon`
  encodes as its underlying type.
