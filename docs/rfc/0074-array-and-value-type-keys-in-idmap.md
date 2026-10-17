---
rfc: 0074
title: Array and Value-Type Keys in idmap
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

# RFC-0074: Array and Value-Type Keys in idmap

## Summary

We propose seven constructors of `idmap.Map`:

- `New128`, `New160` and `New256` return a map whose keys are of an array
  type of 16, 20 or 32 bytes, such as `ulid.ULID`, `ksuid.KSUID` and
  `uuidv4.UUID`.
- `NewTypedULID`, `NewTypedKSUID`, `NewTypedUUIDv4` and `NewTypedUUIDv7`
  return a map whose keys are of a struct type with one embedded field of a
  value type, such as `struct{ ulid.ULID }`.

Each map hashes the bytes of a key in place and does not copy the key. A
lookup then takes the time of a lookup in a map of `NewTyped128`. `idmap`
imports the packages `ulid`, `ksuid`, `uuidv4` and `uuidv7` for the four
struct forms.

## Motivation

### The value types do not key a Map

`idmap.New` takes keys of `idmap.Key`, the type set
`id.ID128 | id.ID160 | id.ID256`. `NewTyped128`, `NewTyped160` and
`NewTyped256` take a struct type whose one field embeds one of these three
types. No constructor accepts a value type of the identifier packages, or a
struct type that embeds one:

```go
type ShardID struct{ ulid.ULID }

idmap.New[ulid.ULID, int](n)       // ulid.ULID does not satisfy idmap.Key
idmap.NewTyped128[ShardID, int](n) // ShardID does not satisfy ~struct{ id.ID128 }
```

A consumer declares a struct that embeds a value type to tell its
identifiers apart, as it does for the fixed-size identifiers. Such a struct
keys a Go map only. A lookup in 10k keys of a Go map keyed by
`struct{ id.ID128 }` took 8.46-8.58 ns, against 5.26-5.41 ns in a `Map` of
the same keys. The array types `crypto.ID` and `sign.KeyID` of core have 16
bytes, and they do not key a `Map` either.

A caller can key a `Map` by `id.ID128` and convert each key at each call.
The compiler then accepts the identifier of any other identifier type as a
key, and the caller converts each key that `All` yields back.

### Each form of key needs a constraint of its own

A hash reads the bytes of a key as an identifier, and generic code converts
a key only when every type of its constraint converts to the target type. A
constraint with one core type gives each form of key a conversion without a
copy:

- A key of an array type of 16 bytes slices to `[]byte`, and the slice
  converts to `*id.ID128`.
- A key of a struct type converts to the struct of its constraint, such as
  `struct{ ulid.ULID }`. The address of its field converts to `*id.ID128`.

No constraint covers the struct types of every value type. An embedded
field may not be a type parameter in Go, so the constraint `~struct{ E }`
with `E ~[16]byte` does not compile. Each struct form has to name its value
type.

### The cost depends on the form of the conversion

A probe outside this module measured `Get` of present keys in a `Map` of
8,192 random keys with values of 8 bytes. The probe ran each variant in a
process of its own on Go 1.27.2, with one thread of an AMD Ryzen 9 9950X3D.
The order of the variants rotated by one place over four rounds of three
runs. Each figure is the median of the 12 runs:

| Key | How the hash reads the key | Get |
|---|---|---|
| `struct{ id.ID128 }` of `NewTyped128` | In place, the embedded `ID128` | 5.07 ns |
| `ulid.ULID` | In place, `(*id.ID128)(k[:])` | 5.39 ns |
| `struct{ ulid.ULID }` | In place, `(*id.ID128)(&c.ULID)` | 4.88 ns |
| `struct{ uuidv4.UUID }` | In place, `(*id.ID128)(&c.UUID)` | 4.94 ns |
| `struct{ ulid.ULID }` | A copy into a new `id.ID128` | 7.01 ns |
| `ulid.ULID` | A copy into a new `id.ID128` | 6.99 ns |
| `struct{ ulid.ULID }` | A function of the caller that returns the `id.ID128` | 8.09 ns |
| `struct{ ulid.ULID }` | A method of the key that returns the `id.ID128` | 8.78 ns |
| `struct{ id.ID160 }` of `NewTyped160` | In place, the embedded `ID160` | 17.39 ns |
| `ksuid.KSUID` | In place, `(*id.ID160)(k[:])` | 17.76 ns |
| `struct{ ksuid.KSUID }` | In place, `(*id.ID160)(&c.KSUID)` | 17.86 ns |
| `struct{ id.ID256 }` of `NewTyped256` | In place, the embedded `ID256` | 10.67 ns |
| An array type of 32 bytes | In place, `(*id.ID256)(k[:])` | 9.42 ns |

A hash in place takes between 12% less and 6% more time than the existing
constructor of its size. A copy into a new identifier cost about 2 ns more
for each key type of 16 bytes that the probe copied. In an earlier run with
the same method, a copy of a `struct{ id.ID128 }` key took 6.66 ns, against
5.02 ns in place. We have not established why the copy costs this much.

## Detailed design

### Components

| Component | Responsibility |
|---|---|
| `New128`, `New160`, `New256` | A `Map` of an array type of 16, 20 or 32 bytes |
| `NewTypedULID` | A `Map` of a struct type whose one field embeds a `ulid.ULID` |
| `NewTypedKSUID` | A `Map` of a struct type whose one field embeds a `ksuid.KSUID` |
| `NewTypedUUIDv4`, `NewTypedUUIDv7` | A `Map` of a struct type whose one field embeds a `uuidv4.UUID` or a `uuidv7.UUID` |

### API

```go
// New128 returns an empty map of the array type K of 16 bytes, such as
// ulid.ULID, uuidv4.UUID, uuidv7.UUID, crypto.ID and sign.KeyID. The map
// hashes the bytes of each key in place, as the ID128 of the same bytes
// hashes. Its table is the table that New allocates for capacity.
//
// New128 panics as make does when the slots for capacity exceed the memory
// that make can allocate.
//
// # Allocation contract
//
// New128 allocates the map, its arrays of tags, keys and values, and the
// hash function of K.
func New128[K ~[id.Size128]byte, V any](capacity int) *Map[K, V]

// New160 is New128 for keys of 20 bytes, such as ksuid.KSUID.
func New160[K ~[id.Size160]byte, V any](capacity int) *Map[K, V]

// New256 is New128 for keys of 32 bytes, such as a type of the caller for a
// SHA-256 digest.
func New256[K ~[id.Size256]byte, V any](capacity int) *Map[K, V]

// NewTypedULID returns an empty map of the struct type K, whose one field
// embeds a ulid.ULID. The map hashes the ULID of each key in place, as the
// ID128 of the same bytes hashes. Its table is the table that New allocates
// for capacity.
//
// NewTypedULID panics as make does when the slots for capacity exceed the
// memory that make can allocate.
//
// # Allocation contract
//
// NewTypedULID allocates the map, its arrays of tags, keys and values, and
// the hash function of K.
func NewTypedULID[K ~struct{ ulid.ULID }, V any](capacity int) *Map[K, V]

// NewTypedKSUID, NewTypedUUIDv4 and NewTypedUUIDv7 are NewTypedULID for a
// struct type whose one field embeds a ksuid.KSUID, a uuidv4.UUID or a
// uuidv7.UUID.
func NewTypedKSUID[K ~struct{ ksuid.KSUID }, V any](capacity int) *Map[K, V]
func NewTypedUUIDv4[K ~struct{ uuidv4.UUID }, V any](capacity int) *Map[K, V]
func NewTypedUUIDv7[K ~struct{ uuidv7.UUID }, V any](capacity int) *Map[K, V]
```

A consumer keys a map by its identifier type:

```go
// ShardID identifies a shard. The zero ShardID means no shard.
type ShardID struct{ ulid.ULID }

shards := idmap.NewTypedULID[ShardID, *Shard](n)
shards.Set(ShardID{u}, s)
```

### Hashing

The hash function of each map converts a key in place and calls the hash of
its size. An array key slices to `[]byte` and converts to a pointer to the
identifier:

```go
func(s seed, k K) (uint64, words) {
	return hash128(s, (*id.ID128)(k[:]))
}
```

The slice of an array of 16 bytes has 16 bytes, so the conversion to
`*id.ID128` never panics. A struct key converts to the struct type of its
constraint, and the address of the embedded field converts to a pointer to
the identifier:

```go
func(s seed, k K) (uint64, words) {
	c := struct{ ulid.ULID }(k)

	return hash128(s, (*id.ID128)(&c.ULID))
}
```

A key of each map hashes as the identifier of the same bytes hashes under
the same seed, and no hash function copies a key into a new identifier.

### Dependencies

`idmap` imports `ulid`, `ksuid`, `uuidv4` and `uuidv7` for the struct
forms. Through them it also imports `clock`, `rand` and
`id/internal/uuidtext`. `idmap` already imports `epoch`, `errs`, `id`,
`version` and the kanon runtime. `ulid`, `ksuid`, `uuidv4` and `uuidv7`
depend on `clock`, `epoch`, `errs`, `id`, `id/internal/uuidtext`, `rand`,
`version` and the kanon runtime alone, so the imports do not form a cycle.

### Scope

- `Concurrent` takes the identifiers of `Key` alone, as it does for the
  struct types of `NewTyped128`.
- `Typed` keeps its keys over `id.ID`. A value type has one size, so a `Map`
  serves it.

### Invariants

- A map of each constructor returns for every sequence of `Set`, `Get` and
  `Delete` what a Go map of the same key type returns.
- The hash function of each constructor returns the hash and the words of
  the identifier of the same bytes under the same seed.
- `All` yields keys of `K`.

### Bounds

| Quantity | Bound |
|---|---|
| Memory of a map of a new constructor | The memory of a map of `NewTyped128`, `NewTyped160` or `NewTyped256` for keys of the same size |
| Time of a lookup | The time of a lookup in a map of the typed constructor of the same size, from 12% less to 6% more in the probe |

### Failure handling

| Condition | Function | Result |
|---|---|---|
| A capacity below 1 | Each new constructor | A map with room for one key |
| Slots for capacity beyond the memory that make can allocate | Each new constructor | A panic, as of make |

### Allocation contract

| Call | Objects |
|---|---|
| Each new constructor | The map, its three arrays and the hash function of `K` |
| `Get`, `Delete`, `Len`, `Clear`, a range over `All` | 0, as for every `Map` |
| `Set` | 0, plus the three arrays of a growth |

### Tests

| # | Guarantee |
|---|---|
| 1 | For each new constructor, `TestMap` checks that random calls return what a Go map returns |
| 2 | An internal test checks that the hash function of each new constructor returns the hash and the words of the identifier of the same bytes under the same seed |
| 3 | `TestMapAllocs` checks the allocation contract of each new constructor and of a `Get` in its map. `BenchmarkMap` checks the contract of the `Get` |
| 4 | The suites cover every statement, and dokimi-mutate-go kills every mutant of the new code |

## Alternatives considered

### A. A copy of the key into an identifier

The hash function would copy a key into a new identifier with
`id.ID128(k)` and hash the copy.

**Why not:** a copy cost about 2 ns per hash for each key type of 16 bytes
that the probe copied, also for `struct{ id.ID128 }`. A lookup took
7.01 ns, 38% more than 5.07 ns.

### B. A conversion function of the caller

One constructor would take a function from the key to its identifier, and
serve every struct type of every value type.

**Why not:** the hash calls the function of the caller through a function
value for every key. A lookup took 8.09 ns, 60% more than 5.07 ns.

### C. A method constraint

Each value type would declare a method that returns its identifier. One
constructor would take every key with that method, and a struct type that
embeds a value type has the method by promotion.

**Why not:** a lookup took 8.78 ns, 73% more than 5.07 ns. The value types
would also declare a method for one caller.

### D. A Key with the array types of each size

`Key` would admit the defined array types of 16, 20 and 32 bytes, and `New`
would serve them.

**Why not:** a constraint of three array sizes has no core type, so generic
code neither slices nor converts a key under it. A hash that is generic over
the type set reads the key by indexing. A lookup in 10k identifiers took
9.76-9.81 ns that way, against 8.04-8.28 ns for the concrete hash. A hash
that reads the bytes through `unsafe` is ruled out, because core does not
import `unsafe`.

### E. Constructors in the packages of the value types

`ulid` would declare `NewMap[K ~struct{ ULID }, V any]`, and `ksuid`,
`uuidv4` and `uuidv7` would declare the same for their types. `idmap` would
then import none of them.

**Why not:** the hash function of a map uses the unexported types `seed` and
`words` and the unexported function `hash128` of `idmap`. `ulid`, `ksuid`,
`uuidv4` and `uuidv7` would need `idmap` to export them. Every caller of
`idmap` would then see them.

### F. A conversion by the caller at each call

The caller keys a map by the value type or by `id.ID128`. It converts each
key at each call.

**Why not:** the compiler accepts the identifier of any other identifier
type of the same scheme as a key. The caller also converts each key that
`All` yields back.

### G. Value types with a type parameter that tells them apart

`ulid` would declare `Of[T any] [16]byte`. A consumer would declare
`type ShardID = ulid.Of[shard]`. Every such type is an array of 16 bytes.
`New128` would serve it without the four struct constructors.

**Why not:** it replaces the value types that their consumers embed. A
consumer type becomes an alias of an instantiation. We have not checked how
kanon generates the codec of a field of such a type.

## Drawbacks

- `idmap` adds 7 constructors: 3 for array types and 4 for the struct forms.
- `idmap` imports 7 more packages of core: `ulid`, `ksuid`, `uuidv4`,
  `uuidv7`, `clock`, `rand` and `id/internal/uuidtext`. A program that
  imports `idmap` imports them too.
- Each value type that the identifier packages add needs one more
  constructor in `idmap`.
- `Concurrent` takes no key of an array type and no struct form.

## Open questions

None.

## Unresolved / future work

- A lookup in 8,192 keys of 20 bytes took 17.39-17.86 ns, against
  4.88-5.39 ns for 16 bytes and 9.42-10.67 ns for 32 bytes. This proposal
  does not change the hash or the comparison of keys of 20 bytes.

## References

- ADR-0050, idmap keys maps by identifier types: the constraint of one
  struct type, the lookup times of the Go map, the rejected `Key` that reads
  keys through `unsafe` and the rejected conversion at each call.
- RFC-0058, fast maps for identifier keys, the section Hashing: the cost of
  a hash that is generic over the type set.
- RFC-0073, ULID, KSUID and UUID value types.
- The Go Programming Language Specification, Struct types: an embedded field
  may not be a type parameter, <https://go.dev/ref/spec#Struct_types>.
- The Go Programming Language Specification, Conversions from slice to array
  or array pointer,
  <https://go.dev/ref/spec#Conversions_from_slice_to_array_or_array_pointer>.
- `idmap/key.go:29`, `Key`, and `idmap/map.go:112`, `NewTyped128`.
- `crypto/hasher.go:30`, `crypto.ID`, and `crypto/sign/sign.go:42`,
  `sign.KeyID`.
