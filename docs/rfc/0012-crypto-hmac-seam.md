---
rfc: 0012
title: Cryptographic HMAC Seam
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-05-06
updated: 2026-05-06
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0012: Cryptographic HMAC Seam

## Summary

`crypto.MAC` — the keyed-authentication peer of `crypto.Hasher`.
Same `Digest` value type, same `ID` + `Algorithm` identifier
model, same `Stream` shape, with first-class constant-time
`Verify`. Implementations land per stdlib hash family in
`crypto/hmac/sha256`, `crypto/hmac/sha512`, and
`crypto/hmac/sha3`.

## Motivation

Webhook signatures, API request signing, audit-trail integrity,
session/cookie signing, and PRF/KDF construction (which
`rand/seeded` already builds inline) all need keyed
authentication. The contract surface they share is identical to
`crypto.Hasher` plus a key — same digest sizes, same streaming
ergonomics, same identifier vocabulary — except for the one
property that makes MACs distinct: verification must run in
time independent of where the bytes first differ. Promoting one
shape into core means every consumer composes the same way and
the timing-oracle hazard is closed at the type level.

The auxiliary motivation is internal: `rand/seeded` already
builds HMAC-SHA-256 inline using `crypto/hmac` + `crypto/sha256`
from the stdlib. Once `crypto/hmac/sha256` exists, that package
becomes a real internal consumer of the new seam, validating the
contract against actual usage from day one.

## Detailed design

```go
// In package crypto.
type MAC interface {
    ID() ID
    Algorithm() Algorithm
    Size() int
    Sign(data []byte) Digest
    Verify(data, expected []byte) bool
    NewStream() Stream
}

// In package crypto, on the existing Digest value type.
func (Digest) ConstantTimeEqual(other Digest) bool

// New Algorithm constants.
const (
    AlgHMACSHA256    Algorithm = "hmac-sha-256"
    AlgHMACSHA384    Algorithm = "hmac-sha-384"
    AlgHMACSHA512    Algorithm = "hmac-sha-512"
    AlgHMACSHA3_256  Algorithm = "hmac-sha3-256"
    AlgHMACSHA3_384  Algorithm = "hmac-sha3-384"
    AlgHMACSHA3_512  Algorithm = "hmac-sha3-512"
)
```

### Mirroring Hasher

`MAC.ID` / `MAC.Algorithm` / `MAC.NewStream` carry exactly the
same semantics as their `Hasher` counterparts. Outputs are
`crypto.Digest` values — identical fixed-max-size shape covering
256/384/512-bit results — so MAC outputs slot into the same
storage, comparison, and serialisation machinery already in use
across the ecosystem.

### Sign vs Verify

`MAC.Sign(data) Digest` is the one-shot keyed-MAC primitive.
`MAC.Verify(data, expected []byte) bool` is the eponymous
verification primitive: the comparison runs in constant time
over the active byte prefix via `crypto/subtle.ConstantTimeCompare`,
with a length check short-circuiting to false for size mismatch.

Length is public information determined by `MAC.Algorithm`, so
the early return is not a timing hazard. The active bytes — the
MAC under comparison — are where attacker-controlled input meets
the secret-derived value; that comparison is the one that must
be constant-time.

`Verify` accepts `[]byte` (not `Digest`) for the expected value
so consumers reading an HTTP signature header can decode hex
once into a byte slice and pass it directly, without a
`Digest`-construction round-trip.

### Size on the interface

`MAC.Size()` returns the output size in bytes — one of
`DigestSize256`, `DigestSize384`, `DigestSize512`. Hot-path
consumers preallocate fixed-size buffers (signature header,
fixed-width DB column, network frame) without consulting an
algorithm-table dispatch. `Hasher` doesn't expose `Size`
because hash output sizes are reachable from `Algorithm` via the
existing constants, but the MAC consumer pattern (sign once →
write to a sized slot) made the indirection expensive enough
to surface the method.

### Streaming verification

The `Stream` interface is shared with `Hasher`. A MAC stream
absorbs bytes via `io.Writer`, finalises with `Sum() Digest`,
then the consumer compares the resulting digest to the expected
value with `Digest.ConstantTimeEqual` — the new method on
`Digest` that runs `subtle.ConstantTimeCompare` over the active
byte prefix.

`Digest.Equal` (and `==` on `Digest`) stays as-is for hash
comparisons, where timing leakage is harmless because hashes are
content-addressed (no key, nothing for an oracle to extract).
Doc on `Equal` explicitly steers MAC and signature comparisons
to `ConstantTimeEqual`. Doc on `ConstantTimeEqual` explains when
each is appropriate. The safer method is one autocomplete away.

### Allocation contract

`Hasher.Hash` is zero-allocation because `crypto/sha256.Sum256`
is a concrete function returning a stack-local `[32]byte`. There
is no analogous one-shot HMAC primitive in the stdlib —
`crypto/hmac.New` returns a heap-allocated `hash.Hash` and is
the only entry point. Two consequences:

- `MAC.Sign` and `MAC.Verify` each allocate the underlying HMAC
  state once per call. Documented prominently. The fact pattern
  is one allocation shifted from "per `NewStream`" (Hasher) to
  "per call" (MAC).
- `MAC.NewStream` allocates once; `Stream.Write`, `Stream.Sum`,
  `Stream.Reset` are zero-allocation thereafter. Hot-path
  consumers (per-request webhook verifiers) construct one stream
  per goroutine and reuse it across `Reset` cycles — exactly the
  pattern Hasher consumers use today.

`MAC.ID`, `MAC.Algorithm`, `MAC.Size` are zero-allocation on
every implementation in the module.

### Defensive key copy

`New(key []byte)` copies the supplied keying material at
construction. The caller may zero, reuse, or mutate the source
buffer immediately — common with credential plumbing where the
key buffer is borrowed from a config decoder or KMS response and
expected to be wiped at the call site. The defensive copy is
paid once at construction; the pooled HMAC state in
`NewStream` references the copy for its lifetime.

### Key-length policy

HMAC accepts any key length per RFC 2104 — long keys are
pre-hashed, short keys are zero-padded. This package matches
that contract: any byte length is accepted at construction.
Cryptographic guidance recommends ≥ output-size keying material
of high entropy (32 bytes for HMAC-SHA-256). Enforcing a
minimum is a higher-layer policy concern; key-management
plumbing rejects short keys with errors appropriate to its
layer (config validation, KMS fetch, etc.). The seam itself
does not reject short keys.

### Algorithm-name spellings

Following the existing convention in `crypto/algorithm.go`:

- SHA-2 family names keep the size hyphen on the wire
  (`hmac-sha-256`, `hmac-sha-384`, `hmac-sha-512`).
- SHA-3 family names do not (`hmac-sha3-256`, `hmac-sha3-384`,
  `hmac-sha3-512`).

These match RFC 4231 and the IETF / NIST registry conventions.
Identifier names follow Go's no-underscore rule for SHA-2
(`AlgHMACSHA256`) and use one underscore to disambiguate the
size suffix in SHA-3 names (`AlgHMACSHA3_256`) — exactly
mirroring `AlgSHA256` vs `AlgSHA3_256`.

### Per-family package layout

```
crypto/
  mac.go              — MAC interface
  digest.go           — + ConstantTimeEqual method
  algorithm.go        — + 6 HMAC algorithm constants
  hmac/
    sha256/           — New(key) → *MAC
    sha512/           — NewSHA384(key), NewSHA512(key)
    sha3/             — NewSHA3_256(key), NewSHA3_384(key), NewSHA3_512(key)
```

Per-family packages mirror the existing `crypto/sha256`,
`crypto/sha512`, `crypto/sha3` layout. Constructors return
exported pointer types (`*MAC`, `*MAC384`, `*MAC512`,
`*MAC256`, `*MAC384`, `*MAC512`) so consumers can keep the
concrete type if they wish; the compile-time interface check
`var _ crypto.MAC = (*MAC)(nil)` guarantees the contract.

### `rand/seeded` refactor

The existing `rand/seeded` package builds HMAC-SHA-256 inline
over a 64-bit big-endian counter. Refactored to construct the
keyed stream once via `crypto/hmac/sha256.New(key).NewStream()`
and drive `Reset` / `Write` / `Sum` per refill. The byte-level
construction (HMAC-SHA-256 over a 64-bit big-endian counter
with the seed-derived key) is unchanged and still part of the
public contract, so the existing frozen test fixture
(`seeded.New(0xabcd).Read(64)` golden bytes) passes unchanged.
Zero-allocation contract preserved — the underlying mechanism
is the same `hash.Hash` reuse pattern, now reached through the
seam.

## Alternatives considered

### A. `sync.Pool` of pre-keyed `hash.Hash` for zero-alloc Sign

Discussed and rejected. `Sign` would Get → Reset → Write → Sum
→ Put against a pool of HMAC states. Steady-state zero-alloc,
but adds machinery (pool churn, defensive-copy semantics for
keys held by pool entries) that the existing `Hasher` precedent
does not justify. The right shape is to match `Hasher`'s
allocation contract verbatim — `Sign` allocates per call,
`NewStream` is the reuse path — and let consumers who want
hot-path zero-alloc do exactly what hot-path `Hasher` consumers
do: hold a `Stream`.

### B. Separate `Signer` and `Verifier` interfaces

Splitting `MAC` into `Signer` (Sign, NewStream) and `Verifier`
(Verify, NewStream) makes sense when the keys differ — public
verification key vs private signing key, as in Ed25519 or
ECDSA. HMAC is symmetric: same key signs and verifies; one type
is the natural fit. Defer the split until `crypto/sign` lands
and the asymmetric case forces the question.

### C. `Verify(data []byte, expected Digest) bool`

Taking `Digest` instead of `[]byte` for `expected`. Stronger
typing, but consumers usually have raw signature bytes off the
wire and would round-trip through a `Digest`-construction API
that doesn't exist today. `[]byte` matches the actual call-site
shape. Rejected.

### D. Returning `error` instead of `bool` from `Verify`

Lets callers distinguish "size mismatch" from "MAC mismatch".
Both mean "reject"; the distinction is rarely actionable. The
one place the distinction matters is logging, and that's
solvable by checking the size at the boundary before calling
Verify. `bool` is the right shape; rejected.

### E. Single `crypto/hmac` package taking a hash factory

`crypto/hmac.New(crypto.AlgSHA256, key)` with internal dispatch
to the right family. Fewer packages. Rejected because it loses
per-family `Algorithm()` reporting and would need to maintain a
parallel registry of "which algorithm uses which factory" —
duplicated state. The per-family package layout is a one-time
fixed cost that mirrors what the SHA-2 / SHA-3 hash packages
already do, with no ongoing maintenance.

### F. Panic on short keys

Matches `Hasher.Combine`'s "panic on programmer error"
precedent. Rejected because keys are typically runtime-sourced
(config, KMS, env) — short keys are a runtime input, not a
programmer error. Also conflicts with the no-panic-in-production
policy. Document the recommendation in package doc; defer
enforcement to higher layers.

### G. Constant-time `Digest.Equal`

Make `Digest.Equal` itself constant-time, to remove the
foot-gun. Rejected because hashes (the dominant consumer of
`Digest.Equal` today) don't need it, and Go's `==` on `Digest`
is already not constant-time — banning the method without
banning `==` doesn't close the hole. The right shape is two
named methods that document their use, with the safer one
discoverable.

## Drawbacks

- Six new `Algorithm` constants in `crypto/algorithm.go` widen
  the public surface. Each carries a paragraph of doc justifying
  its existence; the `Algorithm` type is open-string anyway, so
  this is presentation, not contract widening.
- One allocation per `Sign` / `Verify` call is suboptimal vs
  `Hasher.Hash`'s zero-alloc one-shot. The stdlib doesn't expose
  a fix; consumers who care use `NewStream`. Documented.
- `Digest.ConstantTimeEqual` and `Digest.Equal` look similar
  enough that a reader might pick the wrong one. Mitigated by
  cross-doc that explicitly steers each use case to the right
  method.

## Open questions

None.

## Unresolved / future work

- A `crypto/sign` companion seam for asymmetric (Ed25519,
  ECDSA, ML-DSA) signatures. When that lands, revisit whether
  `MAC` should re-shape as `Signer + Verifier` sub-interfaces;
  the asymmetric case is the one that forces the split.
- HKDF and constant-output-length KDF helpers built on top of
  `MAC`. Defer until a consumer needs them.
- AEAD seam. Independent of `MAC`; lands when its consumers
  emerge.
