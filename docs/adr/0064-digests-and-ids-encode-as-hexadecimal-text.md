---
adr: 0064
title: Digests and IDs Encode as Their Hexadecimal Text
status: Accepted
date: 2026-10-16
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0064: Digests and IDs Encode as Their Hexadecimal Text

## Status

Accepted

## Context

`crypto.Digest` and `id.ID` had binary methods and no text methods, and
their fields are unexported. With Go 1.27.2, `encoding/json` and
`log/slog` treated them as follows:

- `json.Marshal` wrote a field of either type as `{}` and returned no
  error.
- `json.Unmarshal` of `{}` left the field unchanged and returned no error.
- `json.Marshal` of a map keyed by either type failed, because the name
  of an object member must be a string.
- The JSON handler of `log/slog` wrote `{}` for either type.

A digest or an identifier in a JSON document lost its bytes without an
error.

The design of the binary forms left the text forms open, because a text
form is a second wire contract and needs a separate stability promise.
`String` already returned the lowercase hexadecimal of a digest, and `id:`
followed by the lowercase hexadecimal of an identifier.

## Decision

We will give `Digest` and `ID` the methods `AppendText`, `MarshalText`
and `UnmarshalText` over the text that `String` returns, decode only that
text, and refuse the zero `Digest` with `ErrDigestZero`, because a digest
or an identifier in a text format must decode to the value that was
encoded.

## Alternatives Considered

### A JSON method

Each type would declare `MarshalJSON` and `UnmarshalJSON`.

Rejected. `encoding/json` calls the text methods for a value and for the
key of a map. The text methods also serve the other codecs that call
`encoding.TextMarshaler`, such as the text handler of `log/slog`.

### Base64

The text would be the standard base64 of the bytes, the form in which
`encoding/json` writes a `[]byte`.

Rejected. `String` already writes hexadecimal, and two texts of one value
would diverge. A log line that `fmt` writes would not match the JSON
field.

### Hexadecimal without the prefix

An `ID` would encode as the hexadecimal of its bytes alone.

Rejected. The prefix keeps the text of an `ID` apart from the canonical
texts of ULIDs, UUIDs and KSUIDs, which the generator packages format.
The prefix is also part of the text that `String` returns.

### Uppercase digits

`UnmarshalText` would accept the letters A to F, as `encoding/hex` does.

Rejected. A digest and an identifier would then have many texts, and a
document that is decoded and encoded again would change. `errs.Class`
refuses a name in another letter case for the same reason.

### An empty text for the zero `Digest`

The text of the zero `Digest` would be the empty string, which would
decode to the zero `Digest`.

Rejected. The zero `Digest` has no binary form. The text decoder would
then accept an input that the binary decoder refuses. A JSON field that
may contain no digest takes the field option `omitzero`. `encoding/json`
then leaves out the field for the zero `Digest`.

## Consequences

**Positive:**

- `json.Marshal` writes a digest and an identifier as a JSON string, also
  as the name of an object member. `json.Unmarshal` restores the value.
- The JSON handler of `log/slog` writes the same text as `String`.
- Each value has exactly one text, so a JSON document keeps its bytes
  when it is decoded and encoded again.

**Negative:**

- `json.Marshal` returns an error for the zero `Digest` in a field
  without `omitzero`, where it wrote `{}` before.
- The text handler of `log/slog` writes
  `!ERROR:crypto: the zero digest has no encoding` for the zero `Digest`,
  where it wrote an empty string before.
- A text with uppercase digits, such as one that another tool writes,
  does not decode.
- The texts are frozen with the binary forms, so neither text can change.

**Neutral:**

- kanon tries the binary methods of a type before its text methods, so
  the kanon encoding of a field of either type keeps its bytes.
- `ID128`, `ID160` and `ID256` have no text methods, so `encoding/json`
  writes them as arrays of numbers.
- `MarshalText` allocates its result. `AppendText` into a buffer with
  room and `UnmarshalText` do not allocate.

## References

- RFC-0014, binary encoding for core value types, which leaves the text
  forms to future work.
- RFC-0057, fixed-size identifiers.
- ADR-0016, persisted encodings are frozen before 1.0.
- ADR-0030, a class encodes as its name.
- `crypto/digest.go` and `id/text.go`: `AppendText`, `MarshalText` and
  `UnmarshalText`.
- Go 1.27.2, `src/encoding/json/v2/arshal_methods.go`: the methods that
  `encoding/json` calls for a type.
