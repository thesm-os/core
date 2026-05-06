# core

Foundational interfaces for the [thesmos][thesmos] ecosystem.

`core` is a [stdlib-only][adr-0001] Go module that defines the contract
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

These interfaces — and the others added over time — share three
properties:

1. **Stdlib-only.** `core` has zero non-stdlib imports. The dependency
   guard fails CI on any new import outside `$gostd` and the module
   itself. ([ADR-0001][adr-0001])
2. **Single module.** One `go.mod`. Submodules are not needed because
   there are no heavy deps to isolate. ([ADR-0002][adr-0002])
3. **Apache 2.0.** Unencumbered for production and downstream
   redistribution. ([ADR-0003][adr-0003])

## Status

Pre-1.0. Interfaces are added incrementally as their shape stabilises in
consumer libraries. Breaking changes are possible until `v1.0.0`; once
tagged, the standard Go module versioning rules apply.

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
[adr-0001]: docs/adr/0001-stdlib-only-dependencies.md
[adr-0002]: docs/adr/0002-single-module-layout.md
[adr-0003]: docs/adr/0003-apache-2-0-with-spdx-headers.md
[rfc-0001]: docs/rfc/0001-clock-seam.md
[rfc-0002]: docs/rfc/0002-rand-seam.md
[rfc-0003]: docs/rfc/0003-crypto-seam.md
[rfc-0004]: docs/rfc/0004-telemetry-seam.md
[rfc-0005]: docs/rfc/0005-epoch.md
[rfc-0006]: docs/rfc/0006-tag.md
[rfc-0007]: docs/rfc/0007-version.md
[rfc-0008]: docs/rfc/0008-page.md
[rfc-0009]: docs/rfc/0009-id.md
[contrib]: CONTRIBUTING.md
[sec]: SECURITY.md
