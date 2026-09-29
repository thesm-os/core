---
rfc: 0045
title: kanon Forms of Core Types
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-09-29
updated: 2026-09-29
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0036
---

# RFC-0045: kanon Forms of Core Types

## Summary

We propose that core generates the kanon code of the core types that a
kanon record can contain, and freezes the form of each:

- `epoch.Epoch`, `fixed.Fixed64` and `errs.Class` get a generated
  `ValidateKanon`, so a record encodes each of them as its integer. An
  epoch of 7 then takes 2 bytes instead of 10, and a class 2 bytes
  instead of 8 to 13.
- `sign.Signature` gets a generated codec, so every consumer encodes its
  fields with the numbers that core records: `Algorithm` 1, `Value` 2
  and `KeyID` 3.
- `crypto.Digest`, `id.ID` and `clock.Instant` keep their binary forms
  in a record, and declare `SizeKanon`, so kanon writes each of them
  once. `id.ID.IsZero` reads only the size byte, as
  `crypto.Digest.IsZero` does.
- The forms of these types, and of `crypto.Algorithm` and `sign.KeyID`,
  are frozen.

The design depends on ADR-0035, which lets core import the kanon
runtime.

## Motivation

### An opaque form is up to 11 bytes longer than an integer

kanon encodes a type with its own binary, gob or text methods as an
opaque byte string through those methods. `Epoch` and `Fixed64` have
8-byte binary forms, and `Class` encodes as its name. We measured one
field of each in a consumer's record, with core at d758b6e and kanon at
4751eb5 and at cb05b4d, which write the same bytes:

| Field | Opaque form | Integer form |
|---|---|---|
| `Epoch` 7 | 10 bytes | 2 bytes |
| `Epoch` 2³² | 10 bytes | 6 bytes |
| `Fixed64` 12.34 | 10 bytes | 6 bytes |
| `Class` `Transient` | 11 bytes | 2 bytes |
| `Class` `Integrity` | 11 bytes | 2 bytes |

We also measured a record with one field of each of the three types,
with kanon at cb05b4d, Go 1.27.1 on an AMD Ryzen 9 9950X3D and one P.
Each figure is the median of 10 runs:

| Operation | Opaque forms | Integer forms |
|---|---|---|
| Encode | 14.5 ns | 6.4 ns |
| Decode into a reused record | 9.7 ns | 8.0 ns |

The benchmarks report 0 allocations for both forms.

### Only core can choose the form of a core type

kanon encodes a named type other than a struct as its underlying type
when the type has the method `ValidateKanon() error`, ahead of its
binary, gob and text methods (kanon ADR-0022). Go allows the method only
in the package that declares the type, so a consumer cannot choose the
integer form of `Epoch`.

Adding `ValidateKanon` to a type with its own methods changes the
encoding of every field of the type. kanon's RFC-0001 lists the change
as incompatible in both directions, and kanon's check of field numbers
does not detect it, because the numbers do not change. Core has to
choose the form of a type before consumers persist it. This RFC chooses
the forms of `Fixed64` and `Class` for that reason.

### Each consumer numbers the fields of Signature itself

In a consumer's record, a struct of another package encodes inline, with
field numbers that the consumer's generated file assigns in declaration
order and records. When core inserts a field into `Signature`, a
consumer that first generates after the change numbers the struct
differently from one that generated before it. A signature that one of
them writes then decodes into the wrong fields in the other.
`Algorithm` and `Value` are both byte strings, so a swap of their
numbers decodes without an error.

A codec that core generates records one numbering in core, and every
consumer's record calls it.

### kanon encodes an opaque value twice

kanon's generated code sizes an opaque value by encoding it, and then
encodes it again to write it, unless the type declares
`SizeKanon() int` (kanon ADR-0024). It tests whether the value is
present with `IsZero() bool` when the type declares it, and otherwise
compares the whole value with its zero value. `crypto.Digest`, `id.ID`
and `clock.Instant` are the opaque types of core that records contain,
and only core can declare methods on them.

## Detailed design

### Components

| Component | Change |
|---|---|
| `epoch/binary.go` | The directive `//go:generate go tool kanon -type=Epoch` |
| `fixed/binary.go` | The directive `-type=Fixed64 -validate=valid` |
| `fixed/fixed.go`, `fixed/text.go` | The method `valid`, which `FromRaw`, `AppendBinary` and `AppendText` call |
| `errs/class.go` | The directive `-type=Class -validate=valid`, and the method `valid`, which `AppendText` calls |
| `crypto/sign/signature.go` | `Signature` moves here from `policy.go`, with the directive `-type=Signature` |
| `crypto/digest.go`, `id/binary.go`, `clock/instant.go` | `SizeKanon` on `Digest`, `ID` and `Instant` |
| `id/id.go` | `ID.IsZero` reads the size byte |
| `*.kanon.go`, `*.kanon_test.go` | The generated methods and their conformance tests, one pair per directive |
| `testdata/golden/` | The golden files of the conformance tests, one per generated type |
| `go.mod` | kanon cb05b4d as a requirement, and a `tool` directive for `go.thesmos.sh/kanon/cmd/kanon` |
| `.golangci.yml` | The packages of ADR-0035 in the `depguard` allow-lists |
| `.ergon.yaml` | The generated files in `license.exclude_files` and `checks.excludes` |
| `Makefile`, CI | The targets `check-generated` and `check-numbers`, and a CI job for each |

### The forms

| Type | Go type | kanon form | Source of the form |
|---|---|---|---|
| `epoch.Epoch` | `uint64` | varint, every value | Generated `ValidateKanon` |
| `fixed.Fixed64` | `int64` | zigzag varint of the raw value, without `math.MinInt64` | Generated `ValidateKanon`, which calls `valid` |
| `errs.Class` | `uint8` | varint, `Unspecified` 0 to `Integrity` 7 | Generated `ValidateKanon`, which calls `valid` |
| `sign.Signature` | struct | message: `Algorithm` 1, `Value` 2, `KeyID` 3 | Generated codec |
| `crypto.Digest` | struct | bytes: the binary form of ADR-0016 | The type's binary methods and `SizeKanon` |
| `id.ID` | struct | bytes: the binary form of ADR-0016 | The type's binary methods and `SizeKanon` |
| `clock.Instant` | struct | bytes: the 16-byte binary form of ADR-0016 | The type's binary methods and `SizeKanon` |
| `crypto.Algorithm` | `string` | bytes: the name | The underlying type |
| `sign.KeyID` | `[16]byte` | bytes: the 16 bytes | The underlying type |

A record leaves out the zero value of each of these types, such as epoch
0, `Unspecified`, an empty `Signature` and the zero digest.

`Epoch` and `Fixed64` keep their 8-byte binary forms, and `Class` keeps
its name as its text form. Code that signs or hashes those forms does
not change, and JSON and `log/slog` still write a class as its name.
Only a kanon record uses the integer forms.

The integer forms are never longer than the opaque forms, apart from two
ranges, whose values take 11 bytes:

- An epoch of 2⁶³ or more.
- A `Fixed64` whose raw value is 2⁶² or more, or less than −2⁶², which
  is a magnitude of about 4.6 × 10¹⁰ units.

### One domain check per type

Every method of the type that checks the domain calls `valid`, and the
generated `ValidateKanon` returns its error, so every form of the type
accepts the same values: the binary, text and kanon forms of a
`Fixed64`, and the text and kanon forms of a `Class`. RFC-0014 applied
the same rule to the decode paths of `Digest`.

```go
// In package fixed.

// valid returns ErrRange for math.MinInt64, the one int64 outside the
// domain, and nil for every other value. FromRaw, AppendBinary,
// AppendText and the generated ValidateKanon call it.
func (f Fixed64) valid() error {
    if f == outOfDomain {
        return ErrRange
    }

    return nil
}

// In package errs.

// valid returns ErrUnknownClass for a value above Integrity, and nil for
// each of the eight classes. AppendText and the generated ValidateKanon
// call it.
func (c Class) valid() error {
    if c > Integrity {
        return ErrUnknownClass
    }

    return nil
}
```

The generated `ValidateKanon` of `Epoch` returns nil, because every
`uint64` is an epoch.

### Opaque types

`crypto.Digest`, `id.ID` and `clock.Instant` keep their binary forms in
a record. `SizeKanon` and `IsZero` let kanon write each of them once:

- `SizeKanon() int` returns the length of the binary form: the size
  byte of a `Digest` or an `ID`, and 16 for an `Instant`. kanon sizes
  the field with it and appends the binary form into that room.
- `IsZero() bool` reports the zero value, which a record leaves out.
  `crypto.Digest.IsZero` and `id.ID.IsZero` read the size byte, because
  the constructors are the only code that sets a size, and no value but
  the zero value has size 0. `clock.Instant.IsZero` compares its three
  fields.

We measured a record with a SHA-256 digest, a 128-bit ID and an instant,
with kanon at cb05b4d. Each figure is the median of 10 runs:

| Operation | Core at d758b6e | With `SizeKanon` and `IsZero` |
|---|---|---|
| Encode | 20.6 ns | 10.5 ns |
| Decode into a reused record | 22.0 ns | 21.5 ns |

The record has 70 bytes in both, and the bytes are identical. Neither
allocates.

### Failure handling

| Condition | Operation | Error |
|---|---|---|
| A `Fixed64` field is `math.MinInt64` | Encode | `*kanon.EncodeError` that wraps `fixed.ErrRange` |
| A decoded `Fixed64` is `math.MinInt64` | Decode | `*kanon.DecodeError` at the offset of the value, which wraps `fixed.ErrRange` |
| A `Class` field is above `Integrity` | Encode | `*kanon.EncodeError` that wraps `errs.ErrUnknownClass` |
| A decoded `Class` is above `Integrity`, such as a class that a later core adds | Decode | `*kanon.DecodeError` that wraps `errs.ErrUnknownClass` |
| A decoded `KeyID` is not 16 bytes long | Decode | `*kanon.DecodeError` |
| The binary form of a `Digest`, `ID` or `Instant` differs in length from its `SizeKanon` | Encode | `*kanon.EncodeError` that wraps `kanon.ErrSize` |

Both sentinels of core classify as `errs.Invalid`. The tests of
`SizeKanon` pin its result to the length of the binary form, so the last
row needs a change to a frozen binary form.

### The codec of Signature

The generated codec adds nine methods to `*Signature`, with the
contracts of `kanon.Message` and `kanon.Cloner`: `SizeKanon`,
`EncodeKanon`, `AppendBinary`, `MarshalBinary`, `UnmarshalBinary`,
`DecodeKanon`, `MergeKanon`, `Reset` and `CloneKanon`. JSON does not
change.

`encoding/gob` then encodes a `Signature` through `MarshalBinary`.
Because the method has a pointer receiver, gob fails for a `Signature`
in a value that is not addressable, such as a struct passed to `Encode`
by value: `gob: unaddressable value of type *sign.Signature`. A pointer
to the struct, and a slice of signatures, encode. Against core at
d758b6e, the same struct encodes by value.

A consumer whose file records the numbers 1, 2 and 3 for `Signature`, the
declaration order, writes the same bytes after it regenerates. We
measured a signature with an Ed25519 algorithm name, a 64-byte value and
a key ID, with kanon at cb05b4d. Each figure is the median of 10 runs:

| Measure | Inline in the consumer | Core's codec |
|---|---|---|
| Bytes | 95 | 95, identical |
| Encode | 10.7 ns | 11.8 ns |
| Decode into a reused record | 12.2 ns | 12.8 ns |

`Signature` moves to `signature.go`. Its directive then generates
`signature.kanon.go`, a name that matches the type.

### Tests

| # | Guarantee |
|---|---|
| 1 | `kanontest.RunValue` checks `Epoch`, `Fixed64` and `Class`. `ValidateKanon` allocates nothing for a value that it accepts. It accepts a value of the underlying type's value tables exactly when the type's binary or text method accepts it. A golden file pins the encoding of each value |
| 2 | `kanontest.Run` checks `Signature`: a golden file of its samples and probes, the reference encoder and decoder, a fuzz target of the decode, and the allocation contract |
| 3 | A table in `errs` pins the number of each of the eight classes, because the value tables of `uint8` contain only 0, 1 and 2 of them |
| 4 | A test in `crypto/sign` compares the encoding of a `Signature` whose fields have fixed values with recorded bytes |
| 5 | The tests of `AppendBinary`, `FromRaw` and `AppendText`, which call `valid`, fail when `valid` accepts `math.MinInt64` or rejects `Integrity` |
| 6 | `SizeKanon` of `Digest`, `ID` and `Instant` equals the length of the binary form, for a value of each size and for the zero value |
| 7 | `ID.IsZero` reports false for an ID of 16 zero bytes, and `BenchmarkIsZero` measures it |
| 8 | `check-numbers` fails a change that renumbers a field of `Signature` against the base branch |
| 9 | `check-generated` fails when a generated file differs from the output of `go generate` |

The generated tests of the four types pass. `golangci-lint` reports 0
issues over the module.

### Gates

- `license.exclude_files` lists `*.kanon.go` and `*.kanon_test.go`.
  Without the entries, `ergon lint license` fails on every generated
  file. The generator does not write an SPDX header.
- `checks.excludes` lists `*.kanon.go`. kanon's own repository excludes
  the files for the same reason: kanon's golden tests compare the
  generator's output with recorded files, and the generated tests run
  the conformance suite over that output.
- `golangci-lint` skips the generated files through
  `exclusions.generated: lax`.
- Both gates still measure `valid`. Test 5 kills the mutants of its two
  conditions.

### Compatibility and rollout

Core requires kanon's commit cb05b4d as a pseudo-version (ADR-0035).
kanon has no release yet. After core releases the generated code and
ADR-0036, each consumer upgrades core and regenerates its code. A
consumer's records change as follows:

- A field of type `Epoch`, `Fixed64` or `Class` changes from the opaque
  form to the integer form. Records written before the consumer
  regenerates do not decode in the new code, and records written by the
  new code do not decode in the old code. The consumer migrates such
  records before it upgrades.
- A field of type `Signature` keeps its bytes when the consumer's code
  file records `Algorithm=1 Value=2 KeyID=3` for
  `go.thesmos.sh/core/crypto/sign.Signature`. kanon's check of field
  numbers compares that record with the numbers of core's codec, and
  fails a consumer that recorded `Algorithm=2 Value=1` with
  `field Algorithm of go.thesmos.sh/core/crypto/sign.Signature has number 1, and the base revision gives it number 2`.
- A field of type `Digest`, `ID` or `Instant` keeps its bytes.
  `SizeKanon` and `IsZero` change only how kanon writes them.
- Fields of the other types in the table keep their bytes.

## Alternatives considered

### A. Keep the opaque forms

Core generates nothing, and records keep the binary forms of `Epoch` and
`Fixed64` and the name of a class.

**Why not:** an epoch of 7 takes 8 bytes more in a record, and a class 6
to 11 bytes more. The record also encodes and decodes slower. A later
move to the integer forms would break every record that contains the
types.

### B. The name as the kanon form of a class

A class would keep the opaque form, its name through the text methods of
ADR-0030.

**Why not:** ADR-0030 chose the name for text formats that people read,
such as JSON and log lines. Code reads a kanon record. The number takes
2 bytes against 8 to 13 for the name. `errs/class.go` already requires a
new class to take the number after the last one, so the numbers do not
change any more than the names do. Both forms fail to decode a class
that a later core adds.

### C. Struct tags on the fields of Signature

Core would fix the numbers with `kanon:"1"`, `kanon:"2"` and `kanon:"3"`
tags, without a codec. Consumers would keep the inline encoding.
`Signature` would not gain methods, `encoding/gob` would still encode it
by value, and each signature would take about 1 ns less. kanon fails the
generation of a consumer whose recorded numbers differ from a tag, with
`the tag numbers the field 1 but it is recorded as 2`.

**Why not:** after core adds a field to `Signature`, a consumer that
upgrades core and does not regenerate drops the new field when it
encodes, because its generated file encodes the fields itself. We added
a field to `Signature` in a copy of core, and the conformance test of a
consumer with stale generated code still passed. A test in core could
compare the encoding of `Signature` with recorded bytes, as ADR-0016
requires, only through a record type that core declares for that test
alone, because the conformance suite checks only a struct with a codec.
The codec keeps both the encoding and its test in core.

### D. Codecs for Digest, ID and Instant

**Why not:** a generated `kanon.Message` would declare `AppendBinary`,
`MarshalBinary` and `UnmarshalBinary`, and the three types already have
binary methods that ADR-0016 freezes. kanon also rejects `ValidateKanon`
on a struct type. The opaque forms are small already. A SHA-256 digest
takes 34 bytes with its field header. An instant takes 18 bytes,
against 12 to 24 bytes as a message, a range calculated from its three
fields.

### E. ValidateKanon on Algorithm and KeyID

**Why not now:** both types already encode as their underlying types,
because neither has codec methods. `ValidateKanon` would add only a
domain check, and neither type restricts its underlying type.
`Algorithm` is an open vocabulary of names, and any 16 bytes form a key
ID. Core can add the method later without a change to the encoding.

### F. A SizeKanon that kanon generates

**Why not:** kanon knows the length of a type's own binary form only by
encoding the value, which is the cost that `SizeKanon` removes. The
type knows the length, and each method is one line.

## Drawbacks

- `*Signature` gains nine exported methods, and `encoding/gob` encodes a
  `Signature` through kanon's bytes. gob fails for a `Signature` in a
  value that is not addressable.
- The codec of `Signature` adds about 1 ns to each encode and decode of
  a signature, against the inline code of a consumer.
- A consumer's records with the opaque forms of `Epoch`, `Fixed64` or
  `Class` do not decode after it regenerates.
- An epoch of 2⁶³ or more, and a `Fixed64` whose raw value is 2⁶² or
  more or less than −2⁶², take 11 bytes, 1 more than the opaque form.
- A class that a later core adds fails to decode in a consumer built
  with an earlier core, as its text form does.
- `Digest`, `ID` and `Instant` declare a method for kanon, whose result
  must follow any change of their binary forms.
- The generated files are outside core's coverage and mutation gates.
- The fuzz target and the benchmark of `Signature` add to the run time
  of `ergon test fuzz` and `ergon bench`.

## Open questions

None.

## Unresolved / future work

- The kanon forms of other core types, such as `crypto.Role` and
  `version.Version`, once a consumer persists them. Each enters the table
  of ADR-0036 through an ADR.

## References

- kanon RFC-0001, the wire format, and its list of changes that are
  incompatible in both directions.
- kanon RFC-0002, generated Go codecs.
- kanon ADR-0022, a named type other than a struct with `ValidateKanon`
  encodes as its underlying type.
- kanon ADR-0024, an opaque type sizes a value with `SizeKanon` and
  tests its presence with `IsZero`.
- ADR-0015, dependency-free golang.org/x modules in production code.
- ADR-0016, persisted encodings are frozen before 1.0.
- ADR-0030, a class encodes as its name.
- ADR-0035, core imports the kanon runtime.
- ADR-0036, core types have frozen kanon forms.
- RFC-0014, binary encoding for core value types.
