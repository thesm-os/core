---
rfc: 0029
title: Domain-Separated Tree Hashing
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-08-14
updated: 2026-08-15
discussion: go.thesmos.sh/ledger docs/rfc/0001-ledger-architecture-on-core.review.md
produces-adr: ADR-0013, ADR-0014
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0029: Domain-Separated Tree Hashing

## Summary

`crypto.Hasher` replaces `Combine` with `HashTagged` and
`CombineTagged`: a one-byte role prefixing every leaf and every
interior hash, so a consumer building hash chains and Merkle trees
cannot be made to mistake a leaf for an interior node. The role
byte's high bit is reserved for arity — clear for `HashTagged`, set
for `CombineTagged` — so the two operations can never hash the same
first byte and cross-arity confusion is unrepresentable rather than
forbidden. Core ships the mechanism and no roles, as it ships
`HashDomain` and no domains.

The role also retires the zero-`Digest` genesis sentinel outright.
A chain's first link becomes a distinct tagged operation rather
than a combine with an empty operand, so there is no padding rule
to state and none to get wrong — and with untagged `Combine`
deleted, ADR-0007's carve-out retires with it rather than
persisting for a caller who does not exist.

## Motivation

### The defect

`Combine(l, r)` is `H(l ‖ r)` and `Hash(data)` is `H(data)`, over
the same function with nothing distinguishing them. A payload of
exactly two digest widths therefore hashes to an interior node.
Every value below is recomputable from these two inputs alone:

```text
payload0 = {"act":"infer","id":1}
payload1 = {"act":"infer","id":2}

l0 = Hash(payload0)
   = f93e980a49843659410a55ffffd2a28cb04cbe88739798832cfa79a31a04ac3a
l1 = Hash(payload1)
   = 5db4830ba6fc457c9b2194f6c15420f36ddf71eec962e0d6a6405fa59158f454

real interior node   Combine(l0, l1)
leaf of 64-B payload Hash(l0 ‖ l1)
both = b670d81e0904971c5d81caab192ab704681b2fcb2474c5ce16d442d8a6cf4bdb
                                   collide: true
```

Anyone holding two sibling digests from a legitimate proof can
present their concatenation as a payload whose leaf hash equals a
real interior node, then serve a shorter authentication path for an
entry that was never written. The fabricated entry verifies.

This is the second-preimage class RFC 6962 §2.1 closed for
certificate transparency in 2013 with the same fix: a distinct byte
before leaves and before nodes.

A naive tag does not close it by itself. `HashTagged(t, data)` is
`H(t ‖ data)` and `CombineTagged(t, l, r)` is `H(t ‖ l ‖ r)`, so
when `data` is exactly two digest widths under the same `t` they
are the same bytes:

```text
HashTagged(0x01, l0 ‖ l1)      — a 64-byte payload under role 0x01
CombineTagged(0x01, l0, l1)    — an interior node under role 0x01
both = 97055cac3af04d642568f4619517572949240caec5256838a62cf9c166b19c8e
```

A protocol assigning one role byte to both a leaf and a node role
has the original defect back, unchanged — and a rule saying "keep
them disjoint" would be exactly the documentation-only defence
Alternative E rejects. The arity bit makes the disjointness
structural: a leaf role and a node role cannot share a first byte
because the methods refuse each other's half of the space.

The bit closes exactly that and no more. Within the unary half,
applying one role to more than one kind of input remains a
registry discipline the mechanism cannot check — named in
Drawbacks as the residue it is.

The consumers exposed are the ones core exists to serve. Any
audit log, any content-addressed store, any accumulator whose
leaves are caller-supplied bytes — the attack needs no collision
search, only a caller who can choose a payload.

### The genesis sentinel is undocumentable

ADR-0007 admits the zero `Digest` into `Combine`, zero-padded to
the hasher's width. The padding lives in an implementation comment
and is not recoverable from the type's own documentation: a reader
meeting `IsZero` and `Size() == 0` implements `H(leaf)`.

The two readings diverge at a chain's first entry — here over `l0`
from the block above, so both values are recomputable:

```text
zero-padded (implemented)   Combine(zero, l0)
   = 8005ae21d8790c4d737eafcea403531938c72f2a37d2fbf3677aa8faf13b00dc
zero-length (as documented) H(l0)
   = d753451184425020185787db27689b0a40f5319d25d8b9991e69ab454ef13abe
```

An independent verifier built from the documentation rejects
honest history. The sentinel is not a documentation failure that
better prose repairs; it is a value whose meaning cannot be read
off its type.

### The prefix ADR-0007 rejected, and the arithmetic it rested on

ADR-0007 considered and rejected framing `Combine`'s operands with
a leading byte, because it "pushes the input past 64 bytes and
destroys the single-block property `Combine` is built around" and
"doubles the invocations on a hot path". That rejected byte is this
RFC's byte, on the same hot path, and the contradiction has to be
faced rather than re-litigated by the first person who benchmarks
a batch.

The premise is arithmetically false. SHA-256 padding (FIPS 180-4
§5.1.1) appends `0x80` and an eight-byte length, so a 64-byte
input needs 73 bytes of message plus mandatory padding — past one
block, forcing the padded length to 128 and the compression count
to **two**; a 65-byte input pads to the same 128. There was never
a single-block property to destroy. The same holds across every
hasher this module ships: SHA-384 and SHA-512/384 on the SHA-512
core (96 → 1 block, 97 → 1), SHA-512 (128 → 2, 129 → 2), SHA3-256
at rate 136 (64 → 1, 65 → 1), SHA3-384 at rate 104 (96 → 1,
97 → 1) and SHA3-512 at rate 72 (128 → 2, 129 → 2). **The role
byte costs zero additional compression invocations on the combine
path of every shipped hasher.** For `HashTagged` over arbitrary
data the added byte can cross a block boundary at specific
lengths, costing at most one compression, off the hot path.

Two corrections follow and are in this RFC's scope: the
`sha256.go` comment claiming "a single compress invocation with no
padding" is wrong today and is fixed with the implementation, and
ADR-0007 is superseded. By this repository's convention an ADR is
superseded by an ADR, so acceptance produces two: **ADR-0013**,
tagged tree hashing on the interface with the high bit reserved
for arity, and **ADR-0014**, the genesis sentinel deleted rather
than documented. ADR-0007 gains `superseded-by` naming both — it
dies on two independent grounds, its premise and its subject.

### Why now, and why on the interface

Every artefact hashed under the current scheme must be regenerated
when this lands. That cost is paid once and grows with every byte
persisted before it.

The methods belong on `Hasher` rather than beside it because of
escape analysis, which this module has already reasoned about once:
`HashDomain` draws its length buffer from a pool because "it is
passed to `Stream.Write`, an interface method the compiler cannot
see through, so a local array would escape." A free function taking
`Digest` values and writing `left.Bytes()` through the same
boundary escapes both operands. Implemented as a method, the
concrete hasher builds a stack buffer and calls its own primitive,
and nothing escapes — which is why `Combine` is zero-allocation
as a method, and a helper wrapping it would not be.

## Detailed design

### The role

Named `Role` rather than `Tag`: this package already uses "tag" for
the AEAD authentication tag and for `ID`'s printable ASCII form,
and a third meaning — one of them a term of art for the thing that
authenticates — is two too many. `framer.go` already uses "role"
in exactly this sense, so the name lands in existing vocabulary.
The methods keep `Tagged` deliberately: the byte is a tag by
mechanism — something prepended — and a role by meaning — which
one. The type names what a protocol assigns; the methods name what
they do, and "tagged" in this document's prose is that mechanism,
never a fourth meaning.

```go
// Role is a one-byte domain separator distinguishing the roles a
// digest plays inside one protocol's commitment structures — a
// leaf from an interior node, a chain link from a tree node.
//
// The high bit encodes arity and belongs to the mechanism: roles
// with the bit clear (0x00–0x7F) are unary and accepted only by
// [Hasher.HashTagged]; roles with the bit set (0x80–0xFF) are
// binary and accepted only by [Hasher.CombineTagged]. The methods
// refuse each other's half of the space, so a 2w-byte payload
// under a unary role can never share bytes with an interior node
// under a binary one — the cross-arity second preimage is
// unrepresentable, not merely forbidden. 128 roles per arity.
//
// This package ships no Role constants, as it ships no domain
// constants for [HashDomain]. The vocabulary belongs to the
// protocol: only its designer knows how many roles exist and
// which values are already written down.
//
// A protocol's role count is usually larger than it first looks.
// A hash-chained log with batching needs distinct roles for the
// payload commitment, the record commitment over it, the chain's
// genesis and link forms, a fork into another chain, and each
// tree it feeds — which is why a byte is the width rather than a
// bit.
//
// # Scope
//
// Role separates roles WITHIN a protocol. It does not separate
// protocols from each other — two protocols independently choosing
// 0x01 are not distinguished by it. Cross-protocol separation is
// [Domain] and [Framer], which carry a name and a version for
// exactly that reason. A protocol whose digests may be presented
// to another protocol's verifier needs both.
//
// # Allocation contract
//
// Value type; pass by value.
type Role uint8
```

### The operations

```go
// HashTagged returns the digest of data under r, a unary role.
//
// Use it for the leaves of a tree or chain, where data is
// caller-supplied and could otherwise be chosen to collide with an
// interior node. Content addressing uses [Hasher.Hash] instead: a
// content address is the digest OF the bytes, so prefixing would
// make the address name something the bytes are not.
//
// r with the high bit set panics: 0x80–0xFF are binary roles and
// accepting one here is what would let a crafted payload share
// bytes with an interior node. Empty data is legal — the digest of
// the role byte alone — and collides with nothing shorter.
//
// # Allocation contract
//
// Zero-allocation on every implementation in this module.
HashTagged(r Role, data []byte) Digest

// CombineTagged returns the digest of left ‖ right under r, a
// binary role.
//
// r with the high bit clear panics. Both operands must have
// [Digest.Size] equal to this hasher's output size; a mismatch
// panics, per this package's precondition-violation discipline —
// and the zero Digest is a mismatch here, with its own
// diagnostic. There is no sentinel to admit: tagged protocols
// have no genesis case, because a chain's first link is its own
// unary role over one operand, not a combine with an absent one.
//
// # Allocation contract
//
// Zero-allocation on every implementation in this module.
CombineTagged(r Role, left, right Digest) Digest
```

### Byte layout

Normative, and the whole content of the contract:

```text
HashTagged(r, data)           H( r ‖ data )      r ∈ 0x00–0x7F
CombineTagged(r, left, right) H( r ‖ left.Bytes() ‖ right.Bytes() )
                                                 r ∈ 0x80–0xFF
```

One role byte, then the operands, with no length prefixes. Lengths
are constants of the construction — the role is one byte and a
hasher's digest width is fixed — so framing them would add sixteen
bytes per interior node to distinguish inputs that cannot be
confused. `HashDomain` frames because its parts are variable-width
and arbitrary in number; these are neither.

The arity ranges are part of the layout, not advice: a verifier
that meets a hash whose first byte is in the wrong half for its
claimed construction rejects it. `HashTagged(r, nil)` is legal and
is `H(r)` — one byte, colliding with nothing shorter.

`HashTagged` needs no framing for the same reason in reverse: the
role is a fixed prefix, so a one-byte shift cannot reinterpret the
remainder as anything but data.

### Genesis without a sentinel

A tagged protocol never combines with an empty operand. The first
link of a chain is one tagged hash of one operand:

```text
E₁ = HashTagged(genesis, leaf₀)
E₂ = CombineTagged(link, E₁, leaf₁)
```

`E₁` and `E₂` cannot be confused: different roles in different
arity halves, and different input lengths under any hasher.

Untagged `Combine` is deleted with this RFC, and ADR-0007's
carve-out retires with it. It has no call site in this repository
outside its own implementations and their conformance and bench
suites, and no consumer anyone can name; keeping it would preserve
a zero-sentinel for nobody while its interface documentation
instructs the exact construction this RFC exists to close. An
out-of-module holder breaks at compile time — the cheap, visible
failure — and is rewriting its artefacts under this RFC regardless.

### Test vectors

SHA-256. Untruncated, because a verifier checks the whole digest.

Two things these vectors are not. The role values are example
bytes: core ships no roles, and although the seven below are the
ledger's declared permanent registry (RFC-0001 §4, renumbered
under the arity rule), their appearance here binds
implementations to the byte
*layout*, never to any consumer's registry. And the composition is
a mechanism demonstration, not a construction: it chains raw
leaves, which no consumer does — the ledger salts its leaves and
chains entry commitments, and its real composition with golden
vectors lives in its `spec` package, the interim-normative home
its RFC-0001 §18 designates. Build from a consumer's spec, not
from this table.

```text
roles: unary (bit clear)   entry-leaf 0x01   chain-genesis 0x02
                           entry-header 0x07
       binary (bit set)    chain-link 0x83   batch-node 0x84
                           mmr-node   0x85   chain-fork 0x86

payload0 = {"act":"infer","id":1}
payload1 = {"act":"infer","id":2}
payload2 = {"act":"score","id":3}

leaf0 = HashTagged(0x01, payload0)
      = 9978971ab897cd3656fdde798a8984a606b37e51f2153f5ebeae2b6a06550cfd
leaf1 = HashTagged(0x01, payload1)
      = 67a0f291cd3e01dbddfaacf8ba3e0af33627dd94cc4d7ad3d9deefe302faafcd
leaf2 = HashTagged(0x01, payload2)
      = 921fd00458c68f7c46437576db9f96878f388c9eea233858cafa0a18733c77f8

E1 = HashTagged(0x02, leaf0)
   = 34b6c48bb25089e98c8ac2b7a67ea4c35dbf1cf2dff29526af55c46cd3d7dfb5
E2 = CombineTagged(0x83, E1, leaf1)
   = ba248ec5db725a8eff0a0068c4225132425bd983e154b158f7ce5bcb0bc60df6
E3 = CombineTagged(0x83, E2, leaf2)
   = cde1a7dcd5630788b710cd741398fa14bff17083a79ed9e895880b949d2ce296

n01       = CombineTagged(0x84, leaf0, leaf1)
          = cf9b6328ccb2bd4156e7f22b4417d426fa7a0bce96e74690ecbb9ff95b723715
BatchRoot = CombineTagged(0x84, n01, leaf2)
          = fc2f792f6acc7aa9f5ebbc7de9ffa490ff08ef33cbffefb108da46b501d71fba

HashTagged(0x01, nil) — legal, the role byte alone:
          = 4bf5122f344554c53bde2ebb8cd2b7e3d1600ad631c385a5d7cce23c7785459a
```

Three negative vectors, which are the ones that fail if an
implementation drops the role or the arity guard:

```text
the second-preimage attempt — a payload equal to leaf0 ‖ leaf1:
  HashTagged(0x01, leaf0 ‖ leaf1)
    = 97055cac3af04d642568f4619517572949240caec5256838a62cf9c166b19c8e
  CombineTagged(0x84, leaf0, leaf1)
    = cf9b6328ccb2bd4156e7f22b4417d426fa7a0bce96e74690ecbb9ff95b723715
  these must differ

the cross-arity attempt — one role for both a leaf and a node:
  HashTagged(0x01, leaf0 ‖ leaf1) is the 97055cac… value above;
  CombineTagged(0x01, leaf0, leaf1) would be the same bytes and
  must panic — the arity guard is what makes this unconstructible

the same operands under three binary roles must give three digests:
  0x83 chain-link = 2127809bd7be825e…
  0x84 batch-node = cf9b6328ccb2bd41…
  0x85 mmr-node   = 6eebccae2a270500…
```

`coretest/cryptotest` gains the negative vectors, so every
implementation is held to all three rather than only the
reference one — the first two as
digest comparisons, the third as a panic assertion, since the
arity guard is a refusal and only a refusal test proves it exists.
The suite asserts the layout — one role byte in the correct arity
half, then operands, no framing — through these example roles; a
hasher passing it makes no claim about which roles any protocol
assigns.

### What implementations do

Two methods per `Hasher`, each a stack buffer and one call to the
concrete primitive. For SHA-256:

```go
func (Hasher) CombineTagged(r crypto.Role, left, right crypto.Digest) crypto.Digest {
    if r&0x80 == 0 {
        panic("crypto: CombineTagged requires a binary role (high bit set)")
    }
    if left.IsZero() || right.IsZero() {
        panic("crypto: CombineTagged refuses the zero Digest; the genesis sentinel is retired — a chain's first link is a unary role over one operand")
    }
    if !sized(left) || !sized(right) {
        panic("crypto: CombineTagged operands must be full-width digests")
    }
    var buf [1 + 2*crypto.DigestSize256]byte
    buf[0] = byte(r)
    copy(buf[1:], left.Bytes())
    copy(buf[1+crypto.DigestSize256:], right.Bytes())
    return crypto.NewDigest256(sha256.Sum256(buf[:]))
}
```

`sized` is a new predicate — exact output width — and the zero
Digest is refused first, with its own diagnostic: the consumer who
trips it has reached for the retired sentinel, and being told
"operands must be full-width" would send them to the wrong
conclusion about a value whose meaning was never readable from its
type. The old `combinable` admitted the zero Digest by design, and
its diagnostic pointed a debugging consumer at ADR-0007's
carve-out; both retire with `Combine`.

`HashTagged` needs no pool. The prefix is one of 256 constant
bytes: a package-level `var roleTable [256]byte`, sliced as
`roleTable[r : r+1]`, is a non-escaping slice of static memory —
zero allocation with no Get, no Put and no atomics. `HashDomain`
pools because its length buffer's content varies per part; a role
byte's does not.

`Hash` survives and remains correct for content addressing, where
the address must be the digest of the bytes and nothing else. Two
documentation sites are corrected in the same change, and both are
load-bearing given this RFC's own thesis that interface
documentation caused the defect. `Hash`'s own docblock
(`hasher.go`) advertises it as the "hot path for leaf commitments"
— which, post-RFC, instructs the one residual mistake that remains
expressible; it is rewritten to content addressing only, with
leaves directed to `HashTagged`. And `Digest.IsZero`'s docblock
(`digest.go`) is the genesis sentinel's primary prose home — "the
predecessor anchor of the genesis entry", the zero-padding rule,
the ADR-0007 citation — all of which dies here and is rewritten to
the one meaning that survives: the uninitialised value, valid
nowhere.

`Combine` is deleted. The blast radius lands where the work
actually is: the six implementations, `coretest/cryptotest`'s
stub and generated surfaces (`hasher_stub.go`,
`hasher_stub.gen.go`, `hasher_bench.gen.go`), the
`//testkit:sample` directives the new methods need for
regeneration, and the two docblocks above.

## Alternatives considered

### A. Free functions over `Hasher`

`func HashTagged(h Hasher, r Role, data []byte) Digest` and its
combine counterpart, additive and implementable once.

**Why not:** it allocates, and the reason is written in this
module already. Writing `left.Bytes()` to `Stream.Write` — an
interface method — escapes the `Digest` the slice points into, so
a combine costs two heap allocations where the method form costs
zero. `Combine` is zero-allocation precisely because it is a
method on the concrete hasher, and a helper cannot inherit that.
The interface grows by two methods across six implementations;
the alternative taxes every call forever.

### B. A full `Domain` per hash instead of a byte

Prefix each hash with `Domain{Name, Version}` — the vocabulary
RFC-0016 already defines — so tree hashing inherits cross-protocol
separation and versioning.

**Why not:** it prices an interior node at a name plus a version
plus their framing, on the operation a Merkle tree performs N−1
times per batch, to separate protocols that a tree's own operands
cannot reach. Leaves enter a tree from one protocol; the confusion
being closed is between roles inside it. A protocol that also
needs cross-protocol separation composes `Framer` at the boundary
where its data enters, once, rather than at every interior node.

### C. Reuse `HashDomain`

`HashDomain(h, domainBytes, left.Bytes(), right.Bytes())` needs
no interface change at all.

**Why not:** it length-prefixes every part, adding sixteen bytes
of framing per interior node to disambiguate two operands of
fixed, equal, hasher-determined width. It also escapes, per A.
Framing is right for variable parts and wrong for a pair of
digests.

### D. Leave domain separation to consumers

Document the hazard on `Combine` and let each protocol prefix its
own inputs before calling.

**Why not:** it is the status quo, and this RFC exists because a
consumer built a chain, a batch tree and an accumulator on
`Combine` without prefixing, and the omission survived a design
review by six reviewers before three of them found it
independently. A hazard that requires every consumer to
re-derive a 2013 result is a hazard core should close once.

### E. Keep the genesis sentinel and document it better

Amend `Digest.IsZero` and ADR-0007 to state the zero-padding rule
explicitly, leaving the sentinel in place.

**Why not:** the reading that fails is the one the type invites —
`Size() == 0` means zero bytes, and every reader who has tried has
implemented `H(leaf)`. A rule that must be read somewhere other
than the type it governs will be missed again. Tagged genesis
deletes the case rather than annotating it.

### F. RFC 6962's own shape — fixed 0x00 and 0x01

Bake the two prefixes into the mechanism, as certificate
transparency does: leaves are always `H(0x00 ‖ data)`, nodes
always `H(0x01 ‖ l ‖ r)`, nothing for a caller to assign.

**Why not:** two values cannot distinguish the roles one protocol
holds — a chain link, a batch node and an accumulator node are all
"interior" and must not collide with each other, which is the
three-roles negative vector. What 6962's shape gets right is that
its separation is structural rather than disciplinary, and this
RFC keeps exactly that property where it matters: the arity bit is
the mechanism's 0x00/0x01, fixed and refused when wrong, while the
remaining seven bits carry the role multiplicity 6962 never
needed. This is 6962's design extended, not declined.

### G. Keep untagged `Combine` beside the tagged forms

The additive shape: add the two methods, leave `Combine` and its
ADR-0007 carve-out in place for consumers that may hold them.

**Why not:** no such consumer can be named. `Combine` has no call
site in this repository outside its own implementations and their
conformance and bench surfaces, its interface documentation
actively instructs the construction this RFC closes, and keeping
it preserves the zero-sentinel — the value whose meaning cannot be
read off its type — for nobody. An out-of-module holder breaks at
compile time, which is the visible failure, pre-1.0, while
rewriting its artefacts under this RFC anyway. Additive change is
a reflex, not an argument; the honest version of this RFC replaces
rather than grows.

## Drawbacks

**This is a breaking interface change, not an addition.** Every
implementation of `Hasher` — six in this module, the cryptotest
stub and generated surfaces, and any outside — loses `Combine` and
gains two methods; any out-of-module caller of `Combine` stops
compiling. Pre-1.0 makes this cheap; it will not be cheap later,
which is an argument for now rather than a mitigation.

**Two ways to hash remain, and a consumer can still pick the wrong
one — but the forgery-enabling direction is gone.** `Hash` for
content addresses, `HashTagged` for leaves; picking `Hash` for a
tree leaf is still expressible and still wrong. What is no longer
expressible is the interior-node half of the confusion, because
untagged `Combine` no longer exists and `CombineTagged` refuses
unary roles. The residue is a naming decision per call site; the
second preimage needed both halves.

**A one-byte role is intra-protocol only.** A protocol needing
cross-protocol separation must compose `Framer` as well, and
nothing in the type says so except its documentation.

**The arity bit halves the space, and the guard costs a branch.**
128 roles per arity rather than 256 in one pool, and one byte
comparison per call. The role-count argument in the type's own
documentation says protocols need more roles than they first
think; it does not say they need more than 128 per arity, and no
plausible protocol does.

**One disciplinary rule survives, and it is named rather than
hidden: a unary role must be applied to inputs of one kind.** The
composition above uses 0x01 over caller bytes and 0x02 over a
digest — distinct roles, so safe; a protocol assigning one unary
role to both hands a caller who controls a digest-width payload a
preimage of the other construction. The mechanism cannot reach
this: arity is a property of the call site, checkable at the
boundary, while what a role is applied to is a property of the
registry, visible only in the protocol's design review. Splitting
the unary half again — digest-input and bytes-input ranges — would
make it structural at the price of a rule harder to explain than
the confusion it prevents, and input kinds are not two: framed
bytes, raw bytes and digests already differ. This is the residue
the arity bit does not reach, it is documentary, and an RFC that
has just argued documentation-only defences fail owes its reader
this sentence.

**Every artefact hashed under the current scheme is invalidated,
and existing role registries renumber.** Chains, trees,
accumulator snapshots and any persisted proof computed with
untagged `Combine` must be regenerated, and a consumer that
assigned binary roles below 0x80 — the ledger's chain-link,
batch-node, mmr-node and chain-fork — moves them into the high
half and regenerates its vectors. For a consumer with nothing in
production this is a day of mechanical work by the generator built
for it; for one with archives it is a migration this RFC offers no
path for, which is the strongest available argument for landing it
before any consumer has archives.

## Open questions

None. The role width, the arity reservation, the byte layout, the
genesis treatment, the deletion of untagged `Combine` and the
retention of `Hash` are each settled above with the argument that
settles them, and the vectors fix the contract they produce. The
two decisions this revision reopened — discipline versus structure
for role disjointness, and replace versus grow for `Combine` —
are closed structurally and by replacement respectively, for the
reasons in the defect section and Alternative G. The one rule the
mechanism cannot enforce — one input kind per unary role — is not
open; it is decided against further structure and recorded as the
named residue in Drawbacks.

## Unresolved / future work

- **Tagged streaming.** A leaf too large to hold in memory needs
  the role written into a `Stream` before its data. The shape is
  a `Stream` constructor taking a `Role`, additive to this RFC and
  not needed by any current consumer — a claim that holds only
  because the ledger bounds its leaf input by its bytes-per-patch
  limit. A consumer whose leaves are unbounded needs this first,
  and the bound, not this RFC, is what currently spares them.
- **Whether `cas.Store` should tag its addresses.** RFC-0027's
  store addresses content by the digest of its bytes, which argues
  against a prefix. If a future consumer stores structured
  commitments there, the question returns.
- **A `TaggedHasher` capability interface**, if an implementation
  outside this module cannot adopt the two methods. Not proposed
  now: making mandatory behaviour optional lets an implementation
  present a `Hasher` that cannot be used to build a tree, and the
  consumer discovers it at a type assertion rather than at compile
  time. An optional interface is right for a capability some
  backends genuinely lack; every hash function can prefix a byte.
