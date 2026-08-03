---
rfc: 0016
title: Framed Domain Separation
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-08-03
updated: 2026-08-03
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0016: Framed Domain Separation

## Summary

`preimage` — canonical, unambiguous framing for the byte sequences that
get hashed and signed, under a versioned domain tag. Variable-width
fields are length-prefixed; fixed-width fields are not, because their
width is a constant of the protocol, and when that width changes the
domain version changes with it. Also repairs `crypto.HashDomain`, which
today does not separate its own parts from one another.

## Motivation

`crypto.HashDomain` writes the domain and then each part, with no
length prefix between them. `HashDomain(h, d, "ab", "c")` and
`HashDomain(h, d, "a", "bc")` therefore produce identical digests.

A helper whose stated purpose is separating inputs does not separate
its own inputs from one another. Its doc promises non-collision across
domains and is silent on parts, so the hazard is invisible at the call
site — the function looks like it handles framing and it does not.

The general problem is larger than the bug. Any protocol that hashes or
signs a structured record has to decide how the fields become bytes,
and if it decides implicitly by concatenation, two distinct records
collide the first time a field boundary shifts. That collision is a
signature forgery: whoever can influence two adjacent variable-width
fields can move the boundary between them and produce a different
record carrying the same signature.

The version is the second load-bearing part. Without it, a field's
width is frozen at the moment the first artefact is signed, because
widening it silently changes what old signatures cover — old artefacts
verify against a layout the verifier no longer implements, and nothing
detects the mismatch.

## Detailed design

```go
// Package preimage builds canonically-framed, domain-separated byte
// sequences for hashing and signing.
//
// The contract is that no two distinct field sequences produce the
// same bytes. Variable-width fields are length-prefixed; fixed-width
// fields are not, because their width is a constant of the protocol —
// and when that width changes, the domain version changes with it.
package preimage

// Domain is the versioned domain-separation tag prefixing every
// preimage.
//
// Version makes layout evolution safe: a protocol that changes any
// field's width, order, or framing increments Version, and artefacts
// written under the old layout remain verifiable because the domain
// bytes differ.
type Domain struct {
    Name    string
    Version uint16
}

// Builder appends framed fields to a byte slice. It has no decode
// half: a preimage is built, hashed, and discarded; verifiers rebuild
// it from the artefact's own fields.
//
// No method can fail, so none returns an error. The zero Builder is
// not usable — construct one with New.
//
// # Allocation contract
//
// Zero alloc when dst has capacity for the finished preimage.
type Builder struct{ dst []byte }

// New begins a preimage in dst under d.
func New(dst []byte, d Domain) Builder

// Fixed appends p verbatim, with no length prefix. Use only for fields
// whose width is a constant of the protocol.
func (b *Builder) Fixed(p []byte)

// Bytes appends p with a big-endian uint32 length prefix.
func (b *Builder) Bytes(p []byte)

func (b *Builder) String(s string)
func (b *Builder) Uint64(v uint64)
func (b *Builder) Uint32(v uint32)
func (b *Builder) Preimage() []byte
```

### The domain tag is framed like everything else

The tag encodes as `uint32(len(Name)) || Name || uint16(Version)`.

The obvious alternative is a fixed-width name — pad or truncate to some
constant so the tag needs no framing of its own. That design has a
defect severe enough to rule it out: truncation maps every name sharing
a prefix onto the same tag, and a naming convention is precisely a
scheme for giving names a common prefix. Two protocols following the
convention would share a domain, and every artefact of one would verify
under the other. Total failure of the primitive, caused by a
convenience inside the primitive.

Rejecting over-long names instead of truncating fixes the collision but
keeps an arbitrary constant, an error return, and a limit callers will
hit. Length-prefixing the name removes all three: there is no limit, no
truncation, no error, and no constant to justify.

The consequence is that `New` cannot fail, no `Builder` method can
fail, and the package declares no errors at all. That is the shape a
primitive this small should have.

### `Fixed` is a sharp edge and is documented as one

`Fixed` writes no length prefix, so calling it on a variable-width
field reintroduces exactly the ambiguity this package removes.

It cannot be omitted: a preimage made entirely of length-prefixed
fields spends four bytes on every digest and every identifier, which
are the most common fields in any signed record and are all
fixed-width. The mitigation is naming and documentation — the method is
called `Fixed`, its doc says "only for fields whose width is a constant
of the protocol", and the conformance helper described under future
work exists to catch the misuse.

### Repairing `crypto.HashDomain`

`HashDomain` gains a big-endian `uint32` length prefix before each
part. The domain itself keeps its current treatment: it is written
first and its own length is not prefixed, because nothing precedes it
and nothing follows it that could be confused with it.

This changes the digests `HashDomain` produces. The change is breaking
for any artefact already written through it, and it is correct: the
digests it produced before were ambiguous, and preserving them would
mean preserving the collision.

The alternative — deprecating `HashDomain` and pointing callers at
`preimage` — was rejected because it leaves a working-looking function
with a silent collision in the module for the whole deprecation window.

### Relationship between the two

`HashDomain` is the one-shot helper for the common case: a domain and a
few parts, hashed immediately. `preimage.Builder` is for records with
mixed fixed and variable fields, an evolving layout, or a preimage that
is signed rather than hashed.

`core` ships the mechanism and no domain constants. A domain name is a
protocol's own vocabulary, and RFC-0003 §C forbids `core` from holding
one.

## Alternatives considered

### A. Length-prefix everything, drop `Fixed`

**Why not:** four bytes per field on records whose fields are mostly
32-byte digests is a large constant overhead on the most common shape,
and it is pure waste — a fixed-width field cannot be ambiguous. The
sharp edge is the price of not paying it.

### B. A fixed-width domain name

Pad or truncate the name to a constant so the tag is a fixed size.

**Why not:** truncation collides names sharing a prefix, which is what a
naming convention produces. Rejecting over-long names avoids the
collision but keeps an arbitrary constant, an error path, and a limit.
Length-prefixing costs four bytes once per preimage and removes the
entire question.

### C. A tag-length-value encoding

Give every field a type tag as well as a length.

**Why not:** that is a serialisation format, and it invites a decode
half, and then a schema. A preimage is built, hashed, and discarded;
the verifier rebuilds it from fields it already holds. Nothing needs to
parse one.

### D. Leave `HashDomain` as it is and document the hazard

**Why not:** a documented collision in a function whose name promises
separation will be hit by whoever does not read the doc, which is
whoever is in a hurry. The function is small and pre-1.0; fixing it
costs one line and one CHANGELOG entry.

### E. Variable-length prefix (varint) instead of `uint32`

**Why not:** a varint's encoded length depends on the value, which means
the framing of a field depends on the length of the field. That is a
second thing to get right for no benefit at these sizes.

## Drawbacks

- Fixing `HashDomain` breaks every digest it has produced. Pre-1.0 makes
  this permissible, not free: anything already persisted through it
  cannot be reverified.
- `Fixed` can be misused, and the misuse produces a silent collision
  rather than an error. It is the one unsafe operation in a package
  whose purpose is safety.
- A variable-width domain tag means two preimages under different
  domains differ in length as well as content, so a caller sizing a
  buffer from the domain alone cannot.
- Two overlapping mechanisms — `HashDomain` and `Builder` — mean a
  caller has to choose, and the wrong choice is not an error.
- The zero `Builder` is unusable but not detectably so; a caller who
  declares one instead of calling `New` gets an untagged preimage.

## Open questions

None. The three questions this RFC previously carried are resolved
above: the domain name is length-prefixed rather than bounded, so no
size constant is needed and `New` cannot fail; and a hashing
convenience over `Preimage()` is one line the caller writes, since
`h.Hash(b.Preimage())` already reads correctly.

## Unresolved / future work

- A conformance helper that asserts a caller's own preimage
  construction is boundary-shift-free, by generating adjacent
  variable-width field pairs and checking for collisions. This is the
  mitigation for `Fixed` and it belongs with the suites in `coretest`.
- Whether `Instant`, `Digest` and `ID` should have `Builder` methods of
  their own, once RFC-0014's encodings land, so a caller writes
  `b.Instant(t)` rather than remembering that an instant is a
  fixed-width 16-byte field.
