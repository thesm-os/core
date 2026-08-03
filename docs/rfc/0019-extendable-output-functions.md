---
rfc: 0019
title: Extendable Output Functions
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-08-03
updated: 2026-08-03
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0019: Extendable Output Functions

## Summary

`crypto.XOF` — the seam for hash functions whose output length is
chosen by the caller rather than fixed by the algorithm. `Hasher` is
fixed-output by construction and cannot express one. The stream
interface is shaped so that the standard library's own SHAKE type
satisfies it without an adapter, and the seam adds only the identity
half that every other `core` crypto contract carries. Implementation in
`crypto/shake`.

## Motivation

`crypto.Hasher` returns a `Digest`, which is a fixed-width value. There
is no length parameter anywhere in the contract and adding one would
change every implementation, so the family currently cannot express a
SHAKE-style function at all. RFC-0003 identified this and deferred it.

Extendable output is a distinct primitive class rather than a variation
on hashing. It is what key derivation, post-quantum parameter
expansion, and deterministic sampling are built on, and in each case
the caller decides how many bytes it needs — a mask, a seed of a
particular width, a stream to sample from.

The standard library now provides the algorithm. What it does not
provide is the identity half: which extendable function produced a
given output, recorded durably enough that a build that does not yet
exist can reproduce it. That is the same gap the hashing, MAC and
signing seams fill, and the same reason.

## Detailed design

The interface lives in package `crypto`, beside `Hasher` and `MAC`, for
the reason given in RFC-0017: the seam's value types — `ID`,
`Algorithm`, and the `Digest` a caller builds from squeezed bytes — are
`crypto`'s own, and a subpackage would import them from its parent and
stutter on the way.

```go
// In package crypto.

// XOF is an extendable-output function: a hash whose output length is
// chosen by the caller rather than fixed by the algorithm.
//
// Unlike [Hasher], an XOF has no natural digest — the output is a
// stream. Callers needing a fixed-size commitment read exactly that
// many bytes and build a [Digest] with [DigestFromBytes].
type XOF interface {
    ID() ID
    Algorithm() Algorithm

    // NewXOFStream returns an absorbing/squeezing stream. Write
    // absorbs; Read squeezes. Reading continues one output stream;
    // writing after a read is an error.
    NewXOFStream() XOFStream
}

// XOFStream absorbs input and squeezes output.
//
// The method set is exactly that of the standard library's SHAKE type,
// so a stdlib value satisfies XOFStream directly and no adapter is
// needed. That is deliberate: core adds identity to a stdlib contract
// and does not restate the contract itself.
//
// The name distinguishes it from [Stream], which is the fixed-output
// hashing stream and has an incompatible method set — Sum and Close
// rather than Read.
type XOFStream interface {
    io.Writer
    io.Reader
    Reset()
}
```

### Deferring to the standard library's shape

The `XOFStream` method set is not designed here. It is copied from the
standard library's extendable-output type, so that type satisfies
`XOFStream` structurally and an implementation in this module is a
struct holding one, forwarding three methods.

This is the same discipline RFC-0003 §E applied to `hash.Hash` and
RFC-0017 applies to `cipher.AEAD`: where Go defines a contract, `core`
defers to it and adds only what the ecosystem needs on top, which is
almost always durable identity for persisted artefacts.

An interface is still required rather than the concrete type, because
algorithm agility is the entire point of the seam — a caller selects
the function at wiring time from a persisted `Algorithm`, and a
concrete type forecloses that.

### Absorb-then-squeeze is a state machine

Writing after reading is an error, not a second absorption phase. The
sponge construction has no meaning for it: once squeezing begins the
state is being consumed, and resuming absorption would silently produce
output unrelated to what a reader expects.

Implementations return an error from `Write` after the first `Read`
rather than panicking. This is a runtime condition a caller can reach
by threading a stream through code that does not know its state, which
puts it on the error side of the module's failure-semantics split
rather than the panic side.

`Reset` returns the stream to the absorbing phase with no absorbed
input, which is the only transition back.

### Implementations

`crypto/shake` ships SHAKE128 and SHAKE256 over the standard library.

```go
AlgSHAKE128 Algorithm = "shake128"
AlgSHAKE256 Algorithm = "shake256"
```

The names follow the NIST spelling, consistent with how the existing
hash and MAC constants are derived from their registry names rather
than invented.

### Building a `Digest` from a stream

An XOF has no natural digest, so the seam does not pretend otherwise.
A caller needing a fixed-width commitment reads exactly 32, 48 or 64
bytes and calls `DigestFromBytes` (RFC-0014), which is the same decode
path any other digest-shaped byte slice takes.

That is deliberately one step rather than a convenience method. A
`Digest` from an XOF is a caller's decision about output length, and
hiding it behind `XOF.Digest256()` would suggest the algorithm has an
opinion it does not have.

### No minimum-length signal

The seam does not warn a caller reading 8 bytes from SHAKE256 that it
has discarded most of the security margin. The correct output length
depends on what the bytes are for — a mask, a seed, a commitment — and
the seam does not know. A recommendation encoded as a constant would be
wrong for the cases it did not anticipate, and enforcing one would make
legitimate uses impossible.

### Allocation contract

`ID` and `Algorithm` allocate nothing. `NewXOFStream` allocates the
underlying state once; implementations may pool as the hash and MAC
streams do. `Write` and `Read` on a warm stream allocate nothing.

## Alternatives considered

### A. Extend `Hasher` with a length parameter

**Why not:** it changes every existing implementation and every call
site to accommodate a case none of them have, and it makes `Digest`'s
fixed-width contract conditional. Fixed-output and extendable-output
are different primitives; one interface for both serves neither.

### B. Put the seam in its own `crypto/xof` package

**Why not:** `xof.XOF` stutters, and the seam's value types belong to
`crypto`. The one advantage — `xof.Stream` rather than
`crypto.XOFStream` — does not pay for importing `ID`, `Algorithm` and
`Digest` from the parent package.

### C. Define a fresh stream contract rather than mirroring the stdlib

**Why not:** it forces an adapter around the standard library type for
no gain. Mirroring costs nothing and means the obvious implementation
is a forwarding struct.

### D. Return `[]byte` of a requested length instead of a stream

`Squeeze(n int) []byte`.

**Why not:** it forecloses incremental reading, which is the shape a
caller expanding parameters or sampling actually needs, and it
allocates per call. The stream supports both; a helper over it can be
added later if the one-shot form proves common.

### E. Wait until a caller needs it

**Why not:** RFC-0003 already identified the gap, and the cost of the
seam is one interface plus a forwarding implementation. The cost of its
absence is that anything needing extendable output reaches for the
concrete stdlib type and loses algorithm agility permanently, because
the choice is then baked into every artefact it produces.

## Drawbacks

- The seam is thin to the point that a reader may ask what it adds over
  using the standard library directly. The answer is only identity and
  agility, and for a caller who will never rotate algorithms that is
  nothing.
- `XOFStream` mirrors a standard-library method set, so if that type
  gains a method the mirror is incomplete and the structural
  satisfaction argument weakens.
- `NewXOFStream` and `XOFStream` are clumsier names than a subpackage
  would allow. That is the cost of keeping the seam in `crypto`, and it
  falls on the less common of the two stream types.
- Writing after reading is an error state the type system does not
  prevent, so it is a contract every implementation must enforce and
  every conformance suite must check.
- Two more `Algorithm` constants in a vocabulary now carrying hashes,
  MACs, signatures, AEADs and XOFs in one open string space.

## Open questions

None. Both questions this RFC previously carried are resolved above: no
minimum-length signal, for the reason given in that section; and cSHAKE
is deferred to future work rather than silently omitted.

## Unresolved / future work

- cSHAKE, whose customisation string is domain separation built into
  the primitive. It overlaps RFC-0016's framing and the two should be
  reconciled in one place rather than offering callers two answers.
- KMAC, the keyed extendable function, which is to `XOF` what `MAC` is
  to `Hasher`.
- Post-quantum uses, which are the strongest motivation for the seam
  and which cannot be built on it until the underlying algorithms are
  expressible.
