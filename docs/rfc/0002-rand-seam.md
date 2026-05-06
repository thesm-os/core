---
rfc: 0002
title: Randomness Seam
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-05-06
updated: 2026-05-06
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0002: Randomness Seam

## Summary

A unified randomness seam — `rand.Rand` — exposing both fixed-width
`Uint64()` and variable-length `Read([]byte) (int, error)` so that
PRNG-shaped and CSPRNG-shaped consumers share a single contract.
Cryptographic strength is a property of the implementation, not the
interface; four implementations ship — `rand/pcg` (non-crypto),
`rand/crypto` (CSPRNG), `rand/seeded` (deterministic CSPRNG), and
`rand/fixed` (constant for tests). Package-level helpers `Float64`,
`Shuffle`, and `Uint64N` derive from `Uint64`.

## Motivation

Library code that calls `crypto/rand.Reader` or `math/rand/v2`
directly cannot be tested deterministically: simulations cannot
replay a trace, fault-injection tests cannot exercise specific
probabilistic branches, and audit replays cannot reproduce the
exact byte stream that triggered a failure. Routing randomness
through an injectable interface — the same way time is routed
through a clock seam — is the remedy.

Two distinct usage shapes exist in practice:

- **Fixed-width draws** for sampling, shuffling, and probabilistic
  branching: `Uint64() uint64`. Fast, no allocation, no error.
- **Variable-length byte streams** for key material, nonces, and
  cryptographic reads: `Read([]byte) (int, error)`. Matches
  `io.Reader`; surfaces source errors.

These shapes can be derived from each other (a PRNG fills bytes
via successive Uint64 emits; a CSPRNG decodes 8 bytes for a
Uint64) but the native shape of the underlying primitive determines
which derivation is wasteful. Exposing both as first-class
interface methods lets each implementation provide its native
shape efficiently.

Cryptographic strength is not an interface property. A type system
cannot distinguish a CSPRNG-grade `Rand` from a non-crypto one —
the security of `Uint64`'s output is a function of the source, not
the method signature. The interface is uniform; implementations
document their security properties and consumers pick accordingly.

## Detailed design

```go
type Rand interface {
    Uint64() uint64
    Read(p []byte) (n int, err error)
}

type Seed int64
const SeedUnspecified Seed = 0

func Float64(r Rand) float64
func Shuffle(r Rand, n int, swap func(i, j int))
func Uint64N(r Rand, n uint64) uint64    // Lemire's algorithm
```

`Seed` is a typed integer for deterministic-RNG seeding. It is not
on the `Rand` interface — only deterministic implementations carry
one — but lives in the package so implementations share a
vocabulary.

The four implementations cover the use cases without overlap:

- **`rand/pcg`**: PCG generator from `math/rand/v2`. Deterministic
  from a seed, fast, statistically excellent. Not cryptographic.
  Not safe for concurrent use — one instance per goroutine.
- **`rand/crypto`**: CSPRNG over an `io.Reader` source, defaulting
  to `crypto/rand.Reader`. The `NewWithReader` constructor lets
  callers inject HSM backends, audit-logging wrappers, or
  fault-injecting readers in tests. `Uint64` panics on source
  failure, because returning a sentinel from a CSPRNG is a silent
  footgun — predictable "random" output is the worst possible
  failure mode for a security primitive. `Read` returns the
  wrapped error.
- **`rand/seeded`**: HMAC-SHA-256(seed, counter) emitting 32-byte
  blocks. Deterministic, CSPRNG-quality output, stdlib only.
  Useful when a test or simulation needs cross-version
  reproducibility (PCG output is implementation-defined and may
  drift; HMAC-SHA-256 is fixed).
- **`rand/fixed`**: returns a constant Uint64 on every call.
  Useful in tests that assert against a specific probabilistic
  branch — pick the constant that drives the branch under test.

Allocation contract: `Uint64` is zero-alloc on every
implementation. `Read` is zero-alloc per call on every
implementation (any scratch lives on the receiver or the stack).

## Alternatives considered

### A. Separate `PRNG` and `CSPRNG` interfaces

Forces consumers to know which shape they need at the type level.

**Rejected:** the type system cannot enforce the actual security
property — a struct typed `CSPRNG` could be backed by `math/rand`.
The split adds friction without enforcing what it claims.
Per-implementation documentation is the honest layer.

### B. `io.Reader`-only interface

Drop `Uint64`; everything goes through `Read`.

**Rejected:** PRNG consumers (Lemire's `Uint64N`, sampling, fault
injection) make hot-path Uint64 draws. Routing each through a
byte-slice + decode adds a `binary.LittleEndian.Uint64` call and
an `io.ReadFull` allocation discipline at every site. The cost
isn't theoretical — the helpers in this package call `Uint64`
in a tight loop.

### C. `math/rand/v2.Source`-style interface (`Uint64` only)

Drop `Read`; everything goes through `Uint64`.

**Rejected:** CSPRNG consumers want byte slices. Filling a
variable-length slice via successive `Uint64` calls forces an
intermediate buffer and discards entropy on non-aligned tails.
`Read` is the native shape for `crypto/rand`-backed sources.

## Drawbacks

- The single interface mixes crypto and non-crypto shapes — a
  consumer that holds a `rand.Rand` cannot tell from the type
  whether it is safe for cryptographic use. Documentation
  discipline is the only mitigation; reviewers must check the
  implementation type at security-sensitive call sites.
- `pcg.Read` does Uint64 chunks rather than emitting a native byte
  stream; on a fully-aligned read this is identical to a native
  byte source, but a non-aligned tail wastes the unused bytes of
  the final Uint64.
- `crypto.Rand.Uint64` panics on source failure. The behaviour is
  intentional but does mean a consumer cannot recover from a
  degraded entropy source via `Uint64`; consumers that need
  graceful degradation must use `Read` and handle the error.

## Open questions

- **Byte-seed accessor on `seeded.Rand`.** `seeded.NewFromBytes`
  reports `SeedUnspecified` because there is no single int64 that
  recovers a byte seed. A `SeedBytes() []byte` accessor on the
  concrete type would let consumers recover reproducibility from
  the instance alone. Defer until a consumer needs it.

## Unresolved / future work

- A `randtest` conformance suite for third-party `rand.Rand`
  implementations (uniformity, Float64 distribution, Shuffle
  permutation invariants).
- Streaming-byte iterator (`iter.Seq[byte]`-shape) for consumers
  that prefer pull semantics over `Read`.
