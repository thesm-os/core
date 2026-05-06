---
rfc: 0003
title: Cryptographic Hash Seam
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-05-06
updated: 2026-05-06
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0003: Cryptographic Hash Seam

## Summary

A unified hash-function seam — `crypto.Hasher` — that consumers
compute digests and chain extensions through, instead of binding
to a specific algorithm at compile time. The seam covers
single-shot hashing, two-digest combination, and streaming for
arbitrary-length inputs. A single `Digest` value type covers
256-, 384-, and 512-bit outputs in one comparable, zero-alloc
shape via a fixed-max byte array plus a size tag. Six
implementations ship: SHA-256, SHA-384, SHA-512, SHA3-256,
SHA3-384, SHA3-512.

## Motivation

Library code that calls `crypto/sha256.Sum256` (or any other
specific algorithm) directly cannot be configured to use a
different algorithm without a code change. For consumers
operating under regulatory regimes that mandate algorithm
choices — BSI TR-02102 (Germany), ANSSI RGS (France), NSA CNSA
2.0 (United States), the EU AI Act's record-keeping
requirements for high-risk AI — algorithm-agility is a
deployment property, not a code property.

Three concrete forces shape the design:

- **Receipt durability.** Audit chains, signed receipts, and
  content-address tables must survive algorithm rotation. A
  receipt produced under SHA-256 in 2026 and verified under
  SHA3-512 in 2030 needs to carry an algorithm identifier
  alongside the digest. Both [Hasher.ID] (build-local) and
  [Hasher.Algorithm] (long-term) are persisted with each
  digest.

- **Variable-size digests in one type.** SHA-256 produces 32
  bytes, SHA-384 produces 48, SHA-512 / SHA3-512 produce 64.
  Three parallel `Digest256` / `Digest384` / `Digest512` types
  would force callers to switch on the producing algorithm at
  every receiving site. A single `Digest` shape with a runtime
  size tag lets one consumer hold a heterogeneous mix
  transparently.

- **Streaming for agentic contexts.** Large inputs (model
  artefacts, long agentic conversation contexts, batched audit
  payloads) cannot be loaded into memory before hashing. The
  seam exposes a `Stream` for `io.Reader`-driven hashing with
  amortised allocation across many digests via `Reset`.

## Detailed design

### `Digest`

```go
const (
    DigestSize256 = 32 // SHA-256, SHA3-256, …
    DigestSize384 = 48 // SHA-384, SHA3-384
    DigestSize512 = 64 // SHA-512, SHA3-512
    MaxDigestSize = DigestSize512
)

type Digest struct {
    bytes [MaxDigestSize]byte
    size  uint8
}

func (d Digest) Size() int                 // active prefix length
func (d Digest) Bytes() []byte             // slice over the active prefix
func (d Digest) IsZero() bool
func (d Digest) Equal(other Digest) bool
func (d Digest) Compare(other Digest) int  // lexicographic; bytes.Compare semantics
func (d Digest) String() string            // hex-encoded active prefix
```

`Digest` is comparable (`==` works), pass-by-value, zero-alloc.
The fixed-max layout costs 24 % of wire space when SHA-256 is in
use (32 bytes paying for 64-byte slot) but eliminates the
slice-allocation, slice-aliasing, and map-key-incompatibility
problems of a `[]byte`-shaped digest. The design pays a
predictable layout for predictable behaviour — exactly what
audit code wants.

### `Hasher`

```go
type Hasher interface {
    ID() ID
    Algorithm() Algorithm
    Hash(data []byte) Digest
    Combine(left, right Digest) Digest
    NewStream() Stream
}
```

`ID` is a 16-byte build-local identifier (`"sha256/v1"`
zero-padded); `Algorithm` is the long-term cross-build name
(`"sha-256"`). Both are persisted alongside digests; consumers
match against whichever fits their durability story.

`Hash` is the one-shot path. `Combine` is the
audit-chain / Merkle-accumulator hot path: implementations
hash the 64-byte (or 96, or 128) concatenation of two digests
in a single block where the algorithm permits. `NewStream`
returns the streaming primitive.

### `Stream`

```go
type Stream interface {
    io.Writer       // Write returns (len(p), nil) per stdlib hash.Hash contract
    Sum() Digest    // snapshot finalisation; does not reset state
    Reset()         // clear state for reuse
}
```

The output buffer lives on the heap-allocated stream itself
(reusing memory `NewStream` already owned), so `Sum` is
zero-alloc despite passing a slice through the stdlib
`hash.Hash` interface boundary. `Write` and `Reset` are
zero-alloc by construction. A long-lived consumer constructs
one `Stream` per goroutine and amortises the single
construction allocation across millions of digests via
`Reset`.

### `Algorithm` vocabulary

```go
type Algorithm string

const (
    AlgSHA256   Algorithm = "sha-256"
    AlgSHA384   Algorithm = "sha-384"
    AlgSHA512   Algorithm = "sha-512"
    AlgSHA3_256 Algorithm = "sha3-256"
    AlgSHA3_384 Algorithm = "sha3-384"
    AlgSHA3_512 Algorithm = "sha3-512"
)
```

Open-string vocabulary type; consumers add their own constants
(future signing / AEAD / KEM seams will define theirs in the
same shape). The same string persists into receipts, signature
envelopes, and audit headers.

### Domain separation

```go
func HashDomain(h Hasher, domain []byte, parts ...[]byte) Digest
```

Single helper that streams a caller-supplied domain tag
followed by the input parts. core ships **no** domain constants
— each consumer (audit chain, content addressing, idempotency
keys) defines its own. This avoids baking one project's
namespace into the wire format produced by core.

### Algorithm-to-implementation registry

| Algorithm | Subpackage | Notes |
|---|---|---|
| SHA-256 | `crypto/sha256` | FIPS 180-4 |
| SHA-384 | `crypto/sha512` | FIPS 180-4; CNSA 2.0 mandate ≥2027 |
| SHA-512 | `crypto/sha512` | FIPS 180-4 |
| SHA3-256 | `crypto/sha3` | FIPS 202; sponge construction |
| SHA3-384 | `crypto/sha3` | FIPS 202 |
| SHA3-512 | `crypto/sha3` | FIPS 202; PQC parameter expansion |

Every implementation:
- Returns a value-type `Hasher` (zero value usable, no pointer
  ceremony).
- Reports a stable `ID` (`"sha256/v1"`, `"sha3-512/v1"`, …) and
  the matching `Algorithm` constant.
- Is zero-alloc on `Hash`, `Combine`, and the `Stream` hot path
  (Write / Sum / Reset).
- Carries NIST FIPS 180-4 / 202 test vectors plus a
  `TestZeroAlloc` suite enforcing the documented contracts.

## Alternatives considered

### A. Slice-shaped `Digest` (`type Digest []byte`)

Variable-length naturally; no wasted space.

**Rejected:** breaks comparability (`==` doesn't work, can't be
a map key, can't participate in struct equality), forces
heap allocation per `Hash` call (slice header escapes through
the interface boundary), and creates an aliasing footgun where
mutation of one slice value silently corrupts every receipt
holding a reference. For a foundational audit primitive, those
are losses far larger than the 24 % wire-space gain.

### B. Three parallel `DigestN` types

`Digest256`, `Digest384`, `Digest512` — each its own
fixed-array type, three parallel `HasherN` interfaces.

**Rejected:** every consumer holding a "some digest" reference
has to switch on the type at every callsite. Aggregate
structures (chain entries, signed receipts) become generic-
parameterised or carry a `union` field. The complexity tax
dwarfs the cleanliness win.

### C. Domain tags as constants in core

`crypto.DomainChain = "thesmos:chain:"`, `DomainProof = …`, etc.

**Rejected:** core cannot bake one project's namespace into
the wire format. Domain separation is a property of the
consumer's protocol; consumers declare their own tags. core
ships the helper, not the values.

### D. `ChainHasher`-style four-method explosion

Like thesmos's `Chain` / `ChainDigests` / `Blob` / `DataDigest`
quartet — each a distinct method, each prefixing a different
hardcoded domain.

**Rejected:** four methods that differ only in their domain tag
collapses to one method (`HashDomain`) with the tag as an
argument. The four-method shape is appropriate inside an
application that has fixed protocol roles; it is wrong for a
foundation library shared across applications.

### E. Stdlib `hash.Hash` directly

Skip the seam entirely; consumers take a `func() hash.Hash`
constructor.

**Rejected:** `hash.Hash.Sum` allocates per call (same
escape-analysis issue we resolved internally), `hash.Hash`
surfaces no algorithm identifier, and the stdlib interface
predates the audit-chain `Combine` and the
fixed-output-shape `Digest`. Wrapping it gives us a contract
shaped for our use cases at the cost of one indirection.

## Drawbacks

- The fixed-max `Digest` wastes 32 bytes when SHA-256 is in
  use and 16 bytes when SHA-384 is in use. For an audit log of
  10^9 entries that is ~32 GB of in-memory padding. Acceptable
  in a foundation; codecs serialise to the active size.
- `MaxDigestSize = 64` caps SHAKE-style extendable outputs at
  64 bytes. Use cases that need longer outputs (PQC parameter
  expansion beyond the parameter sets ML-DSA / SLH-DSA need)
  will require a separate XOF seam, not handled here.
- The `Algorithm` open-string type accepts any value; typos in
  consumer code that constructs an `Algorithm` literal won't
  compile-fail. Documented; consumers cite the constants.
- `hash.Hash.Write`'s "never returns an error" stdlib contract
  is propagated by every `Stream.Write` implementation —
  consumers that wrap a non-conforming `hash.Hash` will see
  errors silently swallowed.

## Open questions

- **HMAC seam.** Most audit / regulatory protocols require a
  keyed-hash MAC alongside plain hashing. HMAC is a
  one-line wrapper over `Hasher`, but its key-rotation,
  key-state, and AAD semantics warrant a separate interface.
  Defer to a future RFC.
- **XOF (extendable output) seam.** SHAKE128 / SHAKE256 fit
  poorly into `Hasher` (variable output length). When a
  consumer needs them — likely SLH-DSA parameter expansion or
  KDF construction — add a sibling `XOF` interface in the same
  package family.

## Unresolved / future work

- A `cryptotest` conformance suite for third-party `Hasher`
  implementations (NIST CAVP vector replay, Stream-equals-Hash
  invariant, alloc contract).
- HMAC sub-package (`crypto/hmac`) when a consumer needs keyed
  integrity.
- Signing seam (`crypto/sign`) when Ed25519 / ECDSA / ML-DSA
  consumers land.
- AEAD seam (`crypto/aead`) when AES-256-GCM /
  ChaCha20-Poly1305 consumers land.
