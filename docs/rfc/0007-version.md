---
rfc: 0007
title: Version — Opaque CAS Token
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-05-06
updated: 2026-05-06
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0007: Version — Opaque CAS Token

## Summary

`Version`, an opaque per-(scope, key) token returned by reads
and accepted by writes for read-your-writes and compare-and-
swap semantics. Plus `WriteOptions` carrying the per-call
preconditions and `Versioned[T]` wrapping a value with the
version that produced it.

## Motivation

Storage adapters today return optimistic-concurrency tokens
under three different shapes:

- A bare `int64` row version.
- An ETag string formatted by the adapter.
- A composite `(string, time.Time)` pair embedded in the
  caller's domain types.

When a consumer wants to swap one storage adapter for another,
or when two adapters must interoperate (cache + primary store,
backup + restore), the type mismatch surfaces everywhere.
Promoting the shape into core gives every storage adapter a
single contract: `Version` tokens are opaque, comparable
bytewise, and round-trip losslessly.

## Detailed design

```go
type Version string
const Unspecified Version = ""
const Wildcard    Version = "*"

func (Version) IsZero() bool
func (Version) IsWildcard() bool

type WriteOptions struct {
    IfMatch     Version
    IfNoneMatch Version
}
func (WriteOptions) IsConditional() bool

type Versioned[T any] struct {
    Value   T
    Version Version
}
```

### Encoding contract

Implementations encode their native version representation to
the `Version` string deterministically and losslessly:

- **Deterministic:** the same internal version always produces
  byte-identical `Version` output. CAS comparisons are
  bytewise; any non-determinism (timestamp formatting, map
  iteration order, locale-dependent number formatting) silently
  breaks IfMatch correctness.
- **Lossless:** the encoded `Version` round-trips back to the
  same internal version on parse. Versions returned to callers
  and later supplied as IfMatch must select the exact same
  historical state — losing precision (truncating a vector
  clock to a single counter) silently breaks linearizability
  under concurrent writes.

Implementations that wrap structured native versions typically
encode as canonical JSON or fixed-width hex; bare counters
encode as decimal; content hashes encode as their canonical
hex / base64 form.

### High-water-mark / ABA prevention

`Version` MUST be globally unique within a (scope, key) pair
across all time, including deletion cycles. If key K is
created (Version v1), deleted, then recreated, the recreated
key's first `Version` v2 MUST NOT equal v1 or any `Version` K
ever held before deletion.

The requirement prevents the ABA problem: a stalled worker
holding a `Version` from before deletion attempts a conditional
write (IfMatch: v1) against the recreated key. Because v2 ≠ v1,
the write correctly fails. Without this requirement, the
stalled worker silently clobbers resurrected state — a
correctness-critical bug in distributed systems where workers
may outlive the keys they cached.

### Versioned[T]

`Versioned[T]` is the canonical return type of state-bearing
reads when callers need conditional decisions on top of the
read:

```go
cur, err := store.Get(ctx, key)               // Versioned[T]
next := transform(cur.Value)
err  = store.Put(ctx, key, next, WriteOptions{IfMatch: cur.Version})
```

The Put fails if a concurrent writer changed the value between
Get and Put.

## Alternatives considered

### A. `Version` as `int64` or `uint64`

Bare counter.

**Rejected:** forces every adapter to encode their native
representation as an integer. Content-hash-based stores
(IPFS-shaped, hash-keyed CAS) can't represent their version
naturally. Vector-clock stores can't encode multi-component
state. The opaque string form covers every native encoding
losslessly.

### B. Version with embedded `time.Time` for ordering

Pair the opaque token with a comparable timestamp.

**Rejected:** versions are bytewise-compared, not
chronologically-compared. Two writes at the same wall-clock
nanosecond have to be distinguished by something other than
time. Adding a timestamp to every version doubles the wire size
for no correctness gain.

### C. Sentinel errors `ErrMismatch` / `ErrExists` declared in package

Pre-declare the errors storage adapters should return.

**Rejected:** no producer in the package returns them — they
were declarations without producers, the same anti-pattern as
"interface graveyard." Consumers that implement the contract
declare their own sentinels; if a pattern emerges across
multiple consumers, we promote later (non-breaking).

## Drawbacks

- The opaque-string contract puts the encoding burden on every
  adapter implementer. Documented; the contract spells out
  determinism + losslessness as the load-bearing properties.
- `WriteOptions{IfNoneMatch: Wildcard}` for "create if absent"
  uses the magic string `"*"`. ETag-shaped; familiar to
  consumers but worth flagging.
- `Versioned[T]` requires Go 1.18+ generics. Satisfied by the
  Go 1.26 baseline.

## Open questions

- **Sentinel-error promotion.** If `ErrMismatch` and `ErrExists`
  emerge as a shared vocabulary across multiple consumers,
  promote them in a follow-up RFC.

## Unresolved / future work

- Versioned-storage interface contract (`Get`, `Put`,
  `Delete`) when a consumer demonstrates the need for one in
  core. Today every consumer has its own storage interface;
  promoting it requires alignment with multiple consumer
  modules.
