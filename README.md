# core

[![CI](https://github.com/thesmos-ai/core/actions/workflows/ci.yml/badge.svg)](https://github.com/thesmos-ai/core/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/thesmos-ai/core)](https://github.com/thesmos-ai/core/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/go.thesmos.sh/core.svg)](https://pkg.go.dev/go.thesmos.sh/core)
[![Go Report Card](https://goreportcard.com/badge/go.thesmos.sh/core)](https://goreportcard.com/report/go.thesmos.sh/core)
[![Go Version](https://img.shields.io/github/go-mod/go-version/thesmos-ai/core)](go.mod)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![codecov](https://codecov.io/gh/thesmos-ai/core/graph/badge.svg)](https://codecov.io/gh/thesmos-ai/core)
[![Mutation](https://img.shields.io/badge/mutation-100%25%20effective-brightgreen.svg)](README.md)

Foundational interfaces for the [thesmos][thesmos] ecosystem.

`core` is a [stdlib-only][adr-0006] Go module that defines the contract
seams every other thesmos library and framework depends on:

- **Clock** — abstracts `time.Now`, `time.Sleep`, and timers so
  libraries remain deterministic under simulation and test. Returns
  Hybrid Logical Clock instants for distributed callers and a stdlib
  `time.Time` projection for the common case. Implementations:
  `clock/hlc` (production HLC), `clock/fake` (virtual time).
  See [RFC-0001][rfc-0001].
- **Rand** — unified randomness seam exposing both `Uint64` and
  `Read([]byte)`. Implementations: `rand/pcg` (non-crypto PCG),
  `rand/crypto` (CSPRNG over `crypto/rand`), `rand/seeded`
  (HMAC-SHA-256 deterministic CSPRNG), `rand/fixed` (constant for
  tests). See [RFC-0002][rfc-0002].
- **Crypto** — cryptographic-hash seam producing comparable
  fixed-shape digests covering 256/384/512-bit outputs in one
  type, with a stable per-implementation `ID` and long-term
  `Algorithm` identifier so receipts and audit chains survive
  algorithm rotation. `Hash(data)`, `Combine(left, right)`, and
  `Stream` (for inputs that don't fit in memory) cover leaf
  commitments, Merkle / chain construction, and large-payload
  hashing. Implementations: `crypto/sha256`, `crypto/sha512`
  (SHA-384, SHA-512), `crypto/sha3` (SHA3-256, SHA3-384,
  SHA3-512). See [RFC-0003][rfc-0003].
- **HMAC** — keyed-authentication peer of the hash seam.
  `crypto.MAC` mirrors `crypto.Hasher`'s shape (same `Digest`
  output, same `ID` + `Algorithm` model, same `Stream`) with
  first-class constant-time `Verify` and a `Digest.ConstantTimeEqual`
  helper for streaming verification. Implementations:
  `crypto/hmac/sha256`, `crypto/hmac/sha512` (HMAC-SHA-384,
  HMAC-SHA-512), `crypto/hmac/sha3` (HMAC-SHA3-{256,384,512}).
  See [RFC-0012][rfc-0012].
- **Sign** — asymmetric-signing seam. `crypto/sign.Signer` /
  `Verifier` split (verifier-only consumers don't construct a
  signer), `KeyID` value type with canonical per-algorithm
  derivation, optional `StreamingSigner` / `StreamingVerifier`
  capability interfaces for hash-then-sign algorithms.
  Implementations: `crypto/sign/ed25519` (Ed25519 PureEdDSA per
  RFC 8032 §5.1.6), `crypto/sign/ecdsap384` (ECDSA P-384 +
  SHA-384 per FIPS 186-5, ASN.1 DER signatures, also satisfies
  the streaming interfaces). See [RFC-0013][rfc-0013].
- **Telemetry** — metric and trace seams for hot-path
  observability emission, with attribute pre-binding via
  `.With([]Attr)` keeping the emit path zero-allocation while
  preserving `context.Context` for OTel exemplar correlation,
  baggage, and trace-stitching. Kind-tagged `Attr` bridges to
  stdlib `log/slog`. Implementations: `telemetry/noop`.
  See [RFC-0004][rfc-0004].
- **Epoch** — in-process strictly-monotonic 64-bit counter for
  leader generations, schema versions, optimistic-concurrency
  tokens. `epoch.Epoch` value type plus thread-safe
  `epoch.Counter`. See [RFC-0005][rfc-0005].
- **Tag** — snapshot-immutable string key/value pairs used in
  place of `map[string]string` on value-type structs that cross
  async-buffered, cached, or cross-goroutine boundaries.
  See [RFC-0006][rfc-0006].
- **Version** — opaque CAS token (`Version`), `WriteOptions`
  with IfMatch / IfNoneMatch preconditions, and `Versioned[T]`
  for read-your-writes optimistic-concurrency loops.
  See [RFC-0007][rfc-0007].
- **Page** — pagination request (`Page` with `WithDefault`
  helper) and response (`Cursor[T]`) shape with
  `SliceCursor[T]` and `MapCursor[K, V]` generic helpers.
  Range-over-func iteration makes "forgot to check err"
  syntactically impossible. See [RFC-0008][rfc-0008].
- **ID** — fixed-max-size identifier value type (`id.ID`)
  covering 128-, 160-, and 256-bit shapes in one comparable
  type, with four generator subpackages: `id/ulid`
  (128-bit time-sortable Crockford base32), `id/uuidv4`
  (128-bit random RFC 4122), `id/ksuid` (160-bit K-sortable
  base62 — alphanumeric encoding and 128-bit entropy floor
  for gov / defense / fintech / health consumers), `id/fixed`
  (constant for fixtures). Every subpackage ships `Format`
  and `Parse` for canonical serialization.
  See [RFC-0009][rfc-0009].
- **Pool** — typed `sync.Pool` wrappers: `Pool[T any]` for
  arbitrary values, `ResetPool[T Resettable]` that
  auto-clears state on `Put` (preventing cross-tenant data
  leaks at the type level), and `NewBufferPool` for the
  `*bytes.Buffer` case. See [RFC-0010][rfc-0010].
- **Arena** — bump allocator for hot-path variable-length
  output. `Append` / `Alloc` return three-index-capped
  sub-slices into a contiguous backing buffer; epoch-tagged
  `Marker` + `SliceSince` capture multi-call regions
  safely. Pool integration via `Reset` (satisfies
  `pool.Resettable`) keeps the backing buffer warm across
  requests. See [RFC-0011][rfc-0011].
- **Errs** — error-classification seam: a closed eight-value
  taxonomy of what a caller should *do* about a failure, not
  what went wrong. `Classify` walks an error tree
  zero-allocation and recognises stdlib sentinels, so a
  producer that has never heard of the package still classifies
  usefully; `Retryable` is the shorthand a retry loop or a
  circuit breaker asks for. See [RFC-0015][rfc-0015].
- **Resilience** — the algorithms every caller of a remote
  dependency needs: `Breaker` (per-target circuit, single-probe
  half-open, injectable failure judgement for transports where
  failure is not an error), `Bulkhead` (concurrency limit with
  optional queue, rejection / timeout / cancellation kept
  distinct), and `Retrier` (attempt count *and* a sliding-window
  budget, full-jitter `Backoff`). All read time through
  `clock.Clock`, so their transitions are exact under a virtual
  clock. See [RFC-0023][rfc-0023].
- **Batch** — request coalescing: `Loader[K, V]` accumulates
  concurrent single-key loads into one batched call and
  deduplicates concurrent loads of the same key. Not a cache —
  results are not retained past the in-flight window.
  See [RFC-0024][rfc-0024].

These interfaces — and the others added over time — share three
properties:

1. **Stdlib-only.** Production code imports nothing outside the Go
   standard library and the module itself; the dependency guard fails
   CI on anything else. Test code may draw on a closed allow-list that
   only an ADR can extend. ([ADR-0006][adr-0006])
2. **Single module.** One `go.mod`. Submodules are not needed because
   there are no heavy deps to isolate. ([ADR-0002][adr-0002])
3. **Apache 2.0.** Unencumbered for production and downstream
   redistribution. ([ADR-0003][adr-0003])

## Status

Pre-1.0. The primitive set is chosen for coherence of the layer model
rather than per-item demand, and lands incrementally. Breaking changes
are possible until `v1.0.0`; once tagged, the standard Go module
versioning rules apply. ([ADR-0005][adr-0005])

## Install

```bash
go get go.thesmos.sh/core
```

Module path: `go.thesmos.sh/core` · Repo: `github.com/thesmos-ai/core`

## Documentation

- **[ADRs][adr]** — accepted architectural decisions
- **[RFCs][rfc]** — proposals under discussion or accepted as direction
- **[Contributing][contrib]** — local setup, conventions, PR flow
- **[Security][sec]** — vulnerability disclosure policy

## License

Apache 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).

[thesmos]: https://thesmos.sh
[adr]: docs/adr/
[rfc]: docs/rfc/
[adr-0002]: docs/adr/0002-single-module-layout.md
[adr-0003]: docs/adr/0003-apache-2-0-with-spdx-headers.md
[adr-0005]: docs/adr/0005-primitive-set-chosen-for-coherence.md
[adr-0006]: docs/adr/0006-stdlib-only-scope-test-dependencies.md
[rfc-0001]: docs/rfc/0001-clock-seam.md
[rfc-0002]: docs/rfc/0002-rand-seam.md
[rfc-0003]: docs/rfc/0003-crypto-seam.md
[rfc-0004]: docs/rfc/0004-telemetry-seam.md
[rfc-0005]: docs/rfc/0005-epoch.md
[rfc-0006]: docs/rfc/0006-tag.md
[rfc-0007]: docs/rfc/0007-version.md
[rfc-0008]: docs/rfc/0008-page.md
[rfc-0009]: docs/rfc/0009-id.md
[rfc-0010]: docs/rfc/0010-pool.md
[rfc-0011]: docs/rfc/0011-arena.md
[rfc-0012]: docs/rfc/0012-crypto-hmac-seam.md
[rfc-0013]: docs/rfc/0013-crypto-sign-seam.md
[rfc-0015]: docs/rfc/0015-error-classification.md
[rfc-0023]: docs/rfc/0023-resilience-primitives.md
[rfc-0024]: docs/rfc/0024-request-coalescing.md
[contrib]: CONTRIBUTING.md
[sec]: SECURITY.md
