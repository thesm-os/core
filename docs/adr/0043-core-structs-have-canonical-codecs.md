---
adr: 0043
title: Core Structs Have Canonical kanon Codecs
status: Accepted
date: 2026-10-01
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0043: Core Structs Have Canonical kanon Codecs

## Status

Accepted

## Context

kanon's `-canonical` flag makes the decode of the struct types of a
directive accept only their canonical encoding, the bytes that the encode
writes for the decoded value. Any other input that the wire format lets a
decoder accept fails with an error that wraps `kanon.ErrNotCanonical`:
fields out of order, a field at its zero value, a varint longer than its
shortest form, or a field number that the schema does not list. A format
whose records are hashed or signed needs the flag. Without it, one value
has many encodings, and each encoding has another hash.

kanon fails the generation of a canonical type that contains a struct
whose codec decodes without the flag. A consumer's canonical record can
contain `sign.Signature` only when core generates its codec with the
flag.

`tag.Tag` has no codec. In a consumer's record it encodes inline, with
the field numbers that the consumer's generated file assigns in
declaration order. RFC-0045 found the same problem for `Signature`: when
core adds a field, two consumers number the struct differently, and no
test of core pins the bytes. ADR-0036 freezes the kanon forms of the core
types in its table, and the table does not list `Tag`.

`epoch.Epoch`, `fixed.Fixed64` and `errs.Class` encode through a
generated `ValidateKanon` method, and none of them is a struct. kanon
fails `-canonical` on a directive whose types are not structs. A
canonical record checks the encoding of a field of such a type itself.

## Decision

We will generate the kanon codec of each core struct that a record can
contain, `sign.Signature` and `tag.Tag`, with the `-canonical` flag,
because a consumer's canonical record can contain a struct codec only
when the codec is canonical:

- The directive of `Signature` sets `-canonical`. Its kanon form does not
  change.
- `Tag` has a generated codec with the fields `Key` 1 and `Value` 2, so
  core fixes the numbers of its fields, as it does for `Signature`. This
  kanon form joins the table of ADR-0036, frozen from the release that
  first contains it.
- `DecodeKanon`, `MergeKanon` and `UnmarshalBinary` of both types return
  an error that wraps `kanon.ErrNotCanonical` for input that is not the
  canonical encoding of its value.
- A test of each type compares its encoding with recorded bytes, and
  decodes the recorded fields in another order to
  `kanon.ErrNotCanonical`.

## Alternatives Considered

### Keep the codec of Signature lenient

Rejected. kanon then fails the generation of every canonical record of a
consumer that contains a signature, with "decodes without the -canonical
flag, which a canonical type cannot contain".

### Leave Tag without a codec

A consumer's canonical record would encode a tag inline and check its
encoding in the consumer's own generated code.

Rejected. Each consumer would number `Key` and `Value` itself, and a
field that core adds would renumber the tags of a consumer that
regenerates. No test in core could pin the bytes of a tag, which
ADR-0016 requires for a persisted layout.

## Consequences

**Positive:**

- A consumer's canonical record can contain a `Signature` and a `Tag`.
- Every consumer encodes a tag with the numbers that core records. The
  golden file of the conformance suite, the test of recorded bytes and
  CI's check of field numbers fail a change to them.

**Negative:**

- A reader built with an earlier core rejects a field that a later core
  adds to `Signature` or `Tag`. Every reader upgrades before any writer
  sets the field.
- The decode of `Signature` rejects input that it accepted before: fields
  in another order, a field at its zero value, a varint longer than its
  shortest form, and unknown field numbers. Core's encode never wrote
  such input, but another writer can have.
- `*Tag` gains the nine methods of the codec. `encoding/gob` encodes a
  tag through `MarshalBinary`, and fails for a tag in a struct that it
  receives by value with "gob: unaddressable value of type *tag.Tag". A
  struct that embeds `Tag` gains the methods through promotion.

**Neutral:**

- The codecs of `Epoch`, `Fixed64` and `Class` take no flag.
- The encoding of `Signature` does not change. Its recorded bytes and the
  samples of its golden file are the same as before.

## References

- ADR-0016, persisted encodings are frozen before 1.0.
- ADR-0035, core imports the kanon runtime.
- ADR-0036, core types have frozen kanon forms.
- RFC-0045, the kanon forms of core types.
- kanon RFC-0006, decoding that accepts only the canonical encoding.
- kanon at f6f835e, `internal/kanon/generate.go`, which lists the
  conditions under which the generation fails.
