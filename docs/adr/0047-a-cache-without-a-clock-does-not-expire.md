---
adr: 0047
title: A Cache Without a Clock Does Not Expire
status: Accepted
date: 2026-10-11
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0047: A Cache Without a Clock Does Not Expire

## Status

Accepted

## Context

`cache.New` refused a `Config` without a `Clock`. A cache reads its clock
only to expire entries. It reads the clock once in each `Set` and
`Expire`, and in a lookup only for an entry with an expiry time.

No cache in core expires an entry. `tlog`, `crypto/tsp`, `telemetry`,
`tlog/witness` and `net/internal/semconv` build caches. Each sets every
entry with the zero expiry time, which never expires, and no production
code calls `Cache.Expire`.

Each of these packages still had to supply a clock. `crypto/tsp` built
`hlc.New(0)` for the cache of its verified chains, and
`semconv.NewDurations` took a clock that no entry read.
`tlog.ProverConfig.Clock` existed only for the cache of node tiles. A
caller without a clock could not build a `Prover`, although no result of a
Prover depends on the time. The drawbacks of RFC-0056 list this field, and
issue 29 asks for a Prover without a clock.

## Decision

We will make `Config.Clock` optional, and `New` will refuse only a
Capacity that is not positive. A cache without a Clock does not expire its
entries:

- `Set` panics for an expiry time other than zero, and the panic leaves
  the cache unchanged.
- `Expire` returns 0.

We will build every cache in core whose entries do not expire without a
clock, and remove `ProverConfig.Clock`. This removal supersedes the Clock
that RFC-0056 requires of a `ProverConfig`.

## Alternatives Considered

### An optional ProverConfig.Clock

`NewProver` would accept a nil Clock and pass it to the cache.

Rejected. The field would configure nothing. Version 0.11.0 released the
Prover on 2026-10-11, so its removal breaks the fewest callers now.

### A clock that the Prover builds for its cache

`NewProver` would build a clock for its cache, as `crypto/tsp` built
`hlc.New(0)`.

Rejected. The cache would read a clock whose time does not change any
result. Core does not build a clock that its caller does not pass.

### A second constructor or a flag for a cache without expiry

Rejected. A nil Clock already means that the cache has no time to expire
against. A flag beside a non-nil Clock could contradict it.

### A Set that ignores the expiry time in a cache without a Clock

Rejected. A caller that forgot the Clock would store entries that never
expire, and nothing would report the mistake.

## Consequences

**Positive:**

- A caller without a clock builds a `Prover`.
- `crypto/tsp` builds no clock, and `semconv.NewDurations` takes no clock.
- A cache without a Clock does not read a clock.

**Negative:**

- A cache that needs expiry and lacks its Clock fails at its first `Set`
  with an expiry time, not at `New`.
- The removal of `ProverConfig.Clock` breaks every caller that sets the
  field.

**Neutral:**

- A cache with a Clock behaves as before.
- RFC-0049, which is a Draft, still describes a required Clock.

## References

- Issue 29, <https://github.com/thesm-os/core/issues/29>.
- RFC-0049, the bounded cache.
- RFC-0056, proofs from cached node tiles, and its drawback of
  `ProverConfig.Clock`.
- `cache/cache.go`: `Config`, `New`, `Set` and `Expire`.
- hashicorp/golang-lru v2.0.7: `simplelru.NewLRU` takes no time source,
  and `expirable.NewLRU` reads `time.Now`
  (`expirable/expirable_lru.go:123`, `:155`, `:181` and `:240`).
