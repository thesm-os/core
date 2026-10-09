---
rfc: 0027
title: Content-Addressed Storage
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-08-05
updated: 2026-08-05
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0012
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0027: Content-Addressed Storage

## Summary

`cas.Store` — storage whose keys *are* the digests of its values.
Three methods: `Put`, which MUST verify that the bound hasher's
digest of the data equals the address and reports whether it wrote;
`Get`; `Has`. A store is bound to exactly one hashing algorithm at
construction, writes are idempotent by construction, and there is no
`Delete` — deletion of content-addressed data is garbage collection,
which is consumer policy. Ships with `cas/memory` as the reference
implementation and `coretest/castest` as the conformance suite that
holds every implementation to the same laws.

## Motivation

Content addressing is how `core`'s consumers already connect bytes
to identity: hash chains anchor on digests, large payloads are
spilled to digest-keyed side stores, and audit paths verify what
they read against the address that requested it. Nothing in `core`
names the store those digests address, so each consumer defines its
own — and the two observed definitions are the argument:

- The ledger's `Blob` is digest-keyed but verifies nothing:
  `PutIfAbsent(d, garbage)` stores garbage under `d` and returns
  success, and that garbage is then served to every holder of `d`.
  Its response carries no signal of whether a write happened, so
  dedup accounting and quota charging cannot exist at the seam.
- Foundation carries the digest *beside* a name-keyed store
  (`BlobHandle.Key` and `BlobHandle.Digest` as explicitly
  independent fields) — evidence that content identity and naming
  are separate concerns, and that a store conflating them serves
  neither.

The storage-kind analysis (ADR-0012) sorts `cas` as its own kind:
absence is an error, values are whole, the content mints the
address, and nothing mutates. Every property downstream of that —
idempotent writes, inherent deduplication, verifiable reads — holds
only if the address-to-content connection is enforced, which is why
the central clause of this RFC is a MUST.

Why here: the address type is `crypto.Digest`, the verification
hasher is `crypto.Hasher`, absence and integrity classify through
`errs`, and fencing composes per RFC-0026. Every noun is `core`'s;
only the verbs are missing.

## Detailed design

### The interface

```go
// Package cas is the content-addressed storage seam: a store whose
// keys ARE the digests of its values, so writes are idempotent,
// deduplication is inherent, and every read is verifiable against
// the address that requested it.
package cas

// Store is content-addressed storage over one hashing algorithm.
//
// The address is a [crypto.Digest] because a CAS address is a
// commitment, not a name. An assigned identifier — an id.ID, a
// string key — is minted by someone and relates to its content by
// convention only; a digest is computed FROM the content, which is
// what makes Put's verification, idempotent writes, and verifiable
// reads expressible at all. A consumer that wants to NAME stored
// content keeps its own (name → digest) record beside this seam.
//
// A Store is bound to exactly one [crypto.Hasher] at construction.
// One address space, one algorithm: a store that admitted two would
// give identical bytes two addresses, and deduplication — half the
// point of content addressing — would silently halve. An address
// produced by any other algorithm fails Put's verification and is
// absent for Get and Has.
//
// The zero [crypto.Digest] is not an address: it is the documented
// "no digest computed" sentinel — see [crypto.Digest.IsZero] — and
// every method
// rejects it with an error classifying as
// [go.thesmos.sh/core/errs.Invalid] before touching storage.
//
// # Fencing
//
// A Store implementation MAY be fenced: the fence epoch binds at
// handle construction and Put validates it atomically with the
// write, per the fence laws documented on
// [go.thesmos.sh/core/epoch.Admissible].
//
// # Concurrency
//
// Implementations must be safe for concurrent use. For concurrent
// Puts of the same address, exactly one reports wrote=true — the
// signal is an accounting primitive, and double-counting it
// double-charges whatever is metered against it.
type Store interface {
    // Put stores data under its digest. It MUST verify that the
    // bound hasher's digest of data equals d, and return an error
    // classifying as [go.thesmos.sh/core/errs.Integrity] — storing
    // nothing — when they disagree. Returns wrote=false, with no
    // error, when the address is already present: re-putting
    // identical bytes is the idempotent no-op that makes CAS
    // retry-safe.
    Put(ctx context.Context, d crypto.Digest, data []byte) (wrote bool, err error)

    // Get returns the bytes stored under d. The returned slice is
    // the caller's: implementations must not alias it to internal
    // storage, and later Puts must not affect it. An absent
    // address returns an error classifying as
    // [go.thesmos.sh/core/errs.NotFound].
    //
    // Implementations MAY re-verify the digest on read; one that
    // documents doing so is held to it by the conformance suite.
    Get(ctx context.Context, d crypto.Digest) ([]byte, error)

    // Has reports presence without transferring the body.
    Has(ctx context.Context, d crypto.Digest) (bool, error)
}
```

### The four decisions

**The method is `Put`, not `PutIfAbsent`.** In a CAS there is no
other put: the same digest is the same bytes, so every put is
if-absent by construction. Naming the mechanism implies a
conditional variant exists; the semantics *are* the condition.

**Write-side verification is MUST.** An unverifying CAS is a KV in
costume: nothing connects address to content, so idempotency,
dedup, and verifiable reads are all assumptions. The cost objection
fails on its own arithmetic — the legitimate writer hashed the
bytes to *produce* the address, so verification is one
recomputation on the write path of a store whose reads dominate.
What the clause buys is the poisoned-cache attack deleted at the
interface: `Put(d, garbage)` cannot succeed, so no holder of `d`
can be served bytes that do not hash to it.

**`Put` reports whether it wrote.** Dedup accounting, quota
charging, and first-writer semantics all need the signal, and every
adapter can supply it — presence is one existence check on the
address it just verified. The exactly-one-`wrote` law under
concurrency is what makes the signal an accounting primitive rather
than a hint.

**There is no `Delete`.** Deletion of content-addressed data is
reference counting or garbage collection — policy over a
reachability set only the consumer knows. A bare `Delete` breaks
the invariant every other holder of an address relies on: that an
address, once minted, resolves. Erasure of *meaning* is the
encryption layer's job — encrypt-then-store and shred the key, the
crypto-shred pattern [crypto.Destroyer] already serves and the
ledger already practises.

Listing, TTLs, and metadata are refused on the kind test: a digest
space has no prefix structure to enumerate, expiry contradicts
address-implies-availability, and content identity paired with
names or provenance is consumer vocabulary.

### The reference implementation

```go
// Package memory provides an in-process [cas.Store] for tests and
// local development. It is the conformance suite's first subject.
package memory

// New returns an empty Store verifying against h.
func New(h crypto.Hasher) *Store
```

`memory.Store` is a mutex-guarded map keyed by the digest's bytes,
cloning on Get. It exists so `castest` has a subject inside `core`'s
own gates (ADR-0005's conformance bar), and so consumers have a
working store for wiring tests — the role `crypto/localkey` plays
for key custody.

### Conformance: `coretest/castest`

```go
// In package coretest/castest.

// AssertStore drives a Store implementation through the CAS laws.
// newStore returns an empty store bound to the given hasher; the
// suite constructs one per case.
//
// Asserted: put-get round-trips bytes exactly, and the returned
// slice is independent of later writes; re-put of identical bytes
// reports wrote=false with no error; a put under a wrong digest
// classifies as errs.Integrity and stores nothing — rejection
// without mutation; a put under a digest of a foreign algorithm
// fails the same way rather than storing under a foreign address
// space; Has agrees with Get on presence and absence; absence
// classifies as errs.NotFound; the zero digest is rejected with
// errs.Invalid by all three methods; and N concurrent puts of one
// address yield exactly one wrote=true.
func AssertStore(
    t *testing.T,
    newStore func(h crypto.Hasher) cas.Store,
)
```

### Worked example

A consumer spills a large payload: it hashes the ciphertext with
the store's hasher, calls `Put(d, ct)`, and records `d` in its
chain. A corrupted or hostile writer attempts `Put(d, other)`: the
store recomputes, the digests disagree, `errs.Integrity` comes
back, and nothing is stored — every holder of `d` continues to read
bytes that hash to `d`. A retry of the original Put after a timeout
reports `wrote=false`: the bytes are already there, no quota is
re-charged, and the operation was idempotent end to end. A reader
holding `d` calls `Get`, hashes what it receives, and can verify
independently — the address is the proof.

### Edge cases

- **Zero digest** — rejected with `errs.Invalid` by all three
  methods, before storage is touched. It is a sentinel, not an
  address, and letting it reach the map would mint an address with
  no content relation.
- **Foreign-algorithm digest** — `Put` verification fails
  (`errs.Integrity`); `Get`/`Has` simply find nothing. A SHA3-256
  digest of bytes stored under SHA-256 is a different address in a
  different space.
- **Empty data** — legal. The digest of zero bytes is a valid
  address; `Put(digest(""), nil)` stores it and round-trips as an
  empty slice.
- **Context cancellation** — returns the context's error; a
  cancelled Put either wrote fully or not at all, per rejection
  without mutation.

There is no migration: the package is new surface.

## Alternatives considered

### A. `PutIfAbsent` naming

The ledger's spelling.

**Why not:** it names the mechanism as if an unconditional variant
existed. In content addressing the condition *is* the semantics;
the shorter name states the stronger fact.

### B. Verification as MAY

The ledger's practice (no verification, empty response) softened
to an option.

**Why not:** every downstream property of a CAS rests on the
address-content connection, and a MAY makes the whole family
conditional on adapter diligence. The attack it permits — serving
unrelated bytes under a trusted address — is silent and spreads to
every reader. MAY survives only on the read path, where the write
path has already enforced the invariant once.

### C. `Delete` on the seam

Foundation's name-keyed store has it; storage seams usually do.

**Why not:** an address is a promise to every holder, and the seam
cannot know the holders. Deletion needs a reachability policy —
refcounts, GC roots, retention — which is consumer vocabulary. A
consumer that needs erasure of meaning encrypts and shreds keys; a
consumer that needs space reclamation builds GC over `Has` and its
own root set.

### D. Streaming `Put` over `io.Reader`

The blob shape, applied here.

**Why not:** the address is the digest of *all* the bytes, so the
writer must have hashed them before it can name the destination — a
streaming Put would hand the store a reader the caller was already
obliged to materialise. Consumers with objects too large to hold
whole chunk them and address the chunks; the chunking scheme
(Merkle layout, chunk size) is protocol vocabulary above this seam.

### E. Multi-algorithm stores

One store admitting addresses from several hashers, discriminated
by digest size.

**Why not:** size does not discriminate algorithm (SHA-256 and
SHA3-256 are both 32 bytes), identical bytes acquire one address
per algorithm — halving dedup — and verification needs to know
which hasher to recompute with. Algorithm rotation is a second
store and a re-put, priced honestly in Drawbacks.

### F. Digest-plus-metadata records

Storing content type, size, or provenance beside the bytes.

**Why not:** foundation's `BlobHandle` is the evidence that the
pairing is consumer vocabulary — its digest and its metadata travel
in the consumer's own value type. The store's contract is bytes at
an address; everything else varies per consumer and would turn the
seam into a record schema.

## Drawbacks

- Every write hashes twice ecosystem-wide: once by the writer to
  mint the address, once by the store to verify. For a 1 MiB value
  on SHA-256 that is roughly an extra half millisecond per put;
  adapters fronting stores with hardware-offloaded or ingestion-time
  verification pay it without benefit.
- Whole-value transfer puts a memory ceiling on a single address.
  A multi-gigabyte object cannot be CAS'd directly; consumers must
  chunk, and the chunking scheme becomes their protocol surface.
- No enumeration means no seam-level garbage collection, by
  design — but it also means an operator cannot ask a store what it
  holds through the seam. Auditing a store's contents is an
  adapter-level operation.
- One algorithm per store makes hash rotation a data migration:
  construct the successor store, re-put, re-record addresses.
  `crypto.Algorithm` travels with persisted digests (RFC-0014
  discipline), so the *artefacts* survive rotation; the store
  contents must be re-addressed.
- The exactly-one-`wrote` law under concurrency forces adapters
  onto an atomic existence-check-and-write; on some backends that
  is a conditional put with a round trip the unconditional write
  would not pay.

## Open questions

None. The contested calls — MUST-verify, no Delete, whole-value —
were settled in the design spec this RFC compiles, and their
counter-arguments are recorded above at full strength.

## Unresolved / future work

- A chunked/Merkle layer above `cas` for objects beyond memory, if
  a consumer materialises the need; it composes over this seam
  without changing it.
- Read-side verification as a named capability with its own
  conformance assertion, if paranoid-read consumers appear.
