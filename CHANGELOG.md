# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.5.0] - 2026-05-06

### Added

- Repository scaffolding: build tooling, CI, governance docs, and the
  ADR/RFC documentation system.
- `clock` package: the `Clock` interface (HLC-shaped, with `Now`,
  `Time`, `NewTimer`, `Update`), `Timer` interface, `Instant` value
  type with `Compare` / `HappensBefore` / `Sub` / `Add` / `Time` /
  `IsZero` methods, `NodeID` and `InstantRange` value types, plus
  `Sleep` and `After` package-level helpers.
- `clock/hlc` package: production Hybrid Logical Clock implementation
  backed by `time.Now`. `New(node)` and `NewWithSource(node, source)`
  constructors; canonical HLC merge in `Update` per Kulkarni et al.
- `clock/fake` package: deterministic virtual-time `Clock` for tests
  with manual `Advance` / `Set` and goroutine-synchronisation
  primitive `AwaitWaiters`.
- `rand` package: the `Rand` interface (`Uint64` + `Read`), `Seed`
  and `SeedUnspecified`, plus `Float64`, `Shuffle`, and `Uint64N`
  package-level helpers (Lemire's algorithm).
- `rand/pcg` package: deterministic non-cryptographic generator over
  `math/rand/v2`'s PCG.
- `rand/crypto` package: CSPRNG-grade implementation over
  `crypto/rand.Reader`, with `NewWithReader(io.Reader)` for HSM
  backends, audit wrappers, and fault-injection in tests.
- `rand/seeded` package: deterministic CSPRNG-quality generator
  built on HMAC-SHA-256 over a 64-bit counter.
- `rand/fixed` package: constant-output generator for fault-injection
  tests, with `FromFloat64` for probability-threshold fixtures.
- `docs/rfc/0001-clock-seam.md` documenting the clock contract
  rationale.
- `docs/rfc/0002-rand-seam.md` documenting the randomness contract
  rationale.
- `TestZeroAlloc` enforcement of every documented zero-allocation
  method in the clock and rand packages.
- `crypto` package: the `Hasher` interface (`ID`, `Algorithm`,
  `Hash`, `Combine`, `NewStream`), the unified `Digest` value type
  covering 256/384/512-bit outputs in one comparable shape, the
  `Stream` interface for inputs that don't fit in memory, the
  `Algorithm` open-string vocabulary type with constants for
  SHA-2 and SHA-3 families, plus `HashDomain` and `HashReader`
  helpers. Combine panics on size mismatch — programmer-error
  precondition, not a runtime condition.
- `crypto/sha256`, `crypto/sha512`, `crypto/sha3` packages:
  SHA-256, SHA-384, SHA-512, SHA3-256, SHA3-384, SHA3-512
  implementations. Hash, Combine, and the Stream hot path are
  zero-allocation; NewStream allocates the underlying hash state
  once. NIST FIPS 180-4 / 202 vectors and per-algorithm
  benchmarks from 8 B to 64 KiB.
- `docs/rfc/0003-crypto-seam.md` documenting the cryptographic
  hash contract rationale.
- `telemetry` package: the `Reporter` interface, `Counter`,
  `Gauge`, `Histogram`, and `Tracer` instrument interfaces with
  attribute pre-binding via `.With([]Attr)`, the kind-tagged
  `Attr` and `Value` types with constructors for primitives,
  the `SlogAttr` bridge to stdlib `log/slog`, the `Span` /
  `SpanContext` / `SpanKind` types, plus `WithSpanKind` and
  `ApplySpanOptions`.
- `telemetry/noop` package: a `Reporter` that discards every
  signal — empty-struct receivers, zero-allocation by inspection.
- `docs/rfc/0004-telemetry-seam.md` documenting the telemetry
  contract rationale.
- `epoch` package: `Epoch` strictly-monotonic 64-bit value type
  with `Compare`, `Successor`, `IsZero`, `String` methods, plus
  thread-safe `Counter` for in-process advancement.
- `tag` package: `Tag` key/value value type and `Tags` slice
  with `Find`, `Has`, `Get`, `With`, `Without` helpers —
  snapshot-immutable replacement for `map[string]string` across
  async-buffered, cached, and cross-goroutine boundaries.
- `version` package: `Version` opaque CAS token with
  `Unspecified` and `Wildcard` constants, `WriteOptions` with
  `IfMatch` / `IfNoneMatch` preconditions, and generic
  `Versioned[T]` wrapper for state-bearing reads.
- `page` package: `Page` pagination request with `IsFirst` and
  `WithDefault(n int)` helpers, `Cursor[T]` response interface
  with range-over-func iteration, the `SliceCursor[T]` generic
  concrete helper for tests and in-memory adapters, and the
  `Entry[K, V]` / `MapCursor[K, V]` pair for adapters that page
  over key-value stores.
- `id` package: fixed-max-size `ID` value type covering 128-,
  160-, and 256-bit identifier shapes in one kind-tagged
  comparable type with `Size`, `Bytes`, `IsZero`, `Equal`,
  `Compare`, `String` methods and `New128` / `New160` /
  `New256` constructors, plus the `Generator` interface
  (`Generate() ID`). Four generator subpackages:
  `id/ulid` (128-bit Crockford-base32 ULID, 48-bit ms
  timestamp + 80 random bits, depends on `clock.Clock` +
  `rand.Rand`); `id/uuidv4` (128-bit RFC 4122 UUID v4,
  depends on `rand.Rand`); `id/ksuid` (160-bit K-sortable UID,
  32-bit Unix-second timestamp + 128 random bits, base62
  encoded — selected by gov / defense / fintech / health
  consumers); `id/fixed` (constant for fixtures). Every
  subpackage ships `Format` and `Parse` for canonical
  serialization with sentinel errors (`ErrInvalidLength`,
  `ErrInvalidChar`, plus algorithm-specific overflow / format
  errors).
- `docs/rfc/0005-epoch.md`, `0006-tag.md`, `0007-version.md`,
  `0008-page.md`, `0009-id.md` documenting each base-type
  contract rationale.
- `pool` package: typed `sync.Pool` wrappers with `Pool[T any]`
  for arbitrary values and `ResetPool[T Resettable]` that
  auto-Resets on `Put` (preventing cross-tenant data leaks
  at the type level). Plus `NewBufferPool()` convenience
  for the `*bytes.Buffer` case.
- `arena` package: bump allocator for hot-path
  variable-length output. `Append` / `Alloc` return
  three-index-capped sub-slices into a contiguous backing
  buffer; epoch-tagged `Marker` + `SliceSince` capture
  multi-call regions across lifecycle boundaries safely.
  `CopyOut` / `CopyOutTo` / `RebaseSlices` /
  `RebaseSlicesTo` consolidate sub-slices into caller-owned
  memory at the ownership boundary. `(*Arena).Reset`
  satisfies `pool.Resettable` for one-line pool integration;
  `CapExceeds` / `Shrink` let a pool wrapper release
  oversized arenas after anomalous load.
- `docs/rfc/0010-pool.md` documenting the pool seam
  rationale.
- `docs/rfc/0011-arena.md` documenting the arena seam
  rationale.

[Unreleased]: https://github.com/thesmos-ai/core/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/thesmos-ai/core/releases/tag/v0.5.0
