---
rfc: 0028
title: Named Object Storage
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-08-05
updated: 2026-08-05
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0028: Named Object Storage

## Summary

`blob.Store` — caller-keyed objects, streamed in both directions,
with conditional writes supplied by the existing `version`
vocabulary. It is the write half `io/fs` never defined. Five
methods — `Put`, `Get`, `Stat`, `Delete`, `List` — under three laws
practice leaves implementation-defined: visibility is atomic even
where the upload is not, an opened body is one consistent object
version, and a listing cursor walked to exhaustion over a quiescent
store is complete. Stale conditional writes return
`version.ErrMismatch` and create-only collisions return
`version.ErrExists` — no new sentinels. Ships with `blob/memory` as
the reference implementation and `coretest/blobtest` as the
conformance suite.

## Motivation

Named object storage is the storage kind every consumer touches —
service fronts S3, GCS, and Azure behind its own `blob.Provider`,
foundation defines a `BlobStore` of its own, and the two agree on
the shape (string keys, streamed bodies, metadata without body,
prefix listing) while disagreeing on every semantic that matters:

- **Absence:** foundation's `Get` returns its own
  `ErrBlobNotFound`; conditional failures return its own
  `ErrBlobVersionMismatch`. Both respell vocabulary `core` already
  owns, which is the divergence a seam exists to end.
- **Partial writes:** neither surveyed contract says what a reader
  observes while an upload is in flight or after one fails. A
  reader that can observe a truncated body turns every consumer
  into a defensive re-verifier, and whether that is possible is
  today a per-backend surprise.
- **Listing:** foundation states no ordering or completeness
  properties at all. Pagination without a completeness law is a
  loop that silently skips or duplicates under exactly the
  conditions nobody tests.

The kind test (ADR-0012) sorts `blob` as its own kind: absence is
an error, bodies stream, the caller mints the key, and anything may
change under proof-guarded writes. Streaming is the discriminator
against `cas` (RFC-0027): the address there is computed from all
the bytes, so values are whole; here the key is independent of
content, so a body can flow through without ever being held.

Why here: keys are strings, proofs are `version.Version`, pages are
`page.Page`, timestamps are stdlib `time.Time`, absence classifies
through `errs`, and fencing composes per RFC-0026. Every noun is
`core`'s; only the verbs are missing — and this is the second kind
the ADR-0012 charter names, after `cas`.

## Detailed design

### The interface

```go
// Package blob is the named-object storage seam: caller-keyed
// objects, streamed in both directions, with conditional writes
// supplied by the version vocabulary. It is the write half io/fs
// never defined.
package blob

// Info is an object's metadata, without its body.
//
// Info carries no digest. Content identity paired with a name is
// consumer vocabulary: a consumer that wants verified blobs
// composes this seam with [go.thesmos.sh/core/cas], or hashes at
// its own boundary and records (name → digest) in its own types.
type Info struct {
    // Key is the object's name, as the caller minted it.
    Key string

    // Size is the body's byte length; -1 when the backend cannot
    // report it without reading the body.
    Size int64

    // Version is the proof token for conditional writes, per the
    // version package's contract: equality-only, unique across
    // deletion cycles.
    Version version.Version

    // ModTime is the backend's record of the last write. It is
    // stdlib time rather than an instant: an external fact
    // reported by storage is not an event in this deployment's
    // causal chain, which is the distinction clock.Instant's own
    // documentation draws. On the Info returned by Put it MAY be
    // zero; Get and Stat MUST report it.
    ModTime time.Time

    // ContentType is carried opaquely: stored as given on Put,
    // returned as stored, never inferred. Empty means unspecified.
    ContentType string
}

// PutOptions carries a Put's metadata and preconditions.
type PutOptions struct {
    // ContentType is stored with the object and round-trips on
    // Get and Stat. Empty means unspecified.
    ContentType string

    // Write carries the conditional-write preconditions. The zero
    // value is an unconditional overwrite. IfMatch failing returns
    // version.ErrMismatch; IfNoneMatch failing returns
    // version.ErrExists.
    Write version.WriteOptions
}

// Store is named object storage.
//
// A key satisfies ValidKey: an io/fs.ValidPath, excluding the
// root, at most 1024 bytes with elements of at most 255. One that
// does not classifies as errs.Invalid on every method. Nesting is
// unrestricted — a store may hold "a/b" and "a/b/c" — and a
// backend whose namespace cannot hold both encodes around it.
// Prefix narrowing in List is byte-prefix and is not governed by
// ValidKey; a prefix may end part-way through an element.
//
// # Fencing
//
// A Store implementation MAY be fenced: the fence epoch binds at
// handle construction and every write validates it atomically,
// per the fence laws documented on
// [go.thesmos.sh/core/epoch.Admissible].
//
// # Concurrency
//
// Implementations must be safe for concurrent use. Concurrent
// writers to one key are serialised by the backend in some order;
// conditional writes are how a caller makes that order matter.
type Store interface {
    // Put stores the object under key, consuming r to EOF.
    //
    // Visibility is atomic even where the upload is not: a Put
    // that fails for ANY reason — a reader error mid-stream,
    // cancellation, a failed precondition — MUST leave the key
    // exactly as it was, and a concurrent Get during a Put MUST
    // observe the previous object or its absence, never a
    // truncated body. The returned Info describes the object as
    // written, including its new Version.
    Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (Info, error)

    // Get opens the object for reading; the caller closes the
    // reader. The bytes read are one consistent object — the
    // version named by the returned Info — even if the key is
    // overwritten while the reader is open. Absence classifies as
    // errs.NotFound.
    Get(ctx context.Context, key string) (io.ReadCloser, Info, error)

    // Stat returns metadata without the body. Absence classifies
    // as errs.NotFound.
    Stat(ctx context.Context, key string) (Info, error)

    // Delete removes the object, subject to ifMatch.
    //
    // The zero version deletes unconditionally, and an
    // unconditional Delete of an absent key succeeds: the caller's
    // intent — that it be gone — holds. A non-zero ifMatch naming
    // any other version, including against an absent key, returns
    // version.ErrMismatch. The parameter is one version rather
    // than the full WriteOptions because a create-only
    // precondition on a removal is a category error, and a
    // parameter that accepts one in order to reject it is a
    // mistake the caller can write.
    Delete(ctx context.Context, key string, ifMatch version.Version) error

    // List enumerates objects whose keys begin with prefix, one
    // page per call; the empty prefix enumerates everything.
    //
    // Order is unpromised, but a cursor chain is complete: over a
    // store with no concurrent mutation, walking pages to
    // exhaustion yields every matching object exactly once. Under
    // concurrent mutation, objects present for the walk's whole
    // lifetime are yielded exactly once; objects created or
    // deleted mid-walk may or may not appear.
    List(ctx context.Context, prefix string, p page.Page) (page.Cursor[Info], error)
}
```

### The three laws, and why each is contract rather than adapter choice

**Atomic visibility on Put.** Without it, every reader must treat
every body as possibly truncated, and the defence — re-verify
everything — is exactly the tax `cas` exists to make optional, now
paid on the kind that cannot verify. Adapters implement it the way
their backend affords: object stores commit on complete, filesystem
adapters write-then-rename, databases transact. The law names the
observable, not the technique. *Falsifier:* the suite Puts through
a reader that errors halfway and asserts the key's state — body and
Info — is bitwise the pre-Put state; then repeats with a
precondition failure and with a cancelled context.

**One consistent body per open reader.** The read-side half of the
same coin: a reader holding a body stream must never observe two
versions interleaved. S3-family backends give this for free; a
naive filesystem adapter that re-reads an overwritten file does
not, and the law is what forces that adapter to open-then-stream a
snapshot. *Falsifier:* open a reader, overwrite the key, drain the
reader — the bytes must be the version the open-time Info named.

**Cursor completeness over quiescence.** Between "lexicographic
order, always" (exiles every backend that cannot serve it) and "no
promises" (the anti-pattern), the law pagination actually needs:
exhaustive, exactly-once, when nothing mutates. The concurrent
clause is deliberately the weakest honest statement — stable
objects appear exactly once; churn may or may not — because
anything stronger is a snapshot-isolation promise most backends
cannot keep. *Falsifier:* populate N objects, walk cursors at page
sizes 1, 2, and N+1, assert the union is exact; narrow by prefix
and assert the complement never leaks.

### Errors, uniformly

| condition | outcome |
|---|---|
| absent key on `Get`/`Stat` | classifies `errs.NotFound` |
| `IfMatch` failed on `Put`/`Delete` | `version.ErrMismatch` |
| `IfNoneMatch` failed on `Put` | `version.ErrExists` |
| key fails `ValidKey`, any method | classifies `errs.Invalid` |
| ctx done | the context's error |

No new sentinels. The proof axis already has its vocabulary, and
foundation's `ErrBlobNotFound`/`ErrBlobVersionMismatch` pair is the
respelling this table replaces.

### The reference implementation

```go
// Package memory provides an in-process [blob.Store] for tests and
// local development. It is the conformance suite's first subject.
package memory

// New returns an empty Store reading instants from c.
func New(c clock.Clock) *Store
```

Bodies buffer fully before the atomic swap, which makes atomic
visibility and reader snapshots trivial; versions come from a
monotonic counter that never reuses a value across deletions, per
the version package's uniqueness requirement. `List` snapshots one
page of matching keys per call, in key order so the continuation
token — the last key of a truncated page — is stable across calls;
an untruncated page, even an exactly-full one, carries no token,
so a walk never pays a spurious empty round trip. The clock is
injected so a test drives `ModTime` deterministically, and this
module does not read wall time ambiently.

### Conformance: `coretest/blobtest`

`AssertStore(t, newStore func(c clock.Clock) blob.Store)` drives
the laws: streamed round-trip with `ContentType` and `Size`
fidelity; unconditional overwrite changing `Version`; `IfMatch`
success and stale rejection; `IfNoneMatch` create-once, `ErrExists`
on collision, and the specific-version form rejecting exactly the
version it names; the delete table above; atomic visibility
under a failing reader, a failed precondition, and a cancelled
context; reader snapshot under overwrite; cursor completeness at
page sizes 1, 2, and N+1 with and without prefix; rejection of
every key shape `ValidKey` refuses, on every method; a nested key
coexisting with its own prefix; and Version uniqueness across
delete-and-recreate — the ABA requirement inherited from `version`,
asserted here because a filesystem-style adapter synthesizing
versions is exactly where it breaks.

The suite compares `Key`, `Version`, `Size` and `ContentType`
between what `Put` reports and what `Get` and `Stat` report, and
requires only that `ModTime` is set on the read side. A
whole-struct comparison would oblige an adapter over a backend that
reports no timestamp on write to read the object back once per
write, to populate a field the comparison did not mean to assert.

### Edge cases

- **Put of an empty body** is legal: a zero-length object with
  metadata is a real object, distinct from absence.
- **`Size` on `Put`'s returned Info** is the byte count consumed
  from `r`, never -1: the store just read the body, so it knows.
- **Concurrent conditional writers** race on the backend's
  serialisation; the loser's `IfMatch` names a superseded version
  and fails with `ErrMismatch` — the optimistic-concurrency loop
  documented on `version.Versioned`, unchanged.
- **`Get` immediately after a failed `Put`** observes the previous
  object with its previous `Version` — asserted, not implied, by
  the atomic-visibility falsifier.

There is no migration: the package is new surface.

## Alternatives considered

### A. Whole-value `[]byte` bodies

The `cas` shape, applied here.

**Why not:** blobs are streamed by nature — the kind's own
discriminator. A whole-value seam puts a memory ceiling on every
object and forces the streaming adapters everyone actually runs
(S3, GCS) to buffer what their SDKs already stream. `cas` holds
values whole because its address requires hashing every byte first;
no such obligation exists here.

### B. A digest in `Info`

Verified blobs at the seam.

**Why not:** it drags a hashing obligation into every adapter for a
property only some consumers want, and it pairs naming with
commitment — the conflation both surveyed codebases already avoid
(foundation carries key and digest as independent fields in its own
handle type). Composition with `cas` or consumer-side hashing costs
nothing and keeps the obligation where the want is.

### C. Batch-native methods

Foundation's shape: slice-taking `Delete`, batch reads.

**Why not:** foundation's own contract concedes the argument — its
`Put` and `Get` are scalar "because each blob carries its own byte
stream; 'batch Put' is not a meaningful shape when the transport is
a sequence of streaming uploads". Batching the metadata operations
is a transport optimisation adapters may exploit beneath the seam;
surfacing it would make the seam's shape follow one backend
family's wire API.

### D. An `io/fs.FS` read side

Serve reads through the standard filesystem abstraction.

**Why not:** `fs.FS` has no write half, and its `FileInfo` cannot
carry `Version` — the field conditional writes revolve around. An
adapter can offer an `fs.FS` *view* for consumers that want one;
the seam cannot be one without losing its central mechanism.

### E. Kind-specific sentinels

Foundation's `ErrBlobNotFound` and `ErrBlobVersionMismatch`.

**Why not:** both respell vocabulary that exists — absence is a
class, staleness is `version.ErrMismatch` — and every respelling
is a seam consumers must translate at. The error table above is
the whole surface, and none of it is new.

### F. TTLs and lifecycle options

Expiry policies on `PutOptions`.

**Why not:** expiry is policy over a store, not a store (ADR-0012's
cache refusal). Lifecycle belongs to backend configuration or a
consumer's janitor, both of which compose over this seam.

## Drawbacks

- Small values pay the streaming shape: a config blob of 200 bytes
  still travels as an `io.Reader` and comes back as a
  `ReadCloser` to drain and close. This is the priced consequence
  of refusing `kv` — the ergonomic tax lands here, and a
  `bytes.NewReader`/`io.ReadAll` pair at every small-value call
  site is the visible form of it.
- Atomic visibility is an implementation burden on backends without
  native atomicity: filesystem adapters must stage-and-rename,
  chunked-upload backends must commit-on-complete. The law is why
  the seam is trustworthy; the work lands on adapter authors.
- Every `Put` must produce a `Version`, including on backends with
  no native token — and the synthesized token inherits `version`'s
  uniqueness-across-deletion requirement, which mtime-plus-size
  cannot satisfy. Filesystem-style adapters need a real counter or
  content hash, persisted with the store.
- No batch surface means N-object workflows issue N seam calls.
  Adapters may batch beneath; consumers cannot express batching
  above, and a consumer with a hot fan-out pays per-call overhead
  at the seam boundary.
- The reader-snapshot law can force copies: an adapter over a
  mutable medium that cannot pin a version must buffer or
  copy-on-write to honour an open reader across an overwrite.

## Open questions

None. The contested calls — streaming-only bodies, no digest, the
weakest-honest concurrent-listing clause, `IfNoneMatch` on Delete
as Invalid — were settled in the design spec this RFC compiles,
and their counter-arguments are recorded above at full strength.

## Unresolved / future work

- Multipart/resumable upload as an optional adapter capability
  with its own conformance assertion, if a consumer materialises
  transfers large enough to need it.
- An `fs.FS` adapter over `Store` for read-only consumers, outside
  the seam.
