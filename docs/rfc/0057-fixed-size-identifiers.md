---
rfc: 0057
title: Fixed-Size Identifiers
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-11
updated: 2026-10-11
discussion: https://github.com/thesm-os/core/issues/26
supersedes: none
superseded-by: none
produces-adr: tbd
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0057: Fixed-Size Identifiers

## Summary

We propose three fixed-size identifier types in package `id`: `ID128`,
`ID160` and `ID256`. Each is a byte array of one identifier size, and
each converts to and from `ID`. `ID` remains the type for an identifier
of any size, up to 256 bits. A fixed-size type stores an identifier of a
size that the consumer knows, such as the ULIDs that it mints, in a map
key or a field of that size.

- `ID.As128`, `ID.As160` and `ID.As256` return the fixed-size form of an
  `ID` of that size, and report false for an `ID` of another size.
- `ID128.ID`, `ID160.ID` and `ID256.ID` return the `ID` of the fixed-size
  form.
- Each type has `Compare` and `String`, which return what the methods of
  `ID` return for the same identifier.

With `ID` keys, a map of 1,000,000 identifiers takes 67.7 MB. The same
map takes 28.6 MB with `ID128` keys, 35.9 MB with `ID160` keys and
52.0 MB with `ID256` keys.

## Motivation

### A map keyed by ID stores 33 bytes per key

An `ID` is an array of 32 bytes and a size byte (`id/id.go:48-51`). A map
keyed by `ID` stores, hashes and compares all 33 bytes, whatever the size
of the identifier. RFC-0009 accepted the unused bytes, 16 for a ULID and
12 for a KSUID, in exchange for one type of every size. The 256-bit
maximum keeps room for identifiers of larger schemes.

We measured maps of 1,000,000 random identifiers with `int32` values, on
an AMD Ryzen 9 9950X3D with Go 1.27.2. The heap is the difference of
`runtime.MemStats.HeapAlloc` around the build of the map. The times are
the medians of three runs of the mean that `bench` reports for one call,
on one core:

| Identifier | Key | Heap | Insert of 1,000,000 keys | Lookup, 10,000 keys | Lookup, 1,000,000 keys |
|---|---|---|---|---|---|
| 128 bits | `ID` | 67.7 MB | 64.7 ms | 40 ns | 116 ns |
| 128 bits | `[16]byte` | 28.6 MB | 33.0 ms | 33 ns | 63 ns |
| 160 bits | `ID` | 67.7 MB | 61.6 ms | 39 ns | 114 ns |
| 160 bits | `[20]byte` | 35.9 MB | 47.0 ms | 35 ns | 76 ns |
| 256 bits | `ID` | 67.7 MB | 64.6 ms | 39 ns | 122 ns |
| 256 bits | `[32]byte` | 52.0 MB | 33.9 ms | 38 ns | 107 ns |

A consumer that keys large maps by identifiers of one size has this cost
on every map.

### Each consumer repeats the size rule

A consumer can convert an `ID` to an array today:

```go
if i.Size() == id.Size128 {
    k := [id.Size128]byte(i.Bytes())
    // k is the key of i.
}
```

The conversion does not allocate. Each consumer repeats the size check,
and a consumer that omits it panics on an identifier of 160 or 256 bits.
The array has no method, so a log line prints it as 16 numbers, and a
sort compares it through the `bytes` package.

### Prior art

- The identifier types of the Go libraries for these schemes are byte
  arrays of one size: `uuid.UUID` of google/uuid is `[16]byte`
  (`uuid.go:20`), `ulid.ULID` of oklog/ulid is `[16]byte`
  (`ulid.go:48`), and `ksuid.KSUID` of segmentio/ksuid is an array of 20
  bytes (`ksuid.go:28` and `:43`).
- `net/netip` stores an address of either size in one type, `Addr`, and
  returns a fixed-size array with `As4` and `As16`. `AddrFrom4` and
  `AddrFrom16` build an `Addr` from those arrays (`netip.go:91`, `:101`,
  `:706` and `:715` in Go 1.27.2). `As4` panics for an address that is
  not IPv4.

## Detailed design

### Components

| Component | Responsibility |
|---|---|
| `ID128`, `ID160`, `ID256` | An identifier of 128, 160 or 256 bits, as an array of 16, 20 or 32 bytes |
| `ID.As128`, `ID.As160`, `ID.As256` | The fixed-size form of an `ID` of that size, and false for another size |
| `ID128.ID`, `ID160.ID`, `ID256.ID` | The `ID` of a fixed-size form |
| `Compare`, `String` of each type | The order and the diagnostic text of `ID` for the same identifier |

`id/fixed.go` declares `ID128`, `ID160` and `ID256` beside `ID`, and
imports only the standard library, as `id/id.go` does.

### API

```go
// ID128 is a 128-bit identifier, such as a ULID or a UUID, as an array of
// its 16 bytes. It stores an identifier whose size the consumer knows, in a
// map key or a field of 16 bytes. ID128 is comparable, and == compares two
// identifiers. [ID] stores an identifier of any size.
//
// The zero ID128 is the identifier of 16 zero bytes. Its ID is not
// [Zero], so ID128 has no value for "no identifier".
//
// # Allocation contract
//
// A value type of 16 bytes, passed by value. Construction, comparison and
// conversion do not allocate. [ID128.String] allocates its result.
type ID128 [Size128]byte

// As128 returns the fixed-size form of i, and reports whether i has the
// size [Size128]. It returns the zero ID128 and false for an ID of another
// size, and for [Zero].
func (i ID) As128() (ID128, bool)

// ID returns the ID of k, of size [Size128]. k.ID().As128() returns k and
// true.
func (k ID128) ID() ID

// Compare returns -1, 0 or +1 by the order of the bytes of k and other. It
// returns what [ID.Compare] returns for their IDs.
func (k ID128) Compare(other ID128) int

// String returns what [ID.String] returns for the ID of k: "id:" and the
// 16 bytes in hexadecimal.
func (k ID128) String() string
```

`ID160` with `As160`, and `ID256` with `As256`, have the same methods at
20 and 32 bytes.

A consumer that keys its maps by a type of its own embeds the fixed-size
type, as the package documentation describes for `ID`:

```go
type OrderID struct{ id.ID128 }

key, ok := gen.Generate().As128()
if !ok {
    return ErrOrderID
}
orders[OrderID{key}] = order
```

### Invariants

- `i.As128()` reports true exactly when `i.Size()` is `Size128`, and
  then `k.ID() == i` for the `k` that it returns.
- `k.ID().As128()` returns `k` and true for every `k`. `As160` and
  `ID160`, and `As256` and `ID256`, are inverses in the same way.
- `a.Compare(b)` equals `a.ID().Compare(b.ID())`, and `k.String()`
  equals `k.ID().String()`.
- The zero `ID128` converts to the 128-bit `ID` of 16 zero bytes, and
  `Zero.As128()` returns the zero `ID128` and false. A UUID never has 16
  zero bytes, because its version bits are set. A ULID has 16 zero bytes
  only at the timestamp of 1970 with a random part of zero.

### Encodings

kanon writes a field of a fixed-size type as the bytes of its array, the
form that RFC-0045 gives `sign.KeyID` through its underlying type. These
bytes are the binary form of the `ID` of the same identifier
(ADR-0016), so a field that changes from `ID` to `ID128` keeps the bytes
of its records. kanon leaves out a byte array whose bytes are all zero,
and a decoder refuses the bytes of an array of another length.

For the identifier of 16 zero bytes, a field of type `ID128` does not
write bytes, where a field of type `ID` writes 16. A canonical decode into
a field of type `ID128` refuses those 16 zero bytes.

The fixed-size types do not declare binary or text methods. A byte array
is its own binary form, and encoding/gob encodes it. encoding/json writes
an array as a list of numbers, and `ID` does not declare a JSON form
either.

### Bounds

| Quantity | Bound |
|---|---|
| Size of `ID128`, `ID160` and `ID256` | 16, 20 and 32 bytes, against 33 for `ID` |
| Work of `As128`, `As160` and `As256` | One comparison of the size and a copy of the bytes |
| Work of `ID` | A copy of the bytes into an `ID` |

### Failure handling

| Condition | Function | Result |
|---|---|---|
| An `ID` of another size, or `Zero` | `As128`, `As160`, `As256` | The zero array and false |

No function of the fixed-size types returns an error or panics. The size
of an identifier comes from its input, through `FromBytes` or a decode.
A call for an identifier of another size reports false, and the caller
handles it as a refused input. `As4` of `net/netip` panics instead.

### Allocation contract

| Call | Objects |
|---|---|
| `As128`, `As160`, `As256` | 0 |
| `ID` of each type | 0 |
| `Compare` of each type | 0 |
| `String` of each type | 1, the result string |

### Tests

| # | Guarantee |
|---|---|
| 1 | `As128`, `As160` and `As256` return the bytes of an `ID` of their size and true, and the zero array and false for each other size and for `Zero` |
| 2 | Over generated identifiers of each size, `ID` and the `As` method of the size are inverses |
| 3 | Over generated pairs, `Compare` and `String` of each type return what the methods of `ID` return for the converted values |
| 4 | The zero `ID128` converts to an `ID` whose `IsZero` reports false |
| 5 | The bytes of each fixed-size value equal the binary form that `ID.AppendBinary` appends for its `ID` |
| 6 | `TestFixedAllocs` and the benchmarks state the counts of the allocation contract |
| 7 | The suites cover every statement, and dokimi-mutate-go kills every mutant of `id/fixed.go` |

## Alternatives considered

### A. A generic ID

`ID` would take its array as a type parameter:
`type ID[B [16]byte | [20]byte | [32]byte] struct{ b B }`. An `ID` of each
size would then take only its own bytes.

**Why not:** go vet in Go 1.27.2 refuses the slice `i.b[:]`. Its message
is "[16]byte and [20]byte have different underlying types", so `Bytes`
would copy the bytes one at a time. Each instantiation is a type of its
own. A map keyed by `ID[[16]byte]` refuses an `ID[[32]byte]`. A field, a
map or a `Generator` that accepts identifiers of any size would need an
interface, which boxes each value. RFC-0009 rejected a generic
`Generator` for that boxing. `FromBytes` reads the size from the length of
its input, so it cannot return a type that the compiler chooses. Every
consumer of `ID` would change.

### B. An accessor that returns an array

`ID.Key128` would return the 16 bytes as `[16]byte` and a flag.

**Why not:** the accessor removes the size rule from the code of the
consumer. The array that it returns has no `String` or `Compare` method,
so a log line prints it as 16 numbers. A defined type gives the key the
methods of an identifier, and it is still 16 bytes.

### C. One fixed-size type for 128 bits

Only `ID128` would exist, because the generators of this module return
128-bit identifiers, apart from KSUIDs.

**Why not:** a map with `ID160` keys takes 47% less memory than one with
`ID` keys, and a map with `ID256` keys 23% less. Each size of `ID` has
its fixed-size type, as `New128`, `New160` and `New256` build an `ID` of
each size.

### D. A smaller ID

`ID` would store 128 bits at most, in 17 bytes.

**Why not:** RFC-0009 chose 256 bits as the maximum, for KSUIDs and
256-bit identifiers, and to keep room for future schemes. A consumer
whose identifiers have one size chooses the fixed-size type of that size
instead.

### E. IsZero, Size, Bytes and Equal on the fixed-size types

**Why not:** `ID.IsZero` means "no identifier". An `IsZero` of a
fixed-size type would report the identifier of zero bytes under the same
name. The size of each type is a constant, `k[:]` slices its bytes, and
`==` compares two values.

### F. Generators that return fixed-size types

`ulid` would return an `ID128` beside its `ID`.

**Why not:** `Generator` returns `ID`, so every generator serves every
consumer, and `As128` converts the result without an allocation.

## Drawbacks

- The package adds three types and twelve methods.
- A value of a fixed-size type has no value for "no identifier". A
  consumer that needs absence keeps it apart from the key, or uses the
  identifier of zero bytes, which no UUID has.
- A consumer chooses a type per field, and converts at the boundary
  where an identifier of any size enters.
- In a kanon record, the identifier of zero bytes in a field of type
  `ID128` does not write bytes, where a field of type `ID` writes 16. A
  canonical decode into a field of type `ID128` refuses those 16 bytes.

## Open questions

None.

## Unresolved / future work

None.

## References

- google/uuid at 2d3c2a9, `uuid.go:20`, `type UUID [16]byte`.
- oklog/ulid at 75921a7, `ulid.go:48`, `type ULID [16]byte`.
- segmentio/ksuid at d337249, `ksuid.go:28` and `:43`, an array of 20
  bytes.
- Go 1.27.2, `src/net/netip/netip.go`: `AddrFrom4` and `AddrFrom16` at
  lines 91 and 101, and `As16` and `As4` at lines 706 and 715.
- kanon at e296e64, `docs/rfc/0001-wire-format.md`: the presence of a
  byte array at line 131, and its form at line 187.
- RFC-0009, the identifier type `ID` and its sizes.
- RFC-0045, the kanon forms of core types.
- ADR-0016, persisted encodings are frozen.
