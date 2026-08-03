---
rfc: 0017
title: Authenticated Encryption
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-08-03
updated: 2026-08-03
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
// Seal draws a fresh random nonce from r, encrypts plaintext under a
// with aad as associated data, and returns nonce || ciphertext.
//
// The nonce is prepended rather than returned separately because a
// nonce stored apart from its ciphertext is a nonce that gets lost,
// and a lost nonce is an unrecoverable value.
//
// Use this unless you have a specific reason to manage nonces
// yourself. Reusing a nonce under one key breaks AES-GCM completely:
// it discloses the XOR of the two plaintexts and permits tag forgery
// for every message under that key.
func Seal(a AEAD, r rand.Rand, plaintext, aad []byte) ([]byte, error)

// Open splits sealed into nonce and ciphertext and decrypts it.
// Returns ErrCiphertextShort if sealed is smaller than a.NonceSize().
func Open(a AEAD, sealed, aad []byte) ([]byte, error)
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
var ErrKeySize         = errors.New("crypto: key length does not match the algorithm")
var ErrCiphertextShort = errors.New("crypto: ciphertext shorter than the nonce")
```

Open failures from the underlying `cipher.AEAD` are returned unwrapped.
An authentication failure must not be distinguishable from a
malformed-ciphertext failure by anything a caller can branch on, and
adding a distinct sentinel would create exactly that distinction.

### Rotation is the caller's dispatch

There is no helper that tries several algorithms against a ciphertext.
The persisted `Algorithm` names exactly one implementation, so the
caller's dispatch is a map lookup, and a helper that iterated
candidates would turn a deterministic open into a series of
authentication failures — which is both slower and a weaker security
posture than knowing which key and algorithm should have worked.

### Allocation contract

`Seal` allocates one buffer of `NonceSize() + len(plaintext) +
Overhead()`. `Open` allocates one plaintext buffer. The embedded
`cipher.AEAD` methods keep the standard library's append-style
contract, so a caller managing its own buffers can avoid both.

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

## Drawbacks

- `Seal` and `Open` define a ciphertext framing — nonce prefix — that is
  now a wire format `core` owns. A caller with an existing framing
  cannot use them and must drop to the embedded interface.
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
