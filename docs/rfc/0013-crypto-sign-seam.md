---
rfc: 0013
title: Public-Key Signing Seam
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-05-06
updated: 2026-05-06
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0013: Public-Key Signing Seam

## Summary

`crypto.sign` — asymmetric-signing companion to the existing
`crypto.Hasher` (content-addressed) and `crypto.MAC` (symmetric)
seams. Splits the API into `Verifier` (public-key only) and
`Signer` (Verifier + private key), with optional
`StreamingSigner` / `StreamingVerifier` capability interfaces for
hash-then-sign algorithms. Implementations land per algorithm
under `crypto/sign/{ed25519,ecdsap384}`. Future PQ signatures
(ML-DSA, SLH-DSA) and threshold signing slot in additively when
their preconditions land.

## Motivation

Receipts, batch-root attestations, audit envelopes, and
epoch-close signatures all need keyed authentication where the
verifier is a *different party* than the signer. Symmetric
`crypto.MAC` doesn't fit — the verifier must hold the signing
key, which means losing audit-trail isolation. The asymmetric
seam makes the clean shape possible: signer nodes hold private
keys; verifier nodes (audit services, webhook receivers,
offline auditors) hold only public keys.

The space PoC (`thesmos/space/signer/...`) already shipped a
working `Signer` interface with Ed25519 / ECDSA P-384 / FROST
threshold variants. This RFC promotes the validated parts into
core, fixes inconsistencies, and lays groundwork for the
not-yet-exposed PQ stdlib paths.

## Detailed design

```go
// Package crypto/sign

type KeyID [16]byte
func (KeyID) String() string

type Verifier interface {
    KeyID() KeyID
    PublicKey() []byte
    Algorithm() crypto.Algorithm
    Verify(message, signature []byte) bool
}

type Signer interface {
    Verifier
    Sign(message []byte) ([]byte, error)
}

type StreamingSigner interface {
    Signer
    NewSignStream() SignStream
}

type SignStream interface {
    io.Writer
    SignAndReset() ([]byte, error)
}

type StreamingVerifier interface {
    Verifier
    NewVerifyStream() VerifyStream
}

type VerifyStream interface {
    io.Writer
    Verify(sig []byte) bool
}
```

### Splitting Signer and Verifier

The PoC had a single `Signer` interface that did both. This RFC
splits them: `Verifier` is the smaller surface, `Signer` extends
it. Verifier-only consumers — audit services, webhook receivers
holding only a public key — construct a `Verifier` directly from
public-key bytes without ever holding a private key. Generic
code that only verifies accepts either type without an extra
adapter, because every `Signer` is-a `Verifier`.

This is the load-bearing decision RFC-0012 deferred to this
round.

### KeyID lives in `crypto/sign`, not `crypto`

`crypto.ID` identifies an *implementation* (e.g. "ed25519/v1");
`KeyID` identifies a *key* (the SHA-256 prefix of a specific
public key). Different concepts, distinct types, distinct
packages. `MAC` doesn't need a KeyID method (symmetric, key
bytes shared); only public-key signing has the "verifier needs
to identify which public key" requirement.

### Canonical KeyID derivation

Each implementation ships `KeyIDFromPub` deriving
`SHA-256(canonical-public-key-bytes)[:16]` from a fixed-width
canonical encoding:

- **Ed25519**: SHA-256(raw 32 public-key bytes)[:16].
- **ECDSA P-384**: SHA-256(SEC 1 uncompressed point,
  `0x04 || X(48 BE) || Y(48 BE)`, 97 bytes)[:16].

Fixed-width big-endian coordinates make the derivation
deterministic across builds, languages, and verifier services
— that's the load-bearing property for receipt-routing trust
stores. The per-package `TestKeyIDStability` fixture locks the
encoding via hardcoded vectors. CI fails on encoding drift.

For ECDSA P-384, KeyID is derived from the SEC 1 *uncompressed*
encoding (the on-curve coordinates), not the *PKIX* encoding
(which `Verifier.PublicKey` returns). PKIX is variable-length
and embeds curve OIDs and DER framing; we can't use it for a
stable cross-system KeyID. The two encodings co-exist:
`PublicKey()` is the over-the-wire shape consumers archive;
KeyID is the routing identifier.

### Whole-message Sign, no SignTo

Production signing surfaces in this ecosystem are bounded-size
(receipts, batch roots, epoch attestations, audit envelopes —
≤ 1 MB realistic, ≤ 100 KB typical). Large artefacts are signed
via Merkle root (computed through `crypto.Hasher.NewStream`),
and the root itself is a small message. The seam therefore ships:

```go
Sign(msg []byte) ([]byte, error)
```

— and that's it. No `SignTo(dst, msg)`, no `SignDigest(d Digest)`.
Reasoning:

- **No streaming on `Signer`.** Ed25519 PureEdDSA cannot stream
  (RFC 8032 §5.1.6 needs `M` in two SHA-512 computations, the
  second depending on the first). A streaming API would force
  internal buffering — that's not streaming, it's "buffer and
  hash twice at finalise." The seam refuses to ship dishonest
  surface; ECDSA satisfies the optional `StreamingSigner` and
  Ed25519 does not.

- **No `SignTo(dst, msg)`.** Stdlib's `ed25519.Sign` and
  `ecdsa.SignASN1` don't expose buffer-passing APIs — both
  always allocate the returned signature internally. A
  `SignTo` wrapper around stdlib *cannot* reduce the
  per-call allocation count, so shipping it would mislead
  consumers expecting parity with `hash.Sum(b)`. If stdlib
  later gains an append-style sign API, the right way to add
  it is via an optional `AppendSigner` capability interface
  (same pattern as `StreamingSigner`) — additive,
  non-breaking, opt-in. Putting `SignTo` on `Signer` directly
  in v1 forecloses that path.

- **No `SignDigest(d Digest)`.** Ed25519 PureEdDSA signs the
  raw message, not a digest. Distinguishing pre-hashed and raw
  inputs at the API layer would surface algorithm internals
  consumers shouldn't have to know. Callers who want to sign
  a digest pass the digest bytes to `Sign` — Ed25519 will
  produce a PureEdDSA signature over the digest bytes
  (different from Ed25519ph), ECDSA P-384 will hash-then-sign
  (yielding SHA-384 of the digest, then sign).

The Sign doc-comment is explicit about RFC 8032 §5.1.6
(PureEdDSA) vs §5.1 (Ed25519ph): external verifiers expecting
PreHash will not accept signatures produced here. Documentation
discipline, not API limitation.

### Optional StreamingSigner / StreamingVerifier

Hash-then-sign algorithms (ECDSA P-384 today, ML-DSA / SLH-DSA /
Ed25519ph in the future) absorb the message into an incremental
hash and run the curve / lattice operation once at finalise.
Streaming is natural and avoids buffering arbitrary-size
messages. The optional capability interfaces let consumers
type-assert and use streaming when available:

```go
var s sign.Signer = sn
if streamer, ok := s.(sign.StreamingSigner); ok {
    str := streamer.NewSignStream()
    io.Copy(str, large)
    sig, err := str.SignAndReset()
} else {
    sig, err := s.Sign(buffered)
}
```

`SignAndReset` finalises and resets in one call so the same
stream can be reused for the next message. `VerifyStream.Verify`
is single-use (verification is naturally one-shot per message).

### Generate APIs are intentionally asymmetric

`crypto/sign/ed25519.Generate(r rand.Rand)` honours the supplied
randomness source — `crypto/ed25519.GenerateKey` in Go 1.26
still uses the reader. Tests pass `rand/seeded` for
deterministic key generation; production callers pass a
CSPRNG-backed `rand.Rand`.

`crypto/sign/ecdsap384.Generate()` takes no parameter.
`crypto/ecdsa.GenerateKey` in Go 1.26 ignores the supplied
reader and draws from the runtime's internal entropy unless
`GODEBUG=cryptocustomrand=1` is set. Accepting a `rand.Rand`
that the stdlib silently ignores would be a misleading API
shape — symmetry that's a lie. Tests requiring deterministic
ECDSA key generation use `testing/cryptotest.SetGlobalRandom`,
which seeds the runtime's internal RNG.

The asymmetry is intentional: each package's API matches what
the underlying stdlib actually honours. If a future Go release
restores reader-honoring behaviour for ECDSA, the ECDSA
`Generate` signature can grow a `rand.Rand` parameter
non-breakingly via a separate constructor. We preserve that
option by not lying today.

### Allocation contract

- `Verifier.KeyID`, `Verifier.PublicKey`, `Verifier.Algorithm`:
  zero-allocation across all implementations.
- `Verifier.Verify`: zero-allocation on **Ed25519** (the stdlib
  primitive avoids heap allocation); ECDSA P-384 verification
  allocates because `ecdsa.VerifyASN1` performs big.Int
  arithmetic — a stdlib constraint. The interface
  documentation is honest about the asymmetry.
- `Signer.Sign`: always allocates the returned signature slice
  (stdlib constraint). Hot-path consumers amortise this with
  batch-root signing rather than per-entry signing.
- `StreamingSigner.SignAndReset`: same allocation as `Sign`
  (the stdlib primitive); `Stream.Write` is zero-alloc.
- `StreamingVerifier`: `Stream.Write` is zero-alloc;
  `Stream.Verify` inherits `Verify`'s alloc behaviour.

### Failure semantics

- **Constructor errors** (wrong-curve, wrong-size key, malformed
  PKIX): typed sentinel errors per package. `errors.Is`-based
  dispatch.
- **Sign errors**: ECDSA can fail (entropy exhaustion); Ed25519
  cannot. The `error` return is for interface conformance.
  Sign errors wrap the stdlib's underlying cause via `%w`.
- **Verify failures**: single `bool false`. Distinguishing
  malformed-signature, length-mismatch, and cryptographic-
  invalidity through separate returns risks timing-side-channel
  oracles. Callers requiring per-failure diagnostics validate
  signature length / format separately before calling Verify.
- **No panics in production paths**.

### What's intentionally out of scope

- **FIPS-gated wrappers** (hard refusal outside `fips140=on`).
  Stay in `space/` — they encode policy, not primitive. The
  non-gated implementations here run through Go's
  FIPS-validated module under `GODEBUG=fips140=on`
  transparently.
- **Threshold signing** (FROST per RFC 9591). Production
  threshold needs an audited non-stdlib library
  (`bytemare/frost`). When the seam lands, it's its own
  subpackage with its own RFC.
- **PQ signatures** (ML-DSA, SLH-DSA). Go 1.26 has
  `crypto/internal/fips140/mldsa` but doesn't expose it
  publicly; SLH-DSA isn't in stdlib at all. When stdlib
  promotes them out of `internal/fips140/`, both slot in
  additively as `crypto/sign/mldsa{44,65,87}` and
  `crypto/sign/slhdsa{...}` packages with new `Algorithm`
  constants. The seam shape needs no changes — the open
  `Algorithm` vocabulary and the `StreamingSigner` capability
  interface accommodate them.
- **PQ KEMs** (ML-KEM). Different operation entirely
  (encapsulation vs signing). Lands in a separate
  `crypto/kem` seam in a future round, with first-class hybrid
  composition (X25519 + ML-KEM-768 via HKDF) for EU / NSA
  CNSA 2.0 / BSI / ANSSI compliance through ~2030.

### EU compliance posture

The open-string `Algorithm` vocabulary and the consumer-side
policy-gate pattern accommodate BSI (Germany TR-02102), ANSSI
(France), ENISA (EU), and eIDAS 2.0 deployments without
modification:

- BSI-approved algorithms: same NIST-standardised primitives
  shipped here (ed25519, ecdsa-p384) plus PQ when stdlib
  exposes them. Hybrid KEM lands separately.
- Regional gating: the same wrapper pattern as
  `space/signer/ed25519fips` works for `space/signer/ed25519bsi`,
  `space/signer/ecdsap384anssi`, etc. — consumer-side policy.
- SLH-DSA is the EU-conservative choice for long-lived
  signature attestations (eIDAS, 30-year trust scenarios)
  because the security assumption is "the underlying hash is
  collision-resistant," not "lattice problems remain hard." Not
  available yet, but the seam supports it when stdlib does.

## Alternatives considered

### A. Single Signer interface (no Verifier split)

Matches the space PoC. Rejected: verifier-only services would
have to construct a "Signer" type without a private key, or we
ship two parallel surfaces. The split is cleaner.

### B. SignTo(dst, msg) on Signer

Stdlib idiom (`hash.Sum(b)`, `binary.Append*`). Rejected because
stdlib's signing primitives don't expose buffer-passing APIs;
SignTo around stdlib cannot reduce allocations and would
mislead consumers. Future-proof via optional `AppendSigner`
capability interface if stdlib ever opens the door.

### C. SignDigest(d Digest) on Signer

Lets consumers sign a pre-computed digest without re-hashing.
Rejected: surfaces algorithm internals (Ed25519 vs Ed25519ph
behave differently for "sign a digest"). Callers who want to
pre-hash pass the digest bytes to Sign — the algorithm decides
how to interpret them, the consumer doesn't have to.

### D. Streaming on Signer (mandatory)

Force every Signer to implement Stream(). Rejected: Ed25519
PureEdDSA cannot stream without internal buffering. Optional
capability interface is honest.

### E. Constant-time Verify (one-bit failure interpretation)

Adopted. The bool collapse closes the timing-oracle hazard
that distinguishing malformed-from-invalid would expose.
Callers who need diagnostics validate length / format before
Verify.

### F. Combine Signer + KEM into one seam

Rejected. KEM operations (Encapsulate, Decapsulate) have
different shapes and use cases (key exchange, not
authentication). Separate seam in a future round.

## Drawbacks

- The split between `Verifier.PublicKey()` (PKIX) and
  `KeyIDFromPub` (SEC 1) for ECDSA looks asymmetric. It's the
  necessary asymmetry: PKIX is the wire / archive format
  (consumer archives this); SEC 1 is the cross-system stable
  identifier (consumers route on this). Documented; the test
  vector locks the SEC 1 encoding.
- ECDSA Verify allocates due to stdlib's big.Int arithmetic;
  Ed25519 doesn't. Hot-path consumers needing zero-alloc Verify
  use Ed25519 (the default) and document why. ECDSA P-384 is
  primarily for FIPS / regulated deployments where the
  allocation cost is a non-issue.
- ECDSA Generate non-determinism on Go 1.26 is a stdlib
  intentional change (security hardening); we propagate the
  caveat at the seam doc.

## Open questions

None.

## Unresolved / future work

- PQ signature implementations (ML-DSA, SLH-DSA) land
  additively when stdlib exposes them publicly. No interface
  shape change required.
- Threshold signing seam (FROST) lands as its own subpackage
  with its own RFC when consumers need it.
- Optional `AppendSigner` capability interface lands if stdlib
  exposes append-style signing primitives.
- `crypto/kem` seam (ML-KEM, X25519, hybrid combiner) lands as
  its own RFC in a follow-up round.
