---
rfc: 0022
title: Keyed Storage
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Withdrawn
created: 2026-08-03
updated: 2026-08-03
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0008
---

# RFC-0022: Keyed Storage

## Summary

`store` — a durable keyed-storage seam parameterised over the key type,
with compare-and-swap supplied by the existing `version` vocabulary and
enumeration, streaming and prefix listing as optional capability
interfaces. Ships a typed wrapper that takes encode and decode
functions directly rather than defining a serialisation interface, a
read-modify-write helper, and an in-memory implementation so the
conformance suite has a subject.

## Motivation

Nothing in `core` describes durable storage, so every caller that
stores something defines its own `Get`/`Put`/`Delete` and its own
answer for concurrent writes.

`core` already owns the vocabulary those answers need. `version.Version`
is the opaque CAS token. `version.WriteOptions` carries `IfMatch` and
`IfNoneMatch`. `version.Versioned[T]` is the read-your-writes pair.
`page.Page` and `page.Cursor[T]` are the pagination shape. All of it
exists; none of it has an operation that uses it. The nouns are defined
and the verbs are missing.

The cost of the gap is not that each caller writes three method
signatures. It is that each caller decides, separately and invisibly,
what a stale conditional write returns, whether an absent key is an
error or a zero value, and whether a listing is ordered. Those
decisions end up in retry loops, and a retry loop built on the wrong
answer corrupts data rather than failing.

## Detailed design

```go
// Package store is the durable keyed-storage seam.
//
// store models storage, not any particular store: no TTLs, no
// counters, no queries, no transactions. Callers needing those declare
// a superset interface embedding KV. The shared subset is what
// adapters can be written against once.
package store

// KV is durable storage for opaque byte values under keys of the
// caller's choosing, with compare-and-swap supplied by
// [version.WriteOptions].
//
// K is the caller's key type and the implementation is indifferent to
// it: KV[crypto.Digest] is content-addressed storage, KV[id.ID] is
// identifier-addressed, KV[string] is name-addressed.
//
// # Concurrency
//
// Implementations must be safe for concurrent use.
type KV[K comparable] interface {
    // Get returns the value under key with the version that produced
    // it, for a later conditional write. An absent key returns
    // [ErrNotFound].
    Get(ctx context.Context, key K) (version.Versioned[[]byte], error)

    // Put stores value under key subject to opts, returning the new
    // version.
    //
    // With opts.IfMatch set, returns [version.ErrMismatch] when the
    // stored version differs. With opts.IfNoneMatch set to
    // [version.Wildcard], returns [version.ErrExists] when the key is
    // present. An unconditional Put overwrites.
    Put(ctx context.Context, key K, value []byte, opts version.WriteOptions) (version.Version, error)

    // Delete removes key subject to opts. An absent key returns
    // [ErrNotFound]; a stale IfMatch returns [version.ErrMismatch].
    Delete(ctx context.Context, key K, opts version.WriteOptions) error
}
```

### One seam, parameterised

The key type is the caller's decision and the implementation is
indifferent to it, so it is a type parameter rather than three
interfaces. Content-addressed, identifier-addressed and name-addressed
storage are one contract with different `K`.

Defining a separate interface per key shape would bake a key encoding
into a seam that has no business holding an opinion about keys, and
would triple the adapter surface for no gain.

What genuinely varies is whether a value is transferred whole or
streamed. That is a difference in what the implementation must *do*,
not in what the caller wants, so it is a separate method set.

### `ErrNotFound` is declared, not merely classified

```go
// store/errors.go

// ErrNotFound reports that the addressed key does not exist.
// Classifies as [errs.NotFound].
var ErrNotFound = errors.New("store: key not found")
```

An earlier shape of this RFC left absence as a classification only —
"an absent key classifies as `errs.NotFound`" — with no sentinel. That
is weaker than the treatment `version.ErrMismatch` and
`version.ErrExists` get two paragraphs away, for no reason: a caller
who knows it is talking to a store wants `errors.Is(err,
store.ErrNotFound)`, and generic middleware still gets the class
through `errs.Classify`. Declaring the sentinel serves both; declaring
only the class serves one.

`Delete` of an absent key returns `ErrNotFound` rather than succeeding
silently. The alternative — idempotent delete — is defensible, and it
is rejected because a caller who wants idempotency writes one
`errors.Is` and a caller who wanted to know cannot recover the
information that was discarded.

### Capability interfaces

```go
// Streamer is the optional capability for values too large to hold
// whole. Implementations that cannot stream simply do not satisfy it,
// and callers needing streaming discover that at wiring time.
type Streamer[K comparable] interface {
    KV[K]

    // GetStream opens the value for reading; the caller closes it.
    GetStream(ctx context.Context, key K) (io.ReadCloser, version.Version, error)

    // PutStream stores the value, consuming r to EOF.
    PutStream(ctx context.Context, key K, r io.Reader, opts version.WriteOptions) (version.Version, error)
}

// Lister is the optional capability for implementations that can
// enumerate their own keyspace. Many cannot, and requiring it would
// exclude them.
//
// The enumeration order is UNSPECIFIED. Implementations may return
// keys in any order, and the same implementation may return different
// orders on different calls. Callers needing order sort what they
// receive.
//
// Lister is enumeration. Anything selecting on value contents is a
// query, and store does not model queries.
type Lister[K comparable] interface {
    List(ctx context.Context, p page.Page) (page.Cursor[K], error)
}

// PrefixLister narrows enumeration by key prefix. It is constrained to
// string-shaped keys because prefix has no meaning over an arbitrary
// comparable type: a [16]byte key is comparable but not ordered, so
// "keys beginning with" is undefined for it.
//
// The order is unspecified, as it is for [Lister].
type PrefixLister[K ~string] interface {
    ListPrefix(ctx context.Context, prefix K, p page.Page) (page.Cursor[K], error)
}
```

`Streamer` rather than `Streaming`, so all three capability interfaces
are `-er` names and read alike in a type assertion.

A contract some correct implementations cannot satisfy is the wrong
contract. Enumeration and streaming are both capabilities a backend can
genuinely lack, so each is an optional interface the caller
type-asserts, and the gap surfaces at wiring time rather than as a
runtime error on the first request.

### Enumeration order is unspecified

Every backend can honour "no order". Lexicographic order excludes
hash-partitioned and sharded backends from implementing `Lister` at
all, and those are the backends most likely to hold a keyspace worth
enumerating.

The known hazard is stated rather than hidden: the in-memory
implementation will return some order, callers will observe it, and
some will come to depend on it. The conformance suite is the mitigation
— it asserts that a `Lister` is *permitted* to vary, and the in-memory
implementation deliberately randomises its enumeration order so that a
caller depending on stability fails against the double rather than
against production.

### Typed access without a codec seam

Adapters implement the byte-oriented interface because that is all they
can implement. Callers want typed values.

```go
// Typed wraps a [KV] with encode and decode functions, giving typed
// access over a byte-oriented store.
//
// Typed exists so that core need not define a serialisation seam: the
// functions are supplied at construction, so callers choose their own
// encoding without core holding an opinion or an interface.
type Typed[K comparable, V any] struct{ ... }

func NewTyped[K comparable, V any](
    kv KV[K],
    enc func(V) ([]byte, error),
    dec func([]byte) (V, error),
) *Typed[K, V]

func (t *Typed[K, V]) Get(ctx context.Context, key K) (version.Versioned[V], error)
func (t *Typed[K, V]) Put(ctx context.Context, key K, v V, opts version.WriteOptions) (version.Version, error)
func (t *Typed[K, V]) Delete(ctx context.Context, key K, opts version.WriteOptions) error

// Update runs the read-modify-write loop that [version.Versioned]
// documents, retrying on [version.ErrMismatch] until it succeeds or
// ctx ends. fn may be called more than once and must be free of side
// effects.
func Update[K comparable, V any](
    ctx context.Context,
    t *Typed[K, V],
    key K,
    fn func(V) (V, error),
) error
```

`Update` is a package-level function because Go methods cannot take
type parameters, and because the retry loop is the operation callers
actually want. Writing it by hand is where optimistic concurrency goes
wrong: the common error is re-applying to the stale value rather than
re-reading, which produces a lost update that no test catches.

### The in-memory implementation

`store/memory` ships in-module: a `KV[K]` over a map, satisfying
`Lister` and not `Streamer`.

It exists for two reasons. The conformance suite needs a subject, and a
seam whose conformance suite has nothing to run against is a suggestion.
And a caller testing code that stores something needs a double that
honours the CAS semantics exactly, which a hand-rolled map in a test
file will not.

It performs no IO, so it does not contradict ADR-0008's rule that
implementations live in consumer modules.

### Conformance

`coretest/storetest` is the load-bearing suite in this RFC. Its
assertions are the difference between a seam and a suggestion:

- `IfMatch` on a stale version returns `version.ErrMismatch`.
- `IfNoneMatch: Wildcard` on a present key returns `version.ErrExists`.
- An absent key returns `ErrNotFound` and classifies as
  `errs.NotFound`.
- `Delete` of an absent key returns `ErrNotFound`.
- A version returned by `Put` is accepted by a subsequent `IfMatch`.
- A `Cursor` drains exactly once and reports its error through the
  iteration, not out of band.
- `Update` converges under concurrent writers.

## Alternatives considered

### A. Separate interfaces per key shape

`ContentStore`, `KeyValueStore`, `NamedStore`.

**Why not:** three contracts that differ only in one type, tripling the
adapter surface and forcing `core` to decide how each key encodes. The
type parameter says the same thing and leaves the encoding to the
implementation that has to perform it.

### B. A `Codec` interface for typed access

Define `Codec[V]` with `Encode`/`Decode` and take one in `NewTyped`.

**Why not:** Go satisfies interfaces structurally, so an interface here
adds a name without adding composition. Two function values do the same
work, compose with closures, and let a caller use an encoding whose
functions do not sit on a single type.

### C. Guarantee lexicographic enumeration order

**Why not:** it excludes hash-partitioned and sharded backends from
implementing `Lister` at all, which are exactly the backends whose
keyspaces are large enough to need pagination.

### D. Include queries or transactions

**Why not:** any predicate over value contents is domain vocabulary, and
a seam that accepted one would be accepting an expression language.
Transactions have no shape that a key-value cache and a relational
database can both honour, so a common interface would be satisfiable by
neither honestly.

### E. Return a zero value rather than an error for an absent key

**Why not:** it makes "absent" and "stored empty value" indistinguishable
in a seam whose values are opaque bytes, where the empty value is
legitimate.

## Drawbacks

- `Typed` and `Update` are conveniences, not primitives, and they widen
  the package beyond the seam. Both are justified by the same argument
  — that hand-written versions get the retry wrong — and that argument
  could be made for a great many helpers.
- Four interfaces plus a wrapper plus a function is a large surface for
  one RFC, and the capability interfaces multiply with the key
  parameter in a way that reads poorly in godoc.
- Unspecified enumeration order is correct and unhelpful. Callers will
  depend on incidental order despite the randomised double, because the
  double is not what they run in production.
- `PrefixLister`'s `~string` constraint excludes byte-slice-shaped keys
  that are ordered in practice.
- An in-memory subject cannot exercise latency, partial failure, or
  reconnection, which is where storage adapters actually break.
- `store` now declares a sentinel that duplicates information available
  from `errs.Classify`, so a producer must remember to satisfy both.

## Open questions

None. The three questions this RFC previously carried are resolved
above: `ErrNotFound` is declared alongside its classification;
enumeration order is unspecified, with a randomising double as the
mitigation; and `Delete` of an absent key returns `ErrNotFound` rather
than succeeding.

## Unresolved / future work

- A batching capability interface for multi-key get and put.
- TTL as a capability interface, if a shape emerges that a relational
  store and a cache can both honour.
- Whether `store` should define a watch or change-feed capability.

---

## Withdrawal

Withdrawn 2026-08-03, before acceptance. Nothing was implemented.

**One interface was the wrong unit.** `KV` is the intersection of a
relational store, an object store and a cache, and what an
intersection discards is precisely what each member is good at:
transactions and queries, ranges and multipart, TTLs and atomics. This
RFC's own answer — "callers needing those declare a superset interface
embedding KV" — is the tell. If most callers declare the superset, the
subset bought a name.

It is also the shape this module rejects everywhere else. `crypto`
does not define one `Crypto` interface with capability sub-interfaces;
it defines `Hasher`, `MAC`, `AEAD`, `XOF` and `Keeper` as separate
seams, because they are separate primitives. Storage kinds are
separate primitives too, and the differences are contractual rather
than optional:

- A **cache** miss is the expected case. Modelling absence as
  [ErrNotFound] — an error — is backwards for the one kind where
  absence is normal, and no capability interface fixes that.
- A **blob** is streamed by nature. Making streaming an optional
  capability inverts which case is the default.
- A **database** has queries and transactions, which are the whole
  point and which this RFC explicitly refuses to model.

**Two of the kinds are not core's at all.** `database/sql` is already
the database seam, and defining a second repeats the mistake ADR-0009
avoided for logging. Anything document- or query-shaped needs an
expression language, which is domain vocabulary by construction.

**What survives.** The motivation stands: `version.Version`,
`WriteOptions`, `Versioned[T]`, `page.Page` and `page.Cursor[T]` are
shipped and no operation consumes them, so every caller still
re-decides what a stale conditional write returns and whether an
absent key is an error. Those decisions still land inside
optimistic-concurrency loops where a wrong answer corrupts rather than
fails.

The replacement is per-kind seams — `blob` is the best-anchored, since
[io/fs] establishes the read side and the write side is the
acknowledged gap — each argued on its own terms and shipped when a
real backend has been pushed through it. None is written yet, and none
should be written speculatively.

**Why not simply narrow this RFC.** Because the parts worth keeping —
`Update`'s read-modify-write loop, the error vocabulary — do not
depend on the interface, and the parts that do depend on it are the
ones under question. Narrowing would have left the shape half-decided
in an accepted document.
