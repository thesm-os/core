# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.6.1] - 2026-08-04

### Added

- `fixed` package: `fixed.Fixed64`, exact-scale decimal
  arithmetic at eight places stored as one `int64`. Checked
  `Add` / `Sub` / `Mul` / `Div` with 128-bit intermediates,
  away-from-zero variants, `Round` / `RoundAway` quantisation to
  a chosen place count, and a symmetric domain (`Min` is
  `-math.MaxInt64`) that makes `Neg` and `Abs` total. Text form
  renders all eight places and round-trips exactly; binary form
  is 8 bytes big-endian two's complement and is a stable wire
  contract. No `FromFloat` or `Float`, deliberately.
  See RFC-0025.
- `crypto.MAC` interface — keyed-authentication peer of
  `crypto.Hasher` with `ID`, `Algorithm`, `Size`, `Sign`,
  `Verify`, `NewStream`. Verify runs in constant time over the
  active byte prefix; size mismatch short-circuits to false.
- `Digest.ConstantTimeEqual` — constant-time comparison for
  comparing locally-computed MAC / signature digests against
  values supplied by untrusted parties. `Digest.Equal` stays as
  the fast path for hash comparisons; cross-doc steers MAC and
  signature use cases to the new method.
- `crypto/hmac/sha256` package: HMAC-SHA-256 implementation,
  RFC 4231 §4.2 / §4.3 / §4.4 / §4.6 / §4.7 vectors, fuzz +
  cross-stdlib equivalence, benchmarks at 8 B / 64 B / 256 B /
  4 KiB / 64 KiB.
- `crypto/hmac/sha512` package: HMAC-SHA-384 and HMAC-SHA-512
  implementations, RFC 4231 vectors for both, fuzz +
  cross-stdlib equivalence, benchmarks.
- `crypto/hmac/sha3` package: HMAC-SHA3-256, HMAC-SHA3-384,
  HMAC-SHA3-512 implementations. NIST CAVP-equivalent vectors
  computed from the stdlib and frozen to detect regressions,
  fuzz + cross-stdlib equivalence, benchmarks.
- `crypto.AlgHMACSHA256`, `AlgHMACSHA384`, `AlgHMACSHA512`,
  `AlgHMACSHA3_256`, `AlgHMACSHA3_384`, `AlgHMACSHA3_512`
  Algorithm constants — RFC 4231 / IETF / NIST registry
  spellings (`hmac-sha-256`, `hmac-sha3-256`).
- `docs/rfc/0012-crypto-hmac-seam.md` documenting the HMAC seam
  rationale.
- `crypto/sign` package: `Signer` / `Verifier` interface split
  (every Signer is-a Verifier; verifier-only consumers
  construct a Verifier from raw public-key bytes without
  holding a private key), `KeyID` 16-byte value type with
  per-algorithm canonical derivation, optional
  `StreamingSigner` / `StreamingVerifier` capability
  interfaces for hash-then-sign algorithms.
- `crypto/sign/ed25519` package: Ed25519 PureEdDSA per RFC
  8032 §5.1.6, backed by `crypto/ed25519`. Signer / Verifier;
  no streaming (the algorithm cannot stream — RFC 8032 §5.1.6
  needs the message in two SHA-512 computations). Zero-alloc
  Verify. `KeyIDFromPub` derives SHA-256[pub](:16);
  `TestKeyIDStability` locks the encoding via a hardcoded
  vector.
- `crypto/sign/ecdsap384` package: ECDSA over NIST P-384 with
  SHA-384 hashing per FIPS 186-5, ASN.1 DER signatures, backed
  by `crypto/ecdsa`. Implements both Signer + StreamingSigner
  and Verifier + StreamingVerifier. `KeyIDFromPub` derives
  SHA-256[SEC 1 uncompressed point](:16); `TestKeyIDStability`
  locks the encoding (X=1, Y=2 vector).
- `crypto.AlgEd25519`, `crypto.AlgECDSAP384` Algorithm
  constants.
- `docs/rfc/0013-crypto-sign-seam.md` documenting the signing
  seam — including the load-bearing decision to split Signer
  and Verifier (deferred from RFC-0012), the rationale for not
  shipping `SignTo` (stdlib constraint) and not shipping
  streaming on Ed25519 (PureEdDSA cannot stream), EU
  compliance posture, and what's deferred to future rounds (PQ
  signatures, threshold, KEM).

- `crypto.Stream.Close()` method on the [crypto.Stream]
  interface. One-shot consumers ([crypto.HashDomain],
  [crypto.HashReader]) Close after Sum to release the stream
  back to its pool; long-lived consumers (per-message hot paths
  reusing via [crypto.Stream.Reset]) ignore Close. Adding a
  method to a public interface is a breaking change for
  external implementations; none exist outside this module.
- `Makefile` targets: `bench-baseline` regenerates
  `.bench/baseline.txt`; `bench-compare` runs benches into
  `.bench/current.txt` and `benchstat`s against the baseline
  (advisory regression gate).
- Bench coverage:
  - `crypto/sign/ed25519` — `Generate`, `KeyIDFromPub`,
    `SignParallel`, `VerifyParallel`.
  - `crypto/sign/ecdsap384` — `Generate`, `KeyIDFromPub`.
  - `crypto/hmac/sha512` — `Verify`, `Stream`, `SignParallel`
    in (algorithm × size × mode) sub-bench shape.
  - `crypto/hmac/sha3` — full `Sign` / `Verify` / `Stream` /
    `SignParallel` matrix for sha3-256 / sha3-384 / sha3-512.
  - `id.BenchmarkEqual` and `BenchmarkCompare` extended to
    Size128 / Size160 / Size256 (covers ULID / UUIDv4 / KSUID).
  - `pool.BenchmarkPool` split into sequential + parallel
    sub-benches (typed `Pool[*resettable]`).
- RFC 8032 §7.1 known-answer test vectors for Ed25519 (TEST 1,
  2, 3) — locks in interoperability with the published RFC.

- `errs` package: the error-classification seam. `Class` — a
  closed eight-value enumeration of what a caller should *do*
  about a failure, orthogonal to what went wrong. `Classify`
  walks an error tree and returns the first `Classifier` it
  finds, falling back to two recognised stdlib sentinels
  (`fs.ErrNotExist`, `errors.ErrUnsupported`) so a producer that
  has never heard of the package still classifies usefully.
  `Retryable` is the shorthand. `WithClass` wraps. `Classify`
  and `Retryable` are zero-allocation.
  See `docs/rfc/0015-error-classification.md`.
- `crypto.Domain`, `crypto.Framer`, `crypto.NewFramer` —
  unambiguous domain separation. `Framer` length-prefixes every
  part it appends (`Fixed`, `Bytes`, `String`, `Uint64`,
  `Uint32`), so no two distinct inputs can encode to the same
  bytes. See `docs/rfc/0016-framed-domain-separation.md`.
- `crypto.AEAD` interface — authenticated encryption. Embeds
  stdlib `cipher.AEAD` and adds the `ID` + `Algorithm` identity
  model, so a ciphertext at rest records what produced it.
  `crypto.Seal` / `crypto.Open` carry the nonce with the
  ciphertext. See `docs/rfc/0017-authenticated-encryption.md`.
- `crypto/aesgcm` package: AES-128-GCM and AES-256-GCM per
  NIST SP 800-38D, backed by `crypto/aes` + `crypto/cipher`.
- `crypto.Keeper` interface — key custody: wrap and unwrap data
  keys without exposing the root key. Optional `Destroyer` and
  `KeyGenerator` capability interfaces.
  See `docs/rfc/0018-key-custody.md`.
- `crypto/localkey` package: in-process `Keeper` for development
  and tests.
- `crypto.XOF` / `crypto.XOFStream` interfaces — extendable
  output for key derivation and deterministic padding, where a
  fixed-size `Digest` cannot serve.
  See `docs/rfc/0019-extendable-output-functions.md`.
- `crypto/shake` package: SHAKE128 and SHAKE256 per FIPS 202,
  with NIST vectors. The wrapper converts the stdlib's
  write-after-read panic into `crypto.ErrXOFSqueezing`.
- `crypto.AlgAES128GCM`, `AlgAES256GCM`, `AlgChaCha20Poly1305`,
  `AlgXChaCha20Poly1305`, `AlgSHAKE128`, `AlgSHAKE256`
  Algorithm constants.
- `crypto.DigestFromBytes`, `Digest.AppendBinary`,
  `Digest.MarshalBinary`, `Digest.UnmarshalBinary` and
  `clock.InstantSize`, `Instant.AppendBinary`,
  `Instant.MarshalBinary`, `Instant.UnmarshalBinary`,
  `Instant.UnixMilli`, `Instant.UnixMicro`, plus `id.FromBytes`
  — binary encoding for the core value types, satisfying
  `encoding.BinaryAppender` / `BinaryMarshaler` /
  `BinaryUnmarshaler`.
  See `docs/rfc/0014-binary-encoding-for-core-value-types.md`.
- `telemetry.Propagator`, `telemetry.Carrier`,
  `telemetry.MapCarrier` — carrying a `SpanContext` across a
  process boundary. `SpanContext` gains `Sampled` and
  `TraceState`.
  See `docs/rfc/0020-trace-context-propagation.md`.
- `telemetry/w3c` package: W3C Trace Context `traceparent` /
  `tracestate` propagator.
- `pool.Bounded[T]` and `pool.ErrLimit` — fixed-capacity peer of
  `Pool[T]` for objects that are scarce rather than merely
  reusable (a connection, a decoder, a hardware handle), where
  exhaustion must be reported rather than allocated around.
  See `docs/rfc/0021-bounded-pool.md`.
- `arena.AppendVia` and `arena.TruncateTo` — writing into arena
  space through a caller-supplied function, and unwinding to a
  `Marker` when it fails.
- `clock.Wait(ctx, c, d)` — the cancellable counterpart to
  `Sleep` and the safe counterpart to `After`; stops its timer
  on every exit path.
- `resilience` package: `Breaker` (per-target circuit,
  consecutive-failure threshold, single-probe half-open, with
  `Allow` / `Record` for transports where failure is not an
  error and `Call` where it is), `Bulkhead` (concurrency limit
  with optional queue and clock-bounded wait, keeping
  `ErrFull` / `ErrWaitTimeout` / `ctx.Err()` distinct), and
  `Retrier` (`Do` bounded by an attempt count *and* a
  sliding-window budget with a `MinRetries` floor, plus the
  free `Backoff` function — full jitter, zero-allocation).
  All read time through `clock.Clock`.
  See `docs/rfc/0023-resilience-primitives.md`.
- `batch` package: `Loader[K, V]` coalesces concurrent
  single-key loads into one batched call and deduplicates
  concurrent loads of the same key. `Load`, `LoadAll`
  (immediate dispatch, split at `MaxBatch`), `Pending` (the
  coalescing ratio) and `Close`. Not a cache: results are not
  retained past the in-flight window.
  See `docs/rfc/0024-request-coalescing.md`.
- `version.ErrMismatch` and `version.ErrExists` — the sentinels
  the `WriteOptions` preconditions had always described in
  prose but never supplied.
- `docs/adr/0005-primitive-set-chosen-for-coherence.md`,
  `0006-stdlib-only-scope-test-dependencies.md` (supersedes
  ADR-0001), `0007-zero-digest-is-valid-chain-genesis.md`,
  `0008-core-defines-contracts-that-describe-io.md`,
  `0009-logging-is-log-slog.md`.
- `docs/rfc/0022-keyed-storage.md`, recorded as Withdrawn: a
  cache miss is normal, so modelling absence as `ErrNotFound` is
  backwards, and `database/sql` is already the database seam.

### Changed

- `id/fixed` renamed to `id/constant`, and `rand/fixed` to
  `rand/constant`. **Breaking**: both import paths change. The
  name `fixed` now denotes fixed-point decimals, and a package
  name in `core` may repeat only when the repeats denote the
  same concept. Both packages are named for their behaviour —
  which is what their own doc comments already called them.
  See ADR-0010 and ADR-0011.
- Hasher streams (`crypto/sha256`, `crypto/sha512`,
  `crypto/sha3`) — pooled at package level. `NewStream` is
  zero-allocation on the warm path; [crypto.Stream.Close]
  returns the instance for reuse.
- HMAC streams (`crypto/hmac/sha256`, `crypto/hmac/sha512`,
  `crypto/hmac/sha3`) — pooled per-MAC. `NewStream` is
  zero-allocation on the warm path.
- `crypto.HashDomain` and `crypto.HashReader` are now
  zero-allocation on the warm path (previously two allocs from
  `NewStream` wrapper + hash state). Locked in by
  `TestHashDomainZeroAlloc`. The cold path (first call after
  process start, or after GC pool eviction) still pays one
  Stream allocation.
- `crypto.Digest.String()` — stack-buffer + [encoding/hex.Encode]
  - string conversion: 1 alloc (was 2 from
  `hex.EncodeToString`'s `make` + `string`).
- `rand/crypto.Rand.Uint64()` — package-level `pool.Pool[*[8]byte]`
  for the read buffer: zero-allocation on the warm path (was 1
  alloc forced by the [io.Reader] interface boundary).
  `TestZeroAlloc` extended.
- `crypto/hmac/sha256` `MAC.Sign` / `MAC.Verify` —
  zero-allocation on the warm path via a per-MAC pool of
  pre-keyed [hash.Hash] instances. `crypto/hmac/sha512` and
  `crypto/hmac/sha3` adopt the same pattern.
- `id/ksuid.Format` and `id/ksuid.Parse` — base62 long division
  via uint32 chunks: ~4× faster Format, ~2.4× faster Parse vs
  the byte-level implementation.
- `telemetry.SpanOption` — value-typed struct (was function-
  typed closure). `telemetry.ApplySpanOptions` is now
  zero-allocation.
- `arena.RebaseSlices`, `arena.CopyOut` — docstrings nudge
  sustained-throughput callers to `RebaseSlicesTo` /
  `CopyOutTo` (zero-alloc destination-buffer variants).
- `crypto.MAC`, `crypto.Hasher`, `crypto.Stream`, `sign.Signer`,
  `sign.Verifier`, `sign.StreamingSigner`,
  `sign.StreamingVerifier` — per-method godoc (was interface-
  level only).
- `crypto/sign/doc.go`, `crypto/sign/ecdsap384/doc.go`,
  `crypto/sha3/doc.go` — added "Generate API asymmetry",
  "Cost vs Ed25519: prefer BatchRoot for ECDSA P-384", and
  "Performance vs SHA-2" sections.
- `rand/seeded` now constructs its HMAC-SHA-256 stream via
  `crypto/hmac/sha256.New(key).NewStream()` rather than
  inlining `crypto/hmac` + `crypto/sha256`. Byte-level
  determinism preserved (the construction is part of the
  public contract, fixture test unchanged); zero-alloc contract
  preserved.

- `crypto.Combine` now admits the zero `Digest` as a valid chain
  genesis rather than rejecting it. A hash chain has to start
  somewhere, and refusing the zero value forced every caller to
  invent its own sentinel first block.
  See `docs/adr/0007-zero-digest-is-valid-chain-genesis.md`.
- Build gate migrated from `make` targets to `ergon`, with the
  `mod` / `lint` / `test` / `coverage` stages configured in
  `.ergon.yaml`. Line coverage is gated at 100% for every
  package except `coretest`.
- Test assertions across the module migrated to `testkit`
  primitives (`Equal`, `ErrorIs`, `True`, `Panics`, …),
  replacing inline comparisons and per-package `eq[T]` helpers.
- `docs/rfc/0009-id.md` §"Compile-time-distinct identifier
  types" now recommends embedding (`type EpochID struct{
  id.ID }`) instead of a defined type. A defined type inherits
  no methods and, because `ID`'s fields are unexported, cannot
  be constructed outside the package either.

### Fixed

- `BenchmarkHashReader` test artifact — `bytes.NewReader(data)`
  moved out of the `b.Loop` closure (one spurious alloc per
  iteration that wasn't `HashReader`'s).
- `BenchmarkSign` (ed25519, ecdsap384) — sink pattern in the
  loop body forces the per-iteration signature slice to escape,
  surfacing the true allocation cost (the prior bench
  under-reported with `_, _ =` due to compiler stack-promotion).
- `crypto.HashDomain` now length-prefixes each part with a
  big-endian `uint64` before hashing it. Without the prefix,
  `("ab", "c")` and `("a", "bc")` hashed identically — a
  domain-separation failure in the helper whose whole job is
  domain separation. The length buffer is pooled, so the helper
  stays zero-allocation.
- `id.ID` distinct-identifier guidance in `id/doc.go`: the
  defined-type pattern the docs recommended does not work, for
  the reasons above.

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

[Unreleased]: https://github.com/thesmos-ai/core/compare/v0.6.1...HEAD
[0.6.1]: https://github.com/thesmos-ai/core/releases/tag/v0.6.1
[0.5.0]: https://github.com/thesmos-ai/core/releases/tag/v0.5.0
