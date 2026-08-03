---
rfc: 0014
title: Binary Encoding for Core Value Types
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-08-03
updated: 2026-08-03
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0007
---

# RFC-0014: Binary Encoding for Core Value Types

## Summary

`crypto.Digest` and `clock.Instant` are both persisted and both signed
over, and neither can currently be written or read. This RFC adds a
byte-slice constructor and a canonical binary encoding to each,
implementing the standard library's `encoding.BinaryAppender`,
`BinaryMarshaler` and `BinaryUnmarshaler` rather than inventing a
codec. The encodings are stable wire contracts: they will not change
within a major version.

## Motivation

`crypto.Digest` is `bytes [64]byte; size uint8`, both unexported, with
constructors that accept fixed-size arrays only. There is no
constructor from `[]byte` and no marshalling.

The consequence is that every decode path — a wire message, a database
column, a proof body — has no supported route into the type. The
workaround is a slice-to-array conversion, which hardcodes one length
and panics on a short read. That defeats the algorithm agility the
type exists to provide: a `Digest` that can hold 256, 384 or 512 bits
is useless if the decode path has to name the width in advance.

`encoding/gob` refuses a struct with no exported fields outright, as a
value and as a map key, so the type cannot round-trip through the one
stdlib codec that needs no schema.

`clock.Instant` has no encoding at all. An instant that is persisted or
signed over needs a stable byte layout, and without one every caller
invents a layout. Those layouts are frozen the moment the first
artefact is written, and they will not agree.

The two types share one deadline: whatever is written before the
encoding exists cannot be re-read after it does.

## Detailed design

### `crypto.Digest`

```go
// In package crypto.

// DigestFromBytes builds a Digest from a digest-shaped byte slice,
// inferring Size from len(b). Use it on decode paths.
//
// Returns ErrDigestSize unless len(b) is exactly DigestSize256,
// DigestSize384, or DigestSize512. A truncated input is a decode
// error, never a panic and never a silent truncation.
func DigestFromBytes(b []byte) (Digest, error)

// AppendBinary appends d.Size() bytes to dst: the active digest
// bytes, with no length header. The size is recoverable from the
// length of the encoded form.
//
// Returns ErrDigestZero for the zero Digest, which has no wire form
// (ADR-0007).
func (d Digest) AppendBinary(dst []byte) ([]byte, error)

func (d Digest) MarshalBinary() ([]byte, error)
func (d *Digest) UnmarshalBinary(data []byte) error
```

`AppendBinary` is the primitive and `MarshalBinary` is defined in terms
of it, which is the shape `encoding.BinaryAppender` was added for: a
caller assembling a larger buffer pays no intermediate allocation.

`UnmarshalBinary` accepts exactly what `DigestFromBytes` accepts and returns
the same error. Two decode paths that disagree about which inputs are
valid is the defect this RFC exists to close, so they share one
implementation.

### The zero `Digest`

ADR-0007 decides that the zero `Digest` is a valid in-memory sentinel
with no wire form. This RFC implements that decision:

- `DigestFromBytes(nil)` and `DigestFromBytes([]byte{})` return `ErrDigestSize`.
- `Digest{}.MarshalBinary()` returns `ErrDigestZero`.
- `UnmarshalBinary` on empty input returns `ErrDigestSize`.

The alternative — a zero-length encoding — makes every truncated read
decode to a genesis anchor without an error. `ErrDigestZero` is a
distinct sentinel from `ErrDigestSize` so a caller can tell "you tried
to persist the sentinel" from "the bytes are the wrong length".

### `clock.Instant`

```go
// In package clock.

// AppendBinary appends the canonical 16-byte big-endian encoding of
// i to dst:
//
//    Wall     int64   8 bytes
//    Logical  uint32  4 bytes
//    Node     uint32  4 bytes
//
// The encoding is a stable wire contract. Instants are signed over
// and persisted in artefacts that must verify across builds and
// across years; the layout will not change within a major version.
func (i Instant) AppendBinary(dst []byte) ([]byte, error)

func (i Instant) MarshalBinary() ([]byte, error)
func (i *Instant) UnmarshalBinary(data []byte) error
```

Field order follows comparison order, so the encoding of two instants
sorts bytewise in the same order `Compare` ranks them. That is not
required by anything here, and it is free, and a byte-ordered key is
what a caller reaches for the moment an instant becomes part of a
storage key.

`Wall` is signed and encoded big-endian two's complement, so instants
before the Unix epoch encode without a special case. Bytewise ordering
does not hold across the epoch boundary; the doc comment says so
rather than leaving a reader to discover it.

`UnmarshalBinary` returns `ErrInstantSize` unless `len(data) == 16`.

### Unit accessors

```go
// UnixMilli returns Wall truncated to milliseconds.
func (i Instant) UnixMilli() int64

// UnixMicro returns Wall truncated to microseconds.
func (i Instant) UnixMicro() int64
```

`Wall` is nanoseconds. Open-coding `Wall / 1e6` is a recurring unit
error, and it is silent: the wrong divisor produces a plausible number,
and when an instant is being packed into a time-sortable identifier the
wrong divisor collapses the representable window instead of failing.

### Errors

Per the module's convention, the sentinels live in each package's
`errors.go`:

```go
// crypto/errors.go
var ErrDigestSize = errors.New("crypto: digest length must be 32, 48, or 64 bytes")
var ErrDigestZero = errors.New("crypto: the zero digest has no binary encoding")

// clock/errors.go
var ErrInstantSize = errors.New("clock: instant encoding must be 16 bytes")
```

### Allocation contract

`AppendBinary` on both types is zero-allocation when `dst` has spare
capacity. `MarshalBinary` allocates exactly one slice of the encoded
length. `DigestFromBytes` and both `UnmarshalBinary` methods allocate
nothing — they copy into a value the caller already owns.

## Alternatives considered

### A. A length-prefixed digest encoding

Prefix the digest bytes with a one-byte length so the encoded form is
self-describing independently of the framing.

**Why not:** the length is already recoverable from `len(data)`, and
every container that carries a digest — a struct field, a database
column, a protobuf `bytes` — carries its own length. The prefix adds a
byte to every stored digest to restate what the container already knows,
and it introduces a second, disagreeing notion of "the digest's size".

### B. Text encodings alongside binary

Implement `MarshalText` as hex or base64 in the same RFC.

**Why not:** `Digest.String()` already renders hex for humans. A text
encoding that round-trips is a second wire contract with its own
stability promise, and nothing forces the two to be decided together.
Deferred rather than rejected — see future work.

### C. Exported fields on `Digest`

Export `Bytes` and `Size` so `encoding/gob` and reflection-based codecs
work without any method.

**Why not:** it makes the type mutable through a field, which destroys
the immutability the value type is built on — a caller could resize a
digest without rehashing it. The methods restore `gob` support without
that cost.

### D. A single generic `Encode`/`Decode` pair in `core`

Define one encoding helper both types use.

**Why not:** the standard library already names these operations, and
`encoding.BinaryMarshaler` is what `gob` and every downstream codec
look for. A `core`-specific spelling would be a second name for the
same contract, which ADR-0009 rejects in another context for the same
reason.

## Drawbacks

- Two permanent wire formats, decided now. The `Instant` layout in
  particular fixes `Node` at 32 bits and `Logical` at 32 bits; widening
  either later is a new major version, not an additive change.
- `AppendBinary` returning an error is awkward for the zero `Digest`
  case, because the overwhelmingly common call site cannot fail. The
  signature is fixed by `encoding.BinaryAppender` and the error is the
  price of implementing the standard interface rather than a bespoke
  one.
- `UnixMilli` and `UnixMicro` are conveniences that invite a caller to
  reach for a truncated instant where the full one was correct.
- Four new sentinel errors across two packages, each of which is now
  part of the compatibility surface.

## Open questions

None. Both questions this RFC previously carried are resolved: text
encodings are deferred to future work rather than decided here, because
a text form is a second wire contract with its own stability promise
and nothing forces the two to be settled together; and
`DigestFromBytes` gets no size-checking companion, because a caller who
knows the expected algorithm can compare `Size()` against the
`DigestSize*` constant in one line, and a second constructor would
split every decode path in the ecosystem between two spellings.

## Unresolved / future work

- Text encodings for both types.
- A byte-slice constructor for `id.ID`, which has the same defect from
  the same cause. It is separable and lands on its own.
- Whether `tag.Tags` and `version.Version` need canonical encodings.
  Both are already string-shaped and neither is currently signed over.
