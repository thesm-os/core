---
rfc: 0073
title: ULID, KSUID and UUID Value Types
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-17
updated: 2026-10-17
discussion: none
supersedes: none
superseded-by: none
produces-adr: tbd
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0073: ULID, KSUID and UUID Value Types

## Summary

We propose a value type in each identifier package: `ulid.ULID`,
`ksuid.KSUID`, `uuidv4.UUID` and `uuidv7.UUID`. Each type is an array
of the bytes of its identifier, with the text, binary and kanon
methods of its scheme. `String` and `MarshalText` write the canonical
text that `Format` returns. The zero value means "no identifier" and
encodes as no bytes, as `id.Zero` does. The two UUID types also have
`Valid`, which reports whether a UUID has the version and the variant of
its package. Their codecs accept a UUID of any version. A consumer
embeds the type in a struct of its own, and the struct then keys a map
by the bytes of the identifier. Its promoted methods give it the text of
its scheme and its forms in kanon, JSON and gob.

## Motivation

### A consumer writes the codec of its identifier type

`id.ID128` and `id.ID160` key a map by 16 and 20 bytes. They have
`ID`, `Compare` and `String`, and no codec methods. Their `String`
writes the `id:` text of `id.ID`. A consumer that keys its maps by
ULIDs and writes them as ULID text declares a type of its own:

```go
type ShardID struct{ id.ID128 }
```

The struct encodes as its identifier only after it declares these
methods:

- `AppendBinary`, `MarshalBinary`, `UnmarshalBinary`, `SizeKanon`,
  `ExactKanon` and `AppendKanon`. Without them kanon encodes the
  struct as an inline struct with one field.
- `AppendText`, `MarshalText` and `UnmarshalText`. The promoted
  `String` writes the `id:` text, not the ULID text.
- `IsZero`. The zero `id.ID128` is the identifier of 16 zero bytes,
  and the type has no value for "no shard".

One such type has 10 methods in about 140 lines with their
documentation, before its tests. Each consumer of ULIDs, KSUIDs or
UUIDs writes them again, and decides the zero value, the text and the
bytes on its own.

### A fixed-size type encodes as numbers in JSON

With Go 1.27.2, `json.Marshal` writes a field of type `id.ID128` as an
array of 16 numbers, such as `[1,2,0,0,…]`. A map keyed by `id.ID128`
does not encode. `json.Marshal` returns the error "object member name
must be a string". `encoding/json` encodes a value and the key of a
map through the text methods of their type. `id.ID128` has none.

### Text methods on a fixed-size type would change the bytes of kanon records

kanon resolves a named type in this order: a nested struct,
`time.Time`, a validator, a type that encodes itself through its
methods, and last its underlying type. Among the methods it tries the
binary methods first, then `GobEncode`, then the text methods. A field
of type `id.ID128` encodes today as an array of 16 bytes. With text
methods, kanon would write the field as its `id:` text. Every record
with such a field would change its bytes, and persisted encodings are
frozen.

A fixed-size type has no scheme, so its text method can only write the
`id:` text. The consumer above keeps its own text methods either way.

### Prior art

- oklog/ulid declares `ULID` as an array of 16 bytes. It has
  `MarshalText`, `UnmarshalText`, `MarshalBinary`, `UnmarshalBinary`,
  `Compare`, `IsZero` and the variable `Zero`.
- google/uuid declares `UUID` as an array of 16 bytes. It has the same
  four codec methods and the variable `Nil`.
- segmentio/ksuid declares `KSUID` as an array of 20 bytes. It has the
  same four codec methods, `IsNil` and the variable `Nil`.

All three write their zero value as its all-zero text and as its 16 or
20 zero bytes.

## Detailed design

### Components

| Component | Responsibility |
|---|---|
| `ulid.ULID` | A ULID as its 16 bytes, with the text of the ULID specification |
| `ksuid.KSUID` | A KSUID as its 20 bytes, with its base62 text |
| `uuidv4.UUID`, `uuidv7.UUID` | A UUID of one version as its 16 bytes, with the text of RFC 9562 and a check of the version |
| `FromID` of each package | The value type of an `id.ID` of the size of the scheme |
| `ErrInvalidBytes` of each package | The error of a binary decode of bytes that encode no value |
| `internal/uuidtext` | The text and binary code that both UUID types call |

### API

```go
// ULID is a ULID as an array of its 16 bytes: a timestamp of 48 bits in
// milliseconds, big-endian, then 80 random bits. ULID is comparable, and ==
// compares two ULIDs. The bytes order as the timestamps of the ULIDs.
//
// The zero ULID means no ULID. IsZero reports it. Its binary form is no
// bytes, and its text is 26 zeros.
//
// # Allocation contract
//
// A value type of 16 bytes, passed by value. String and MarshalText allocate
// their result, and so does MarshalBinary for a ULID that is not zero. The
// other methods do not allocate.
type ULID [id.Size128]byte

// FromID returns the ULID with the bytes of i and true for an ID of size
// [id.Size128]. It returns the zero ULID and true for [id.Zero], and the
// zero ULID and false for an ID of another size.
func FromID(i id.ID) (ULID, bool)

// ID returns the ID of size [id.Size128] with the bytes of u, and [id.Zero]
// for the zero ULID.
func (u ULID) ID() id.ID

// IsZero reports whether u is the zero ULID.
func (u ULID) IsZero() bool

// Compare returns -1, 0 or +1 by the order of the bytes of u and other.
func (u ULID) Compare(other ULID) int

// String returns the canonical text of u: 26 characters of Crockford's
// base32 in uppercase, and 26 zeros for the zero ULID.
func (u ULID) String() string

// AppendText appends the text that String returns to dst and returns the
// extended slice. The error is always nil.
func (u ULID) AppendText(dst []byte) ([]byte, error)

// MarshalText returns the text that String returns, in a new slice. The
// error is always nil.
func (u ULID) MarshalText() ([]byte, error)

// UnmarshalText sets u to the ULID of text by the rules of [ParseULID]. It
// accepts both cases of a letter and the substitutions of the ULID
// specification, and decodes 26 zeros to the zero ULID. On an error u is
// unchanged.
//
// It returns [ErrInvalidLength], [ErrInvalidChar] and [ErrInvalidTimestamp]
// as ParseULID does.
func (u *ULID) UnmarshalText(text []byte) error

// AppendBinary appends the binary form of u to dst and returns the extended
// slice. The binary form is the 16 bytes of u, and no bytes for the zero
// ULID. The error is always nil.
func (u ULID) AppendBinary(dst []byte) ([]byte, error)

// MarshalBinary returns the binary form that AppendBinary appends, in a new
// slice. The error is always nil.
func (u ULID) MarshalBinary() ([]byte, error)

// UnmarshalBinary sets u to the ULID of data: the zero ULID for no bytes, and
// the ULID of 16 bytes that are not all zero. On an error u is unchanged.
//
// It returns [ErrInvalidBytes] for data of another length, and for 16 zero
// bytes, because the binary form of the zero ULID is no bytes.
func (u *ULID) UnmarshalBinary(data []byte) error

// SizeKanon returns the length of the binary form of u: 16, and 0 for the
// zero ULID.
func (u ULID) SizeKanon() int

// ExactKanon declares ULID a kanon.Exact type. No code calls it.
func (ULID) ExactKanon()

// AppendKanon appends the binary form of u to dst and returns the extended
// slice.
func (u ULID) AppendKanon(dst []byte) []byte
```

The other three types have the same function and methods at their
size:

| Package | Type | Bytes | Text | Text of the zero value | Decoder of the text |
|---|---|---|---|---|---|
| `ulid` | `ULID` | 16 | 26 characters of Crockford's base32, in uppercase | 26 zeros | `ParseULID` accepts both cases and the substitutions I, L and O |
| `ksuid` | `KSUID` | 20 | 27 characters of base62 | 27 zeros | `Parse` is case-sensitive |
| `uuidv4` | `UUID` | 16 | 36 characters with 4 hyphens, in lowercase | `00000000-0000-0000-0000-000000000000` | `Parse` accepts both cases |
| `uuidv7` | `UUID` | 16 | 36 characters with 4 hyphens, in lowercase | `00000000-0000-0000-0000-000000000000` | `Parse` accepts both cases |

The two UUID types also declare `Valid`. RFC 9562 fixes 6 of the 128
bits of a UUIDv4 or a UUIDv7, so 63 of every 64 values of the array are
not a UUID of the version of the package:

```go
// Valid reports whether u is a UUIDv4: version 4 in the high 4 bits of byte 6
// and the variant of RFC 9562 in the high 2 bits of byte 8. It reports false
// for the zero UUID, because the Nil UUID has neither.
func (u UUID) Valid() bool
```

`uuidv7.UUID.Valid` reports version 7. It returns what `uuidv7.Valid`
returns for the ID of the UUID. `FromID` and the codecs do not check the
version or the variant, as `Parse` does not. `ULID` and `KSUID` have no
`Valid`, because every 16 bytes are a ULID and every 20 bytes are a
KSUID. Only their texts can encode no value, and their parsers refuse
those texts with `ErrInvalidTimestamp` and `ErrOverflow`.

Each package declares one more sentinel in its `errors.go`:

```go
// ErrInvalidBytes is returned by [ULID.UnmarshalBinary] for data that is not
// the binary form of a ULID: data whose length is not 0 or 16, and 16 zero
// bytes. It classifies as [go.thesmos.sh/core/errs.Invalid].
var ErrInvalidBytes = errs.WithClass(
	errors.New("ulid: invalid bytes, want none or 16 that are not all zero"), errs.Invalid,
)
```

The parser of each package runs over a string or a byte slice through
a type parameter. `UnmarshalText` then decodes its bytes without
converting them to a string. A conversion would allocate for the 36
bytes of a UUID, because the runtime converts only up to 32 bytes in a
buffer on the stack.

A consumer embeds the type in a struct:

```go
// ShardID identifies a shard. The zero ShardID means no shard.
type ShardID struct{ ulid.ULID }

u, ok := ulid.FromID(gen.Generate())
if !ok || u.IsZero() {
	return ErrShardID
}
shards[ShardID{u}] = s
```

`ShardID` gets every method of `ulid.ULID` through promotion. kanon
finds the binary methods in the method set of `*ShardID`, so a field of
type `ShardID` encodes as the 16 bytes of its ULID.

### Invariants

- `FromID(u.ID())` returns `u` and true for every value `u`.
- `FromID(i)` reports true exactly when `i.Size()` is the size of the
  scheme or `i` is `id.Zero`.
- `UnmarshalText` of `u.String()` sets `u`, and `UnmarshalBinary` of
  `u.MarshalBinary()` sets `u`, for every value `u`.
- `u.AppendBinary(nil)` equals `u.ID().AppendBinary(nil)`. A field that
  changes from `id.ID` to a value type keeps the bytes of its records
  when it contains identifiers of the scheme or `id.Zero`.
- `u.SizeKanon()` equals the length of `u.AppendKanon(nil)`.
- `a.Compare(b)` equals `bytes.Compare(a[:], b[:])`.
- `u.String()` equals the text that `Format` returns for the ID of the
  bytes of `u`.
- `u.Valid()` of a UUID type reports true exactly when the high 4 bits of
  byte 6 are the version of the package and the high 2 bits of byte 8
  are `10`. It reports true for every UUID that the generator of the
  package returns.

### Encodings

| Codec | Form of a value | Form of the zero value |
|---|---|---|
| kanon | Its bytes, through `AppendKanon` | No bytes. kanon leaves out a field when `IsZero` reports true |
| encoding/json | A JSON string of the text, also as the name of an object member | The all-zero text. The option `omitzero` leaves out the field |
| encoding/gob | The bytes of `MarshalBinary` | No bytes |
| fmt and log/slog | The text that `String` returns | The all-zero text |

kanon writes a value of the type through `AppendKanon` in every
position without an error path, because the type is a
`kanon.Appender`. A canonical decode refuses 16 zero bytes, because
`UnmarshalBinary` refuses them.

### The zero value

The zero value of each type means "no identifier". RFC 9562 defines
the Nil UUID for this use, with all 128 bits set to zero. The prior art
declares `Nil` for KSUIDs and `Zero` for ULIDs. A UUID generator of
core never returns the Nil UUID, because it sets the version bits. A
ULID or KSUID generator returns the zero value only for a clock at the
epoch of the scheme and a source of zeros.

The binary form of the zero value is no bytes, as for `id.Zero`. Its
text is the all-zero text of the scheme. RFC 9562 and the prior art
write the same text, so the text of a nil value from another system
decodes.

### Bounds

| Quantity | Bound |
|---|---|
| Size of `ULID` and `UUID` | 16 bytes, against 33 for `id.ID` |
| Size of `KSUID` | 20 bytes |
| Text | 26 bytes for a ULID, 27 for a KSUID, 36 for a UUID |
| Binary form | 16 or 20 bytes, and none for the zero value |
| Work of `FromID` and `ID` | One comparison of the size and a copy of the bytes |

### Failure handling

| Condition | Function | Result |
|---|---|---|
| An `id.ID` of another size | `FromID` | The zero value and false |
| A text that the parser of the scheme refuses | `UnmarshalText` | The sentinel of the parser, and the receiver unchanged |
| The empty text | `UnmarshalText` | The length sentinel of the parser, such as `ErrInvalidLength`, and the receiver unchanged |
| Bytes of another length, or all-zero bytes | `UnmarshalBinary` | `ErrInvalidBytes`, and the receiver unchanged |
| A UUID of another version or variant | `FromID`, `UnmarshalText` and `UnmarshalBinary` of a UUID type | The UUID. `Valid` reports false for it |

No function panics. Every sentinel classifies as `errs.Invalid`.

### Allocation contract

| Call | Objects |
|---|---|
| `FromID`, `ID`, `IsZero`, `Valid`, `Compare` and `SizeKanon` | 0 |
| `AppendText`, `AppendBinary` and `AppendKanon` into a buffer with room | 0 |
| `UnmarshalText` and `UnmarshalBinary` | 0 |
| `String` and `MarshalText` | 1, the result |
| `MarshalBinary` | 1, the result, and 0 for the zero value |

### Tests

| # | Guarantee |
|---|---|
| 1 | Over generated identifiers of each size, `FromID` and `ID` are inverses, and `FromID` reports false for every other size |
| 2 | Over generated values, the text and the binary forms round-trip, and `String` equals the text of `Format` for the same bytes |
| 3 | `UnmarshalText` accepts every text that the parser of the scheme accepts, and returns the sentinel of the parser for every other text |
| 4 | `UnmarshalBinary` returns `ErrInvalidBytes` for each length other than 0 and the size, and for the all-zero bytes |
| 5 | `Valid` of a UUID type reports true for every UUID that the generator returns, and false for the zero value and for each other version and variant |
| 6 | The binary form of each value equals the binary form of its `id.ID`, and the zero value encodes as no bytes |
| 7 | kanon writes a field of each type as the bytes of an `id.ID` field with the same identifier |
| 8 | `encoding/json` writes and reads a field and a map key of each type, and `omitzero` leaves out the zero value |
| 9 | The `Allocs` tests and the benchmarks state the counts of the allocation contract |
| 10 | The suites cover every statement, and dokimi-mutate-go kills every mutant of the new code |

### Migration

A consumer type that embeds `id.ID128` or `id.ID160` and declares the
methods above can embed the value type of its scheme instead.

- Its kanon records keep their bytes when its binary form is the bytes
  of the identifier, and no bytes for its zero value.
- Its JSON changes when it writes another text for its zero value, such
  as the empty text.
- A type without binary methods encodes in kanon as an inline struct.
  Its records change their bytes when it embeds a value type, so the
  consumer migrates its records first.

## Alternatives considered

### A. Text methods on the fixed-size types

`id.ID128`, `id.ID160` and `id.ID256` would declare `AppendText`,
`MarshalText` and `UnmarshalText` over the `id:` text of `id.ID`.

**Why not:** kanon resolves a type through its own methods before its
underlying type, and uses the text methods of a type without binary
methods. Every record with a field of a fixed-size type would change
from the 16, 20 or 32 bytes to the `id:` text, and persisted encodings
are frozen. The `id:` text is also not the text of a ULID, a KSUID or
a UUID, so a consumer of those schemes keeps its own text methods. Only
the JSON form improves.

### B. Binary and text methods on the fixed-size types

The fixed-size types would also declare the binary methods of `id.ID`,
so that kanon keeps their bytes.

**Why not:** kanon would encode every field of a fixed-size type
through the methods instead of as a byte array. gob would change its
form from an array to the bytes of `MarshalBinary`. The text remains
the `id:` text. The fixed-size types still have no value for "no
identifier", because their zero value is the identifier of zero bytes.

### C. Zero bytes for the zero value

The binary form of the zero value would be its 16 or 20 zero bytes, as
in oklog/ulid, google/uuid and segmentio/ksuid.

**Why not:** `id.Zero` encodes as no bytes. A field that changes from
`id.ID` to a value type keeps its bytes only when the zero value also
encodes as no bytes. The binary form of the zero value then differs
from the prior art in gob and in `MarshalBinary` alone.

### D. The empty text for the zero value

The text of the zero value would be the empty string to match its
binary form of no bytes. A decode would refuse the all-zero text.

**Why not:** RFC 9562 defines the Nil UUID and its text, and the prior
art writes and reads the all-zero text. A decoder that refuses it
refuses valid input from other systems. A JSON field that may contain
no identifier takes the option `omitzero`, which reads `IsZero`.

### E. A struct that embeds the fixed-size type

`type ULID struct{ id.ID128 }` would promote `ID`, `Compare` and
`String` of `id.ID128`.

**Why not:** the promoted `String` writes the `id:` text and the
promoted `Compare` takes an `id.ID128`. The type would declare both
methods again. The array form matches the prior art and the fixed-size
types.
A caller converts it to `id.ID128` and back with a type conversion.

### F. One UUID type for every version

A new package `id/uuid` would declare one `UUID` type for every
version, as google/uuid does.

**Why not:** core keeps one package per UUID version, each with its
`Format`, `AppendFormat`, `Parse` and generator. A type in each version
package keeps that structure. `internal/uuidtext` keeps one copy of the
code that both types call. A consumer that stores UUIDs of more than
one version keeps `id.ID`.

### G. A type in each consumer

Each consumer would keep declaring its own type, as it does today.

**Why not:** every consumer writes the same 10 methods, their tests
and their mutation runs. Consumers of one scheme can also disagree on
the text and the bytes of the same identifier.

### H. A check of the version in `FromID` and the decoders

`FromID`, `UnmarshalText` and `UnmarshalBinary` of the UUID types would
refuse a UUID of another version or variant. A field of the type would
then contain no such UUID after a decode.

**Why not:** the encoders cannot refuse a value. `AppendKanon` has no
error result, `ExactKanon` states that the append method returns no
error for a value other than the zero value, and `String` cannot fail.
A type conversion builds a UUID of any 16 bytes. Such a UUID encodes
into a kanon record or a JSON document that a strict decoder refuses.
Persisted encodings are frozen, so such a record could not be read
again. `FromID` would also no longer invert `ID` for such a UUID, and
its result false would mean either a wrong size or a wrong version.

A check on both sides is `ValidateKanon`, which kanon calls on every
encode and decode. kanon encodes a type with that method as its
underlying type and not through its binary methods. Every encode of the
type then has an error path. `uuidv7.Parse` and `uuidv7.TimestampMillis`
already leave the check to `uuidv7.Valid`, and RFC 9562 recommends
treating UUIDs as opaque values where an application does not need
their bits.

## Drawbacks

- `ulid`, `ksuid`, `uuidv4` and `uuidv7` add 4 types, 4 functions, 54
  methods and 4 sentinels.
- `uuidv4.UUID` and `uuidv7.UUID` declare the same 14 methods. Their
  text and binary methods call the same functions of
  `internal/uuidtext`.
- The codecs of the UUID types accept a UUID of any version. A consumer
  that requires the version of the package calls `Valid` after each
  decode.
- The decoders of ULID and UUID text accept both cases of a letter, so
  a value has more than one text. A document that is decoded and
  encoded again can change its letters. `id.ID` has exactly one text
  per value.
- The binary form of the zero value is no bytes while its text is the
  all-zero text. oklog/ulid, google/uuid and segmentio/ksuid write zero
  bytes for it.
- A consumer type that embeds `id.ID128` without binary methods changes
  the kanon bytes of its records when it embeds a value type.

## Open questions

None.

## Unresolved / future work

- The types have no methods of `database/sql`, such as `Scan` and
  `Value`, and no `flag.Value` methods. Core does not call them.
- The types have no method for the timestamp. `ulid.TimestampMillis`
  and `uuidv7.TimestampMillis` read it from `u.ID()`.

## References

- RFC 9562, Universally Unique IDentifiers (UUIDs): section 4, the text
  and its letter case, section 5.9, the Nil UUID, and section 6.12, the
  opacity of a UUID.
- The ULID specification at d0c7170, <https://github.com/ulid/spec>: a
  canonical text of 26 characters, case insensitive.
- oklog/ulid v2.1.1, `ulid.go`: `Zero` at line 80, `UnmarshalBinary` at
  303, `MarshalText` at 317, `UnmarshalText` at 404 and `IsZero` at 423.
- google/uuid v1.6.0: `Nil` at `hash.go:19`, the codec methods at
  `marshal.go:10-38`, `Parse` at `uuid.go:68`, which checks no version,
  and `Version` at `uuid.go:286`.
- segmentio/ksuid at d337249, `ksuid.go`: `Nil` at line 56, `IsNil` at
  95, and the codec methods at 111 to 135.
- kanon v0.3.0, `internal/kanon/classify.go` lines 95 to 101 and 184 to
  189, and `internal/kanon/methods.go` lines 81 to 92: the order in which
  kanon resolves a named type and its methods.
- kanon v0.3.0, `message.go` lines 135 to 181: `Validator`, its
  encoding as the underlying type and its calls on every encode and
  decode.
- kanon v0.3.0, `message.go` lines 204 to 268: `Sizer`, `Exact` and
  `Appender`.
- Go 1.27.2, `src/encoding/json/encode.go` lines 135 to 144: the option
  `omitzero` and `IsZero`.
- Go 1.27.2, `src/encoding/gob/type.go` lines 79 to 89: gob encodes
  through `GobEncode` and the binary methods, and not through the text
  methods.
- Go 1.27.2, `src/runtime/string.go` line 20: the stack buffer of 32
  bytes for a string conversion.
- RFC-0057, fixed-size identifiers without codec methods.
- RFC-0045, the kanon forms of core types.
- ADR-0016, persisted encodings are frozen.
- ADR-0064, the text of `id.ID` and `crypto.Digest`.
