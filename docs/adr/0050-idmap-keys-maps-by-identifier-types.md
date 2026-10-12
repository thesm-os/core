---
adr: 0050
title: idmap Keys Maps by Identifier Types
status: Accepted
date: 2026-10-12
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0050: idmap Keys Maps by Identifier Types

## Status

Accepted

## Context

A caller of package `id` declares an identifier type as a struct whose one
field embeds an identifier. The compiler keeps two such types apart:

```go
type ShardID struct{ id.ID128 }
type StreamID struct{ id.ID }
```

`idmap.Key` is the type set of `id.ID128`, `id.ID160` and `id.ID256`, so a
map keyed by an identifier type does not compile. The caller converts each
key at each call, and converts each key that `All` yields back. The
compiler then accepts the identifier of any other identifier type as a key.

Generic code converts a key to a struct type only when every type of the
constraint converts to it. Under the constraint `~struct{ id.ID128 }`, a
constructor converts a key to `struct{ id.ID128 }` and reads its
identifier. A constraint that unites the struct types of two sizes has no
such conversion. A hash under that constraint reads the bytes of a key
only through `unsafe`. Core does not import `unsafe`.

An `id.ID` has 16, 20 or 32 bytes, or none for `id.Zero`. Its input
decides the size, so a map keyed by `StreamID` can receive keys of every
size.

A probe outside this module measured lookups of present keys in a random
order, in plain loops. Each variant ran in a process of its own on four
cores of an AMD Ryzen 9 9950X3D with Go 1.27.2. The order of the variants
rotated over five rounds. The keys were random identifiers of 16 bytes, and
the values had 8 bytes:

| Lookup | 10k keys | 1M keys |
|---|---|---|
| `Map` keyed by `id.ID128` | 5.57-6.18 ns | 35.8-37.6 ns |
| `Map` keyed by `struct{ id.ID128 }` | 5.26-5.41 ns | 37.1-37.7 ns |
| Go map keyed by `struct{ id.ID128 }` | 8.46-8.58 ns | 49.4-60.7 ns |
| `Typed` keyed by `struct{ id.ID }` | 10.70-10.93 ns | 49.4-50.8 ns |
| Go map keyed by `struct{ id.ID }` | 12.39-12.82 ns | 90.0-95.9 ns |

## Decision

We will key the maps of `idmap` by the identifier types of a caller
through constructors whose type parameter has the constraint of one struct
type, because generic code converts such a key to its identifier without
`unsafe`:

- `Map` takes a key of any comparable type. `NewTyped128`, `NewTyped160`
  and `NewTyped256` return a `Map` of a struct type whose one field embeds
  an `ID128`, an `ID160` or an `ID256`. The hash function of the map
  converts a key to its identifier and hashes the identifier.
- `NewTyped` returns a `Typed` of a struct type whose one field embeds an
  `id.ID`. A `Typed` keeps the keys of each size in a `Map` of that size.
  The first `Set` of a size creates that `Map` with the capacity of
  `NewTyped`. The value of the key of `id.Zero` is beside the three maps.
- `Typed` hashes the identifier of a key with the hash function of the
  `Map` of its size. The compiler inlines the walk of that `Map` into the
  methods of `Typed`.
- `Concurrent` takes the identifiers of `Key` alone.

This decision supersedes the constraint `Key` that RFC-0058 specifies for
the keys of `Map`.

## Alternatives Considered

### A Key that admits the struct types

`Key` would admit the struct types and the defined array types of each
size, and the hash would read the bytes of a key through `unsafe`.

Rejected. Core does not import `unsafe`.

### A conversion by the caller at each call

The caller keys a `Map` by the identifier of each key, which `As128`
returns for a `StreamID`.

Rejected. The compiler accepts the identifier of any other identifier type
as a key, and the caller converts each key of `All` back. In a run with
values of 4 bytes, the conversion took 6.73-7.02 ns for a lookup in 10k
keys. The caller also handles each identifier of another size.

### One Map of 33-byte keys

A `Map` would keep the keys of `struct{ id.ID }` of every size in one
table, and its hash function would branch on the size.

Rejected. In a run with values of 4 bytes, a lookup in 10k keys took
17.86-20.91 ns against 11.45-13.03 ns for the Go map of the same keys. A
key of 16 bytes takes 33 bytes of the table.

### A wrapper over the methods of Map

`Typed` would convert a key and call `Map.Get`, `Map.Set` and `Map.Delete`.

Rejected. In a run with values of 4 bytes, a lookup in 10k keys took
13.43-13.72 ns against 12.22-12.52 ns for the Go map. The wrapper copies the
33-byte key through four slots of its stack before `Map.Get` hashes the
identifier.

### A wrapper type for each fixed size

A type such as `Typed128` would hold a `Map` of `ID128` keys and convert
each key.

Rejected. A `Map` of the struct type needs only a hash function that
converts the key. A wrapper type adds a type and a call per method.

## Consequences

**Positive:**

- A caller keys a map by its identifier type. The compiler refuses a key
  of another identifier type. `All` yields keys of the identifier type.
- A `Map` of an identifier type looks up as fast as a `Map` of its
  identifier. A lookup takes 62% of the time of the Go map of the same keys
  in 10k keys. It takes 66% in 1M keys.
- A lookup in a `Typed` takes 86% of the time of the Go map of the same
  keys in 10k keys. It takes 53% in 1M keys.

**Negative:**

- `Map[string, int]` compiles. No constructor returns one, and the zero
  `Map` is not usable.
- `NewTyped128`, `NewTyped160` and `NewTyped256` allocate one object more
  than `New`: the hash function, a closure over the dictionary of the
  identifier type.
- A `Typed` of keys of two or three sizes allocates a `Map` for each size,
  each with room for the capacity of `NewTyped`.
- A lookup in a `Typed` of 10k keys takes about twice the time of a lookup
  in a `Map` of the identifiers, because a key of 33 bytes passes through
  memory.
- `Concurrent` does not take identifier types.

**Neutral:**

- A key of an identifier type hashes as its identifier hashes under the
  same seed.
- `Typed.All` yields the key of `id.Zero` first, then the keys of 16, 20
  and 32 bytes.

## References

- RFC-0058, fast maps for identifier keys, the section API.
- Issue 36 of thesm-os/core, the request for a `Key` that admits the
  struct types.
- `idmap/map.go`: `Map`, `New`, `NewTyped128`, `NewTyped160`,
  `NewTyped256` and `newMap`. `idmap/typed.go`: `Typed` and `NewTyped`.
- `id/doc.go`, the section Compile-time-distinct identifier types.
- The Go Programming Language Specification, Conversions,
  <https://go.dev/ref/spec#Conversions>.
