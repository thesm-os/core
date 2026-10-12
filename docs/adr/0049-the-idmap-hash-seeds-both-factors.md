---
adr: 0049
title: The idmap Hash Seeds Both Factors
status: Accepted
date: 2026-10-12
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0049: The idmap Hash Seeds Both Factors

## Status

Accepted

## Context

`idmap.Map` and `idmap.Concurrent` hash the first 16 bytes of a key with
one multiplication of two 64-bit factors into 128 bits. The hash is the
exclusive or of the two halves of the product. In the design of idmap, the
first factor combined the first word of the key with the seed and a
constant. The second factor combined the second word with another
constant.

A product is 0 when one of its factors is 0. The second factor is 0 for
every key whose second word equals the constant of that factor. All such
keys hash to 0 under every seed, whatever their first word is. A caller
who chooses such keys puts them all in one run of the table, and every
lookup of such a key walks that run. The seed exists to prevent that
attack.

## Decision

We will combine each factor with a secret word of its own, because a
factor without a secret word lets a caller choose keys that hash to 0
under every seed:

- Each map draws two secret words from `hash/maphash` when it is created.
- The first factor combines the first word of the key with the first
  secret word, and the second factor combines the second word with the
  second secret word.
- The bytes of an ID160 or an ID256 past the first 16 enter a second
  multiplication. Its first factor combines the third word with the first
  hash, and its second factor combines the fourth word with the second
  secret word.

This decision supersedes the factors that RFC-0058 specifies for the hash.

## Alternatives Considered

### The seed in the first factor and a constant in the second

The design as RFC-0058 specifies it.

Rejected. Every key whose second word equals the constant hashes to 0
under every seed.

### One secret word in both factors

The first factor would combine the first word with the seed and one
constant, and the second factor would combine the second word with the
seed and another constant.

Rejected. Each key then has a partner key with the same hash under every
seed. The partner of a key swaps the two factors, and a caller computes
it from the key and the two constants without the seed.

## Consequences

**Positive:**

- A caller who does not know the secret words cannot make a factor 0.
- The hash takes the same time: one exclusive or per factor.

**Negative:**

- The hash is not a cryptographic hash. A caller who knows a secret word
  can choose keys that hash to 0. The timing of lookups can reveal a
  secret word.
- A map keeps two secret words, 8 bytes more than one seed.

**Neutral:**

- The hash of every key changes. idmap does not store a hash, so no
  stored data changes.
- The hash function of each key size returns the words of the key with
  the hash, and `Concurrent` compares those words.

## References

- RFC-0058, fast maps for identifier keys, the section Hashing.
- `idmap/key.go`: `seed`, `newHash`, `hash128`, `hash160`, `hash256` and
  `mix`.
- Go 1.27.2, `src/internal/runtime/maps/runtime_hash64.go:23` and `:64`:
  the fallback hash of the runtime combines one factor with a random key
  of the process and the other with the seed of the map.
  `runtime_alg.go:25` and `:32` draw the keys of the process at startup.
- wyhash, <https://github.com/wangyi-fudan/wyhash>.
