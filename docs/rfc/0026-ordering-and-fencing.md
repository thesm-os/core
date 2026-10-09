---
rfc: 0026
title: Ordering and Fencing
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-08-05
updated: 2026-08-05
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0026: Ordering and Fencing

## Summary

Fencing lands in `epoch` as a law over the existing `Epoch` type
rather than a new type: an admit-equal comparison (`Admissible`), a
sentinel for revoked authority (`ErrFenced`, classifying as Conflict),
an adapter kit (`Watermark`) whose constructor makes the seeding
obligation unforgettable, and the canonical binary encoding `Epoch`
needs the moment a watermark is persisted. Alongside it, one law on an
existing type: `version.Version` is equality-only — it proves
identity, never order. A conformance suite in `coretest/epochtest`
makes the fence laws checkable against any fenced writer, including
the law that fencing binds at handle construction, so an unfenced
write path is structurally absent rather than a thing review must
notice.

## Motivation

`core` ships the producer half of epoch-based coordination and not
the validator half. `epoch.Counter` mints strictly-monotonic values;
nothing in `core` says what a storage adapter must do when a write
arrives carrying one. Every consumer answers alone, and the two
observed answers are the argument for a law:

- The ledger threads a `Fence uint64` through nine request structs
  and validates in each adapter by hand. It forgot one —
  `PutStreamMeta` carries no fence field, so a deposed writer can
  mutate stream metadata after its lease is revoked. The hole
  survived review because per-call threading makes it a field
  omission, invisible unless you diff request structs.
- Foundation defines `FenceToken{Epoch, Node}` and repeats a
  MUST-validate clause on every backend's write methods. No validator
  exists; enforcement lives only in conformance tests, and the `Node`
  field participates in no comparison rule anywhere in the module —
  identity riding in a token, never consulted.
- The ledger's own contract doc requires the watermark to be durable;
  its reference adapter keeps it in a struct field, seeded to zero on
  restart. A restarted adapter admits every zombie that ever held the
  scope. Nothing caught this, because the seeding obligation lived in
  prose.

The same survey produced the `version` half of this RFC. Foundation's
`VersionedCache` requires "monotonic version ordering … comparison is
lexicographic (the Version type is an opaque string; implementations
define ordering)". A `Version` is computed by the backend from
whatever it has — a row counter, a content hash, an HLC — so two
backends sort the same tokens differently, and a cache that drops
writes by that sort keeps stale data on one backend and fresh on
another, silently. RFC-0007 made `Version` opaque; this RFC states
the consequence that was always implied: opaque means unordered.

Why here: the fence is a law over `epoch.Epoch`, its rejection is a
class in `errs`, and its interaction with conditional writes is
`version`'s vocabulary. The three packages are `core`'s; a law that
spans them can live nowhere lower.

## Detailed design

### The three fence laws

A write bearing fence epoch `e`, arriving at a scope whose watermark
is `w`:

1. **Admit-equal.** The write is admitted iff `e >= w`; the watermark
   advances iff `e > w`. One authority performs many writes per
   tenure — including multi-write operations under a single fence,
   such as an epoch rotation that commits two writes — so
   strictly-greater admission is wrong by construction, not by
   preference.
2. **Zero is unfenced.** `e == epoch.Zero` means the caller is not
   using fencing for this operation: admitted unconditionally, the
   watermark untouched. This is the existing [Zero] sentinel's
   documented meaning — "no epoch assigned" — applied to authority.
3. **Validation is atomic with the write, against durable state.**
   The watermark is authoritative only where it is advanced in the
   same atomic step as the write it admits, and only where it
   survives restart. An in-process watermark is an implementation
   aid; the authority is the durable one.

### API

```go
// In package epoch.

// ErrFenced reports that a write's fence epoch is behind the
// scope's watermark: another holder has been granted a later epoch
// and this caller's authority is revoked. Classifies as Conflict
// under [go.thesmos.sh/core/errs.Classify].
//
// The remedy is NOT to retry the write. It is to abdicate: stop
// writing, release state derived from the revoked tenure, and
// re-acquire authority through whatever election issued the epoch.
// Only then is retry sound. The sentinel is distinct from
// [go.thesmos.sh/core/version.ErrMismatch] — both classify as
// Conflict, but errors.Is must be able to separate "re-read and
// retry" from "abdicate and re-elect".
var ErrFenced = errors.New("epoch: fence epoch superseded")

// ErrSize is returned by [Epoch.UnmarshalBinary] when the input is
// not exactly [EpochSize] bytes. A truncated read is a decode
// error, never a panic and never a partial value.
var ErrSize = errors.New("epoch: encoded epoch must be 8 bytes")

// EpochSize is the width in bytes of the binary encoding.
const EpochSize = 8

// Admissible reports whether a write bearing fence e may proceed
// against watermark w, per the admit-equal law: true iff e is
// [Zero] (the caller is not fencing this write) or e >= w.
//
// Admissible is pure. The atomicity obligation — validate and
// advance in the same step as the write — belongs to the caller;
// a check-then-write gap reintroduces the race fencing exists to
// close.
func Admissible(w, e Epoch) bool

// Watermark tracks the highest fence epoch admitted, applying the
// three fence laws: admit-equal, advance-on-greater, zero-bypass.
//
// Watermark is the adapter kit, not the authority. An in-memory
// adapter may use it as its whole implementation; a durable
// adapter uses it as the in-process cache of a mark it persists
// atomically with each admitted write. NewWatermark takes the
// seed because the obligation is unforgettable only if it is a
// parameter: a zero-seeded Watermark after restart admits every
// zombie that ever held the scope. Load the seed from the same
// durable state the writes go to.
//
// # Concurrency
//
// Safe for concurrent use. Admit is lock-free.
//
// # Allocation contract
//
// Value semantics via pointer; Admit and Current are zero-alloc.
type Watermark struct{ current atomic.Uint64 }

// NewWatermark returns a Watermark seeded at seed.
func NewWatermark(seed Epoch) *Watermark

// Admit applies the fence laws to e: nil and no advance when e is
// [Zero]; nil, advancing the watermark, when e is at or above it;
// [ErrFenced] when e is behind it. The advance is a CAS loop, so
// concurrent admits converge on the highest epoch with no lost
// update.
func (w *Watermark) Admit(e Epoch) error

// Current returns the highest epoch admitted so far, or the seed.
func (w *Watermark) Current() Epoch

// AppendBinary appends the canonical 8-byte big-endian encoding
// of e to dst. The encoding is a stable wire contract (RFC-0014):
// a persisted watermark or a fence carried in a message must read
// back identically across builds and years. Implements
// [encoding.BinaryAppender]. The zero Epoch has a wire form — it
// is the number zero, and a freshly-created scope legitimately
// persists it.
func (e Epoch) AppendBinary(dst []byte) ([]byte, error)

func (e Epoch) MarshalBinary() ([]byte, error)
func (e *Epoch) UnmarshalBinary(data []byte) error
```

`errs.Classify` learns `ErrFenced` exactly as it learned
`version.ErrMismatch` and `version.ErrExists` (RFC-0015): the
sentinel joins the recognised set, so producers that return it
unwrapped still classify correctly and generic middleware needs no
knowledge of this package.

### The equality-only law on `version.Version`

Doc contract added to `version.Version`, no signature change:

> A Version proves identity, never order. Two Versions are the same
> observation or different observations; neither is "newer". Any
> component that sorts, compares, or sheds writes by an ordering
> over Versions is out of contract — ordering across time is
> [epoch.Epoch]'s axis, and a backend that has a meaningful order
> exposes it as one.

The falsifier ships as fixture data rather than a universal
assertion, because the law constrains consumers of `Version`, not
implementations of anything:

```go
// In package coretest/versiontest.

// OrderingTrap is a pair of Versions whose bytewise order
// contradicts their production order: Older was produced first,
// yet sorts after Newer lexicographically.
type OrderingTrap struct {
    Older, Newer version.Version
}

// OrderingTraps returns pairs that make any ordering assumption
// observable: numeric-style tokens ("9" vs "10"), length-varying
// tokens, and high-byte prefixes. A suite drives its subject with
// each pair in both arrival orders and asserts behaviour is
// identical — a subject that branches on the bytewise order of a
// trap has ordered an opaque token.
func OrderingTraps() []OrderingTrap
```

### Fencing binds at construction

A fenced adapter takes its epoch when the handle is opened — one
holder, one epoch, one handle — and every write the handle performs
validates that epoch atomically. Per-call fence parameters are out
of contract. The ledger's missing-field hole is the argument: with
per-call threading, forgetting a fence is omitting a struct field,
and review must notice an absence. With construction binding there
is no unfenced write path to forget; a new write method is fenced
because the handle is.

The consequence is deliberate: a handle is tenure-scoped. When
authority is lost (`ErrFenced`), the holder abdicates and a new
handle is opened under the new epoch — which is the lifecycle both
surveyed consumers already implement, one of them as an explicit
poison-and-reopen discipline. That lifecycle stays consumer-side;
`core` supplies the law it enforces.

### Conformance: `coretest/epochtest`

The suite is parameterised over the consumer's handle, because the
law it checks — no write escapes validation — is about the
handle's whole surface:

```go
// In package coretest/epochtest.

// FencedScope is one isolated scope of a consumer's fenced
// writer. Open constructs a handle bound to fence epoch e;
// Supersede advances the scope's authority out-of-band, as an
// election would; Snapshot fingerprints the scope's observable
// state so rejection-without-mutation is assertable.
type FencedScope[H any] struct {
    Open      func(e epoch.Epoch) (H, error)
    Supersede func(e epoch.Epoch)
    Snapshot  func() any
}

// FencedSystem describes the writer under test. NewScope returns
// a fresh, independent scope per write case, so cases cannot
// observe each other's watermarks. Writes names every write the
// handle exposes — an omitted write is an untested hole.
type FencedSystem[H any] struct {
    NewScope func() FencedScope[H]
    Writes   map[string]func(H) error
}

// AssertFencedWriter drives every named write through a fenced
// handle's lifecycle on its own scope. Asserted, per write: (a) a
// write through a superseded handle returns an error matching
// ErrFenced, classifying as errs.Conflict, AND leaves the scope's
// snapshot unchanged — rejection without mutation; (b) a write at
// an epoch equal to the current authority is admitted; (c) a
// handle opened at Zero neither validates nor advances; (d) after
// supersession to epoch n, a fresh handle at n succeeds where the
// superseded handle fails — the reseed law.
func AssertFencedWriter[H any](t *testing.T, sys FencedSystem[H])
```

Rejection-without-mutation is the assertion that earns the suite:
one surveyed implementation holds it only by accident of lock
scope, and it is the difference between a fence and a suggestion —
a rejected write that half-applied has ignored the fence where it
mattered.

### Worked example

Node A holds epoch 7 and stalls mid-batch. Election grants B epoch
8; B's first write is admitted (`Admissible(7, 8)`), advancing the
durable watermark to 8. A wakes and retries with fence 7:
`Admissible(8, 7)` is false, the adapter returns `ErrFenced`, and
nothing is mutated. A's generic retry loop asks
`errs.Classify(err)` — `Conflict` — and `errors.Is(err, ErrFenced)`
tells it this conflict's remedy is abdication, not re-read.
Meanwhile A also holds a stale read of key K at `Version v3`; its
conditional write `IfMatch: v3` fails with `version.ErrMismatch`
independently of fencing. Authority and proof fail on separate
axes, and both outcomes are recomputable from the laws alone.

### Edge cases

- **`Admit(Zero)`** returns nil and never advances — an unfenced
  write cannot move authority.
- **Concurrent admits** at epochs 5 and 7 may interleave arbitrarily;
  the CAS loop guarantees the watermark converges to 7 and no update
  is lost. The admit at 5 may succeed or return [ErrFenced] depending
  on interleaving — racing authorities are exactly what the fence
  arbitrates — and an admit at 4 fails regardless.
- **Epoch exhaustion** inherits [Epoch.Successor]'s position: a
  producer advancing once per nanosecond exhausts `uint64` in ~584
  years; the wrap is not guarded, and consumers needing
  bounds-checked monotonicity enforce it above.
- **Crash between write and advance** cannot occur in a conformant
  durable adapter — law 3 requires one atomic step — and the
  `Watermark` doc says exactly that, because the kit itself cannot
  provide the atomicity.

There is no migration: every addition is new surface, and the
`version` law changes no behaviour in `core`, which contains no
component that orders Versions.

## Alternatives considered

### A. A fence token struct carrying node identity

Foundation's shape: `FenceToken{Epoch, Node}`.

**Why not:** its own codebase defines no comparison rule for `Node`
anywhere — the field is diagnostics riding in a token. The
election layer already guarantees one holder per epoch, which is
why the ledger's bare integer works. Identity belongs in telemetry
attributes, where it is actually read.

### B. Strictly-greater admission

Reject `e == w`: each write consumes the fence.

**Why not:** one tenure performs many writes, and the surveyed
multi-write operation (epoch rotation: two writes, one fence)
breaks on its second write. Per-write fences are a different
primitive — one-shot tokens — with no observed consumer and a
heavy issuance cost.

### C. A validator interface

`type Validator interface { Validate(e Epoch) error }` as a seam
adapters implement.

**Why not:** validation is inseparable from the write's atomicity.
A seam invites calling `Validate` and then writing — the
check-then-act gap the fence exists to close. The law binds
*writes*, so the conformance suite targets writers, and the only
reusable code — the comparison and the CAS — ships as `Admissible`
and `Watermark` instead.

### D. Per-call fence parameters

The ledger's shape: a fence field on every write request.

**Why not:** nine request structs, one forgotten, one silently
unfenced write path in a compliance-grade system. The failure mode
of construction binding (a consumer needing two concurrent epochs
through one component) has no observed instance; the failure mode
of per-call threading is on disk.

### E. A lease lifecycle in `core`

Acquisition, renewal, revocation callbacks, poison-on-revoke.

**Why not:** issuance is leader election, which `core` does not
ship; the holder discipline is a consumer pattern with one good
implementation already. `core` supplies the law leases enforce,
not the leases.

### F. Ordered Versions instead of the equality-only law

Foundation's `VersionedCache`: opaque tokens, lexicographic order.

**Why not:** the order is fiction. Backends compute Versions from
unrelated native representations; sorting them compares row
counters to content hashes. The observed contract corrupts
silently and differently per backend — the worst failure class
this module recognises.

## Drawbacks

- A fenced handle is tenure-scoped, so consumers open a new handle
  per election. For a connection-pooled adapter that means handle
  construction must be cheap and the pool lives below the handle —
  a real constraint on adapter architecture, stated here rather
  than discovered in one.
- `Watermark` is an attractive nuisance: it makes an in-process
  fence easy, and an in-process fence that is not backed by
  durable state is wrong in exactly the way law 3 forbids. The
  seed parameter and the doc push against it; they cannot prevent
  it.
- `errs.Classify`'s recognised set grows by one, and each addition
  is a coupling: `errs` now knows two packages' sentinels. The
  alternative — every producer wrapping with `WithClass` — was
  rejected in RFC-0015 for the same reason it fails here: the
  sentinel must classify even when returned by code that has never
  heard of classification.
- The equality-only law is not mechanically enforceable in
  general. `OrderingTraps` catches subjects whose suites use it;
  a consumer that never runs the traps can still sort Versions.
  The law's teeth are review plus fixtures, and that is weaker
  than a type-system guarantee.
- Zero-bypass means a forgotten fence — a zero value where an
  epoch was meant — admits silently. Construction binding narrows
  this to one site per consumer (the handle constructor), which is
  where review looks; it does not eliminate it.

## Open questions

None. Two were settled in the design spec this RFC compiles:

**Should the token carry node identity?** No — Alternative A; no
comparison rule exists to give it meaning.

**Is a revoked fence `Conflict` or `Denied`?** `Conflict` — it is
premise-invalidation, distinguished from `ErrMismatch` by sentinel,
not by class; the remedy difference (re-elect versus re-read) is
carried by `errors.Is`.

## Unresolved / future work

- A lease seam, if a second consumer materialises a holder
  lifecycle that the poison-handle pattern does not cover.
- One-shot fence tokens (strictly-greater admission) if a consumer
  arrives with per-write issuance; Alternative B records the
  distinction so it lands as a new primitive, not a flag.
