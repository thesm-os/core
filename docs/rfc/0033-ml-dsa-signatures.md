---
rfc: 0033
title: ML-DSA Signatures
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-09-24
updated: 2026-09-24
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0020
---

# RFC-0033: ML-DSA Signatures

## Summary

We propose a package `crypto/sign/mldsa` that implements `sign.Signer`
and `sign.Verifier` for ML-DSA-44, ML-DSA-65 and ML-DSA-87, the
post-quantum signature scheme of FIPS 204, over the standard library's
`crypto/mldsa`. The FIPS 204 context string is fixed when a signer or
verifier is built, so one key can sign for one purpose per signer
without one purpose's signature verifying for another. Core's minimum
Go version is 1.27.0, the first release with `crypto/mldsa`.

## Motivation

- Core's signing seam offers Ed25519 and ECDSA P-384. Neither resists a
  quantum adversary, and signatures over long-lived records have to
  verify for as long as the records are kept.
- NIST standardised ML-DSA in FIPS 204. NSA's CNSA 2.0 requires
  ML-DSA-87 for national security systems, and Go 1.27 adds
  `crypto/mldsa`, which the FIPS 140-3 Go Cryptographic Module
  provides from version v1.26.0.
- Core's signing design planned ML-DSA for when the standard library
  exposes it, and noted that the seam needs no change to admit it.

## Detailed design

### The package

```go
// Package mldsa implements [sign.Signer] and [sign.Verifier] for
// ML-DSA, the module-lattice signature scheme of FIPS 204, over
// [crypto/mldsa].
package mldsa

// Params selects a FIPS 204 parameter set.
type Params uint8

const (
    // MLDSA44 is ML-DSA-44: 1,312-byte public keys and 2,420-byte
    // signatures.
    MLDSA44 Params = iota + 1

    // MLDSA65 is ML-DSA-65: 1,952-byte public keys and 3,309-byte
    // signatures.
    MLDSA65

    // MLDSA87 is ML-DSA-87: 2,592-byte public keys and 4,627-byte
    // signatures. CNSA 2.0 requires it.
    MLDSA87
)

// Algorithm returns the parameter set's long-term name:
// crypto.AlgMLDSA44, crypto.AlgMLDSA65 or crypto.AlgMLDSA87.
func (p Params) Algorithm() crypto.Algorithm

// Verifier verifies ML-DSA signatures under one public key and one
// context string. Safe for concurrent use.
//
// # Allocation contract
//
// KeyID, PublicKey and Algorithm are zero-alloc. Verify is
// zero-alloc.
type Verifier struct{ /* unexported fields */ }

// NewVerifier returns a Verifier for the encoded public key pub under
// context. pub is copied.
//
// Returns ErrPublicKey when pub is not a valid encoding for p, and
// ErrContext when context is longer than 255 bytes.
func NewVerifier(p Params, pub []byte, context string) (*Verifier, error)

// Signer signs with one ML-DSA private key under one context string.
// Every Signer is a Verifier for the same key and context. Safe for
// concurrent use.
//
// # Allocation contract
//
// Sign allocates the signature once. The standard library offers no
// buffer-passing signing function.
type Signer struct{ /* unexported fields */ }

// New returns a Signer for the private key derived from the 32-byte
// FIPS 204 seed, under context. seed is copied.
//
// Returns ErrSeed for a seed of another length, and ErrContext when
// context is longer than 255 bytes.
func New(p Params, seed []byte, context string) (*Signer, error)

// Generate returns a Signer for a fresh key whose 32-byte seed is read
// from r. A seeded r gives a reproducible key, which tests use.
func Generate(p Params, r rand.Rand, context string) (*Signer, error)

// Seed returns a copy of the private key's 32-byte seed, for storage
// in a custodian.
func (s *Signer) Seed() []byte

// KeyIDFromPub returns SHA-256 of the encoded public key, truncated to
// 16 bytes.
func KeyIDFromPub(pub []byte) sign.KeyID
```

`Signer.Sign` uses the standard library's hedged signing, which mixes
fresh randomness into each signature. FIPS 204 specifies that variant
as the default.
`Verifier.Verify` returns true only for a signature made under the same
key and the same context.

The package adds three names to `crypto`'s algorithm vocabulary:

```go
const (
    AlgMLDSA44 Algorithm = "ml-dsa-44"
    AlgMLDSA65 Algorithm = "ml-dsa-65"
    AlgMLDSA87 Algorithm = "ml-dsa-87"
)
```

Once released, the names and the key-ID derivation are frozen persisted
encodings under ADR-0016.

### The context string

FIPS 204 separates signatures made for different purposes with a
context string of up to 255 bytes, and the standard library passes it
in `Options.Context`. `sign.Signer.Sign` takes only the message, so the
context is set when the signer is built:

```go
checkpoints, err := mldsa.New(mldsa.MLDSA87, seed, "example/checkpoint/v1")
if err != nil {
    return err
}
cosignatures, err := mldsa.New(mldsa.MLDSA87, seed, "example/cosignature/v1")
if err != nil {
    return err
}
```

The two signers share a key and a `KeyID`. A signature made by one does
not verify under a verifier built with the other's context, so a
checkpoint signature cannot be replayed as a cosignature. The context
separates signatures inside the algorithm, and a `crypto.Framer`
domain separates them inside the message. A caller can use either or
both.

### Go version

Core's module declares `go 1.27.0`. The package builds wherever core
builds and does not need a build constraint. Go 1.27.1 contains no
security fixes, so the directive declares 1.27.0 and not 1.27.1. CI
resolves its toolchain from the directive, so every CI run tests the
declared minimum.

### Private keys

A private key is its 32-byte FIPS 204 seed. `New` takes the seed and
`Seed` returns it, and the standard library expands it into the
signing key. RFC 9881, which defines ML-DSA keys for X.509, makes the
seed the RECOMMENDED format for storing and transmitting a private key.
The expansion is one-way, so a store that kept only the expanded key
could never recover the seed. A custodian stores 32 bytes per key,
whatever the parameter set.

### Cost

Measured with Go 1.27.1 on an AMD Ryzen 9 9950X3D, three runs of 2,000
operations, 64-byte messages, on a machine running other work:

| Parameter set | Sign | Verify | Signature | Allocations |
|---|---|---|---|---|
| ML-DSA-44 | 228-279 µs | 83-86 µs | 2,420 B | 1 to sign, 0 to verify |
| ML-DSA-65 | 381-466 µs | 110-111 µs | 3,309 B | 1 to sign, 0 to verify |
| ML-DSA-87 | 406-425 µs | 183 µs | 4,627 B | 1 to sign, 0 to verify |

Ed25519 signs in about 11 µs. A caller that signs once per batch, as
core's signing design recommends, pays the ML-DSA cost once per batch.

### Errors

```go
var (
    // ErrSeed reports a seed that is not 32 bytes. It classifies as
    // errs.Invalid.
    ErrSeed = errs.WithClass(errors.New("mldsa: seed must be 32 bytes"), errs.Invalid)

    // ErrPublicKey reports a public key that is not a valid encoding
    // for the parameter set. It classifies as errs.Invalid.
    ErrPublicKey = errs.WithClass(errors.New("mldsa: invalid public key encoding"), errs.Invalid)

    // ErrContext reports a context string longer than 255 bytes. It
    // classifies as errs.Invalid.
    ErrContext = errs.WithClass(errors.New("mldsa: context longer than 255 bytes"), errs.Invalid)

    // ErrParams reports a Params value other than MLDSA44, MLDSA65 and
    // MLDSA87. It classifies as errs.Invalid.
    ErrParams = errs.WithClass(errors.New("mldsa: unknown parameter set"), errs.Invalid)
)
```

### Tests

- The conformance suite for `sign.Signer` and `sign.Verifier` runs for
  each parameter set.
- Signatures cross-verify in both directions with `crypto/mldsa` used
  directly.
- A signature made under one context fails verification under another,
  and under an empty one.
- `TestKeyIDStability` pins the key ID of the public key that a fixed
  seed gives, for each parameter set.
- `TestZeroAlloc` covers `Verify`, `KeyID`, `PublicKey` and
  `Algorithm`.

The standard library replays the ACVP known-answer tests in
`crypto/internal/fips140/mldsa`. Those vectors hash the expanded
private key, which `crypto/mldsa` does not expose, so core cannot
replay them through the public API.

## Alternatives considered

### A. One package per parameter set

Core's signing design planned `crypto/sign/mldsa44`, `mldsa65` and
`mldsa87`, one package per parameter set as Ed25519 and ECDSA P-384
have.

**Why not:** the three parameter sets differ only in the value passed to
the standard library, which exposes them as values of one type. Three
packages would triple the code and the tests for no difference in
behaviour. One package with a `Params` value keeps each signer's
`Algorithm` distinct, which is what verifier routing needs.

### B. A context parameter on `Sign`

`sign.Signer.Sign` would take a context string, or a capability
interface would add `SignWithContext`.

**Why not:** Ed25519 and ECDSA P-384 have no context string, so the
parameter would mean nothing for two of the three algorithms. A
context fixed at construction gives each purpose its own signer value,
which the caller already passes to the code that signs for that
purpose.

### C. Keep Go 1.26 and put build constraints on the package

Every file of the package would start with `//go:build go1.27`, and the
rest of core would keep its Go 1.26 minimum. On a Go 1.26 toolchain the
package would contain no files.

**Why not:**

- The constraint has to be on every file, generated tests included.
  `testkit sentinel` writes its test file without the source's
  constraint, so the package's own tests would fail to build on Go 1.26.
- A caller on Go 1.26 who imports the package gets a build error either
  way.
- Core's CI tools already need Go 1.27. `github.com/palantir/go-license`
  v1.50.0 declares `go 1.27.0`, and `make bootstrap` installs it.

### D. Deterministic signing

`crypto/mldsa` also offers `SignDeterministic`, which gives the same
signature for the same key and message.

**Why not:** FIPS 204 section 3.4 makes the hedged variant the default,
because fresh randomness "helps mitigate side-channel attacks". It
states that the deterministic variant "should not be used on platforms where
side-channel attacks are a concern". Tests do not need deterministic
signatures: they verify signatures and pin keys, not signature bytes.

## Drawbacks

- ML-DSA signatures are 38 to 72 times the size of an Ed25519
  signature, and signing costs 20 to 40 times as much.
- Core's minimum Go version rises to 1.27.0 for every caller, including
  callers that never sign with ML-DSA. The Go project supports Go 1.26
  until Go 1.28 is released.
- A signer per purpose means a caller that signs for three purposes
  builds three signers over one seed.
- `Signer.Seed` exposes the private seed, as the standard library's
  `PrivateKey.Bytes` does. A caller that keeps keys in a custodian
  stores the seed there and zeroes its own copy.

## Open questions

None.

## Unresolved / future work

- A `sign.StreamingSigner` for ML-DSA through the standard library's
  external μ, `crypto.MLDSAMu`. The standard library signs a
  pre-computed μ but verifies only whole messages, so a streaming
  verifier needs a later Go release.
- SLH-DSA, when the standard library provides it.
- Hybrid signatures that require both a classical and an ML-DSA
  signature. They belong to signature policies, not to this package.

## References

- NIST, FIPS 204, "Module-Lattice-Based Digital Signature Standard",
  August 2024, <https://nvlpubs.nist.gov/nistpubs/FIPS/NIST.FIPS.204.pdf>,
  section 3.4.
- RFC 9881, "Internet X.509 Public Key Infrastructure -- Algorithm
  Identifiers for the Module-Lattice-Based Digital Signature Algorithm
  (ML-DSA)", <https://www.rfc-editor.org/info/rfc9881/>.
- The Go project, release history and policy,
  <https://go.dev/doc/devel/release>.
- NSA, "The Commercial National Security Algorithm Suite 2.0 and
  Quantum Computing FAQ",
  <https://media.defense.gov/2022/Sep/07/2003071836/-1/-1/0/CSI_CNSA_2.0_FAQ_.PDF>.
- Go 1.27.1: `crypto/mldsa`, `api/go1.27.txt`, and
  `crypto/internal/fips140/mldsa/mldsa_test.go`.
- RFC-0013, the signing seam, and its plan for post-quantum
  signatures.
- ADR-0016, persisted encodings are frozen.
