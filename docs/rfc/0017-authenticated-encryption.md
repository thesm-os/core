---
rfc: 0017
title: Authenticated Encryption
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-08-03
updated: 2026-08-04
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0017: Authenticated Encryption

## Summary

`crypto.AEAD` — the standard library's `cipher.AEAD` plus the durable
identity a persisted ciphertext needs to outlive the build that wrote
it. Adds a nonce-managing `Seal`/`Open` pair, because the raw
`cipher.AEAD` contract puts nonce generation on the caller and that is
the single most common way authenticated encryption is deployed
incorrectly. Ships `crypto/aesgcm` over the standard library.

## Motivation

`core` can hash, authenticate and sign, and cannot encrypt. That is a
hole in a family that claims to cover cryptography, and it is the hole
that blocks anything protecting data at rest.

The contract itself is not missing: `crypto/cipher.AEAD` defines it,
and forking it would repeat the mistake RFC-0003 §E identified for
`hash.Hash`. What the standard library lacks is the identity half — the
`ID` and `Algorithm` every other `core` crypto seam carries — so that a
ciphertext written today can be opened by a build that does not yet
exist and by an implementation chosen at open time rather than compile
time.

There is a second gap and it is a safety one. `cipher.AEAD.Seal` takes
a nonce from the caller and says nothing about where it comes from.
Reusing a nonce under one key with AES-GCM does not degrade
confidentiality gracefully; it discloses the XOR of two plaintexts and
allows authentication-tag forgery for the whole key. A seam that hands
callers a raw `Seal` and no safe path is shipping the footgun.

## Detailed design

The interface lives in package `crypto`, beside `Hasher` and `MAC`,
with implementations in a subpackage. That is the layout the module
already uses for every seam whose value types are `crypto`'s own.

```go
// In package crypto.

// AEAD is an authenticated-encryption primitive with associated data.
// It embeds the standard library contract verbatim and adds identity.
//
// Callers persist Algorithm alongside the ciphertext and select the
// matching implementation at open time, exactly as they persist
// [Hasher.Algorithm] alongside a digest.
type AEAD interface {
    cipher.AEAD

    // ID is the build-local implementation identifier.
    ID() ID

    // Algorithm is the long-term, cross-build name. Persist it.
    Algorithm() Algorithm
}
```

### Nonce management

The embedded `cipher.AEAD` is available for callers who manage nonces
themselves — a counter under a per-message key, for instance, which is
both correct and cheaper than randomness. For everyone else the package
provides the safe path:

```go
// Seal draws a fresh random nonce from r and returns a sealed
// envelope: version || algorithm || nonce || ciphertext.
func Seal(a AEAD, r rand.Rand, plaintext, aad []byte) ([]byte, error)

// AppendSeal appends the envelope to dst, for callers reusing a buffer.
func AppendSeal(dst []byte, a AEAD, r rand.Rand, plaintext, aad []byte) ([]byte, error)

// Open parses the envelope and decrypts it.
func Open(a AEAD, sealed, aad []byte) ([]byte, error)

// AppendOpen appends the plaintext to dst.
func AppendOpen(dst []byte, a AEAD, sealed, aad []byte) ([]byte, error)

// PeekAlgorithm reads the algorithm an envelope names, without a key
// and without authenticating. It is how a caller selects which AEAD to
// open with.
func PeekAlgorithm(sealed []byte) (Algorithm, error)
```

`Seal` and `Open` sit beside `HashDomain` and `HashReader` as
package-level helpers over a seam, which is the shape `crypto` already
uses for operations that take an implementation as their first
argument.

`Seal` takes the randomness source rather than reaching for
`crypto/rand` directly, which keeps the seam's determinism story intact
and makes the entropy dependency visible at the call site. A caller
passing a deterministic source to `Seal` is doing something dangerous,
and the signature makes that visible rather than hiding it inside the
implementation.

Random nonces are safe for AES-GCM's 96-bit nonce at any message volume
a single key will realistically see; the doc comment states the bound
rather than leaving a reader to look it up.

### The envelope carries its own algorithm

A ciphertext must say what produced it. `AEAD` reports an `Algorithm`
for exactly the reason `Hasher` does — so an artefact written today can
be opened by a build that does not exist yet — and an envelope that
omits it leaves that identity in a side channel the caller has to keep
in step by hand.

The nonce is already prepended on the argument that *a nonce stored
apart from its ciphertext is a nonce that gets lost*. That argument does
not distinguish between the nonce and the algorithm: both are
non-secret, both are needed to open, and losing either makes the
ciphertext unrecoverable. Prepending one and not the other was
inconsistent rather than considered.

```text
version    1 byte              layout version, currently 1
algLen     1 byte              length of the algorithm name, 1..255
algorithm  algLen bytes        e.g. "aes-256-gcm"
nonce      a.NonceSize() bytes
body       ciphertext || tag
```

The header is not merely prepended — it is bound into what the tag
covers. The associated data handed to the underlying `cipher.AEAD` is
built with the package's own framer:

```go
f := NewFramer(scratch[:0], Domain{Name: "thesmos.crypto.aead", Version: 1})
f.String(string(a.Algorithm()))
f.Bytes(callerAAD)
```

Without that binding an attacker could rewrite the algorithm name in
transit; the open would then either fail with a confusing error or, for
two implementations sharing a nonce and key size, succeed under the
wrong primitive. Framing it rather than concatenating it is what stops
a crafted `aad` from imitating a longer algorithm name.

### Two versions, deliberately

The layout version appears twice: once as a plaintext byte, once as
`Domain.Version` inside the authenticated bytes.

The wire byte gives a clean rejection. A reader that meets an unknown
version returns `ErrEnvelopeVersion` before any key touches the input,
which is what an operator migrating formats needs to see instead of an
opaque authentication failure.

`Domain.Version` makes the rejection unforgeable. The domain tag is part
of what the tag authenticates, so bytes written under one layout cannot
authenticate under another even if the plaintext version byte is
rewritten. The wire byte is for diagnosis; the domain version is the
guarantee.

**An unknown version is rejected, never partially parsed.** This is the
opposite of the rule for `traceparent` in RFC-0020, where a higher
version may append fields a v1 reader ignores. Forward-compatible
parsing of a security envelope is a downgrade path: it invites a reader
to act on the part of a structure it recognises while ignoring the part
that changed the meaning.

### No fallback to a headerless ciphertext

`Open` does not attempt a bare `nonce || ciphertext` read when the
header does not parse. A reader that falls back is a reader an attacker
can force into the weaker mode by stripping bytes, and the algorithm
binding then covers nothing.

`core` is pre-1.0 and this changes a wire format, so ciphertexts written
by an earlier build do not open. That is the intended outcome: a
migration a caller performs deliberately is better than a compatibility
path that is permanently attackable. The failure is loud — an old
ciphertext's first nonce byte is a version this build does not know,
almost always, and the algorithm check catches the rest.

### What the envelope does not carry

No key identifier. `core` cannot know how a deployment names keys, and a
field it cannot specify is a field every implementation fills
differently. A caller that needs one puts it in `aad`, where it is
authenticated without `core` having invented a schema for it.

No ciphertext length. The storage or transport that hands over the bytes
already knows how many there are, and a second copy of a length is a
second thing that can disagree.

### Algorithms

Constants extend the existing open-string vocabulary:

```go
AlgAES128GCM         Algorithm = "aes-128-gcm"
AlgAES256GCM         Algorithm = "aes-256-gcm"
AlgChaCha20Poly1305  Algorithm = "chacha20-poly1305"
AlgXChaCha20Poly1305 Algorithm = "xchacha20-poly1305"
```

Implementations ship for the two the standard library can express, in
`crypto/aesgcm`. The ChaCha20 constants are reserved and not
implemented: reserving the name now costs nothing and prevents two
spellings appearing if a standard-library implementation lands later.

### Errors

Per the module's convention, in `crypto/errors.go`:

```go
var ErrKeySize          = errors.New("crypto: key length does not match the algorithm")
var ErrCiphertextShort  = errors.New("crypto: sealed envelope truncated")
var ErrEnvelopeVersion  = errors.New("crypto: unknown sealed-envelope version")
var ErrAlgorithmMismatch = errors.New("crypto: envelope algorithm does not match the AEAD")
var ErrAlgorithmSize    = errors.New("crypto: algorithm name empty or over 255 bytes")
```

Open failures from the underlying `cipher.AEAD` are returned unwrapped.
An authentication failure must not be distinguishable from a
malformed-ciphertext failure by anything a caller can branch on, and
adding a distinct sentinel would create exactly that distinction.

The three envelope errors do not weaken that rule, and the reason is
worth stating because it is the test any future sentinel here must
pass: each is decided from public header bytes before a key is used.
`ErrEnvelopeVersion` and `ErrAlgorithmMismatch` report facts an attacker
supplied and can already read; neither is a function of the key, the
plaintext, or the tag. The rule forbids an oracle, not an error — what
it rules out is a sentinel that distinguishes *why the tag did not
verify*, and no sentinel here does.

`ErrAlgorithmSize` covers both directions of the same malformation. On
seal it means the `AEAD` reports a name that is empty or longer than the
header can express, which is a defect in that implementation; it returns
an error rather than panicking because this module does not panic in
production paths. On open and on `PeekAlgorithm` it means the envelope
declares a zero-length name, which no `AEAD` can match and which would
otherwise surface as a confusing mismatch against a name that was never
there.

### Rotation is the caller's dispatch

There is still no helper that tries several algorithms against a
ciphertext. What changes is where the dispatch key comes from:
`PeekAlgorithm` reads it from the ciphertext, so the caller's map lookup
no longer depends on a side channel staying in step with the bytes.

That is the whole benefit of the envelope. A helper that iterated
candidates would remain wrong for the reasons it always was — slower,
and a weaker posture than knowing which key and algorithm should have
worked — and `PeekAlgorithm` removes the only excuse for wanting one.

`PeekAlgorithm` authenticates nothing, and says so. The name it returns
is an attacker-supplied hint used to *select* a key, never to decide
whether the bytes are genuine; the tag decides that, and the selected
algorithm is bound into what the tag covers. A caller must treat an
unrecognised name as a rejection rather than a reason to try something.

### Allocation contract

Measured, not asserted:

| | allocs/op |
|---|---|
| `Seal` | 2 |
| `AppendSeal` | 1 |
| `AppendOpen` | 0 |
| `PeekAlgorithm` | 1 |
| embedded `cipher.AEAD.Seal`, sized dst | 0 |

`Seal` sizes its buffer once, from the header, nonce, plaintext and
overhead together, rather than regrowing as each field is appended —
four allocations it used to pay and no longer does.

The associated-data frame is scratch drawn from a package-level pool,
the same treatment `HashDomain` gets and for the same reason. It costs
nothing per call.

**`AppendSeal`'s remaining allocation is the entropy read, and it is
not removable from here.** Drawing the nonce through the `rand.Rand`
interface costs one 16-byte allocation because an indirect call cannot
be shown not to retain its buffer; the same read is zero-alloc when the
compiler can devirtualise it, which is why `rand/crypto`'s own
benchmarks report zero. Reading into pooled scratch and copying does
not help — it is the indirect call, not the destination. `AppendOpen`
draws no entropy and is genuinely zero-alloc.

`PeekAlgorithm` allocates the string it returns.

The embedded `cipher.AEAD` methods keep the standard library's
append-style contract, so a caller wanting neither the envelope nor the
managed nonce pays nothing at all.

## Alternatives considered

### A. Define a fresh AEAD interface rather than embedding `cipher.AEAD`

**Why not:** RFC-0003 §E settled this for hashing and the reasoning
carries. Forking a standard-library contract means every implementation
writes an adapter, and every caller holding a `cipher.AEAD` from
elsewhere cannot pass it in.

### B. Put the seam in its own `crypto/aead` package

**Why not:** it stutters as `aead.AEAD`, and it splits the crypto
vocabulary — `Algorithm` and `ID` live in `crypto`, so the seam would
import its own value types from its parent. `Hasher` and `MAC` set the
precedent and this follows it. `crypto/sign` is the exception because
it introduces `KeyID`, a value type of its own.

### C. Ship only the interface, no `Seal`/`Open` helpers

**Why not:** it leaves the nonce problem entirely to the caller, which
is where it currently goes wrong. The helpers are eight lines each and
they are the difference between a seam that is safe by default and one
that is safe if you already knew the failure mode.

### D. Generate nonces from `crypto/rand` internally, with no `rand.Rand`

**Why not:** it hides an entropy dependency inside a function whose
signature says it is pure, and it makes the package untestable without
real randomness.

### E. Leave the algorithm out and let the caller persist it

The original design, and what `Hasher` does for digests.

**Why not:** a digest is a value a caller stores in a field it defines,
alongside whatever else that record needs — the algorithm sits in the
next column. A sealed ciphertext is an opaque blob handed to storage
that has no other columns, and the nonce prefix already conceded that
point. Splitting the identity out means every caller reinvents the same
two-field record, and the ones that forget find out when they rotate.

### F. Put the build-local `ID` in the envelope instead of `Algorithm`

`ID` is fixed-size, so the header would need no length byte.

**Why not:** `ID` is documented as build-local and explicitly not for
cross-build identification. A ciphertext outlives the build that wrote
it, which is the entire premise. Fourteen bytes of header is the cost of
the field that is still meaningful in five years.

### G. A registry of numeric algorithm codes

Compact and fixed-width, like a TLS cipher-suite registry.

**Why not:** `core` would own the registry, and a registry is external
data with a release cadence `core` cannot meet — the same reason
`money` stays out. `Algorithm` is already an open string precisely so a
consumer can add one without waiting for `core`, and a numeric code
would take that back.

## Drawbacks

- `Seal` and `Open` define a wire format `core` owns, and it is now
  larger than a nonce prefix. A caller with an existing framing cannot
  use them and must drop to the embedded interface.
- The header costs two bytes plus the algorithm name — fourteen for
  `aes-256-gcm`. For a payload of a few bytes that is a real proportion
  of the record, and a caller storing many tiny ciphertexts under one
  known algorithm is paying for agility it has decided not to use.
- Ciphertexts written before this change do not open. `core` is pre-1.0
  and the alternative was a permanent downgrade path, but it is still a
  migration someone has to run.
- The seam adds identity to a contract whose stdlib form has none, so a
  `cipher.AEAD` from elsewhere does not satisfy `crypto.AEAD` without a
  wrapper. That is the cost of the identity half and it is the whole
  point of the package.
- Reserving two algorithm constants with no implementation means
  `Algorithm` values exist that nothing can open. A caller can persist
  one by mistake.
- AES-GCM's security bound on random nonces is a number in a doc
  comment. Nothing enforces it, and a caller encrypting enough messages
  under one key will exceed it without any signal.
- `crypto` grows two more package-level functions and two more
  sentinels, in a package that is already the module's largest.

## Open questions

None. Both questions this RFC previously carried are resolved above: a
deterministic `rand.Rand` cannot be detected through the interface, so
the mitigation is the doc warning on `Seal` and a conformance
assertion that two seals of one plaintext differ; and algorithm
rotation is the caller's dispatch on the persisted `Algorithm`, not a
helper.

## Unresolved / future work

- ChaCha20-Poly1305 and XChaCha20-Poly1305 implementations, if and when
  the standard library expresses them.
- A streaming AEAD for payloads that do not fit in memory. The
  construction is not a straightforward extension — it needs chunk
  framing and a chunk-counter nonce scheme — and it deserves its own
  RFC rather than a subsection here.
- Key commitment, which AES-GCM does not provide and which matters
  wherever a ciphertext may be opened under more than one candidate key.
