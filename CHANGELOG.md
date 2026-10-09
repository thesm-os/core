# Changelog

## 0.7.0

### Minor Changes

- 1338d20: Encode `errs.Class` as text, so JSON and `log/slog` write its name instead of its number.
- 1338d20: Add `aesgcm.NewRandomNonce` for AES-GCM in FIPS 140-only mode.
- 1338d20: Speed up `arena.Arena.Alloc` by clearing only the bytes that a rewind or a failed `AppendVia` left written.
- 1338d20: Make `arena.Arena.Reset` zero every written byte, so `AppendVia` never exposes the bytes of a previous user.
- 1338d20: Add `arena.List` for append-only sequences of typed values in chunks that never move.
- 1338d20: Add `arena.Slabs` for byte slices that a caller frees one at a time in any order.
- 1338d20: Run the batches of `batch.Loader.LoadAll` through `task.Each`, so a failed batch cancels the others.
- 1338d20: Add `blob.AppendBytes` to read an object into a buffer of the caller under a read limit.
- 1338d20: Read an 8 KiB object with `blob.GetBytes` from `blob/memory` in 2 allocations instead of 13.
- 1338d20: Keep the objects of `blob/memory.Store` in a `btree.Map`, so `List` reads a page in O(log n + p).
- 1338d20: Add package `blob` for named-object storage with a memory store and a conformance suite.
- 1338d20: Add `blob.RangeReader` and `blob.AsRangeReader` to read a byte range of one version of an object.
- 1338d20: Add `blob.ValidContentType` and `blob.MaxContentTypeLen` for content types that every store accepts.
- 1338d20: **Breaking:** Let a reader of `blob.Store.Get` fail with `version.ErrMismatch` once its version is replaced or deleted.
- 1338d20: **Breaking:** Make `blob.Store.Put` refuse an invalid content type with `errs.Invalid` before it writes.
- 1338d20: **Breaking:** Require Go 1.27.2.
- 1338d20: **Breaking:** Return the time at which destruction becomes irreversible from `crypto.Destroyer.Destroy`.
- 1338d20: **Breaking:** Replace `crypto.Hasher.Combine` with `CombineTagged`, and treat the zero `Digest` as invalid.
- 1338d20: **Breaking:** Encode `sign.Signature` in gob through its kanon codec, which needs an addressable value.
- 1338d20: **Breaking:** Encode `tag.Tag` in gob through its kanon codec, which needs an addressable value.
- 1338d20: **Breaking:** Make `fsm.Builder.Build` reject a state that has no path to a declared terminal state.
- 1338d20: **Breaking:** Pass a `clock.Clock` to `localkey.New` for the time that `Keeper.Destroy` returns.
- 1338d20: **Breaking:** Decode kanon fields of `epoch.Epoch`, `fixed.Fixed64` and `errs.Class` only as integers.
- 1338d20: **Breaking:** Make `page.MapCursor[K, V]` an alias of `page.SliceCursor[page.Entry[K, V]]`.
- 1338d20: **Breaking:** Make `pool.NewBufferPool` pool `*pool.Buffer`, whose `Reset` zeroes the bytes of the previous user.
- 1338d20: **Breaking:** Forbid instruments to panic, and make `telemetry.Counter.Add` discard a negative value.
- 1338d20: **Breaking:** Add `Release` to `telemetry.Counter`, `Gauge` and `Histogram` to end a bound instrument.
- 1338d20: **Breaking:** Decode only the canonical kanon encoding of `sign.Signature`.
- 1338d20: Add package `btree` for ordered maps and sets as in-memory B+ trees.
- 1338d20: Add package `cache` for a cost-bounded map with S3-FIFO eviction, expiry and pinning.
- 1338d20: Find the optional capabilities of `cas`, `crypto` and `sign` values through decorators that implement `Unwrap`.
- 1338d20: Add package `cas` for content-addressed storage with a memory store and a conformance suite.
- 1338d20: Add `checkpoint.Cosigner` to sign a cosignature at a time that the caller sets.
- 1338d20: Add `crypto.AppendSealChunk` and `AppendOpenChunk` to seal and open a message one chunk at a time.
- 1338d20: Add `clock.UTCSource` and `clock/kernel.Source` to read UTC with a bound on its error.
- 1338d20: Generate codecs with kanon at e296e64, and check in CI that they are current and keep every field number.
- 1338d20: Report the failures of the concurrent `Put` races of `coretest/castest` on the test goroutine.
- 1338d20: Add `coretest/epochtest` to test the fenced writes of a consumer.
- 1338d20: Add a time-stamp authority for tests in `coretest/tsptest`.
- 1338d20: Add `coretest/versiontest` with fixtures that expose an ordering assumption over `version.Version`.
- 1338d20: Add `crypto.AADKeeper` for custodians that bind associated data to a wrapped key.
- 1338d20: Implement `kanon.Exact` on `crypto.Digest`, `id.ID` and `clock.Instant`.
- 1338d20: Speed up `crypto.Digest.IsZero` from 5.1 ns to 0.3 ns by comparing the size alone.
- 1338d20: Add `SizeKanon` to `crypto.Digest`, `id.ID` and `clock.Instant`, so kanon writes their fields in place.
- 1338d20: Make `UnmarshalBinary` of `crypto.Digest` and `id.ID` decode into the receiver without a copy.
- 1338d20: Add `crypto.GenerateKey` to create a data key in the clear and wrapped.
- 1338d20: Add `crypto/kek` to wrap data keys in process memory under a key-encryption key.
- 1338d20: Add `crypto.KeyCreator` to rotate wrapping keys without an operator.
- 1338d20: Add `crypto.Role` and tagged hashing with `Hasher.HashTagged` and `Hasher.CombineTagged`.
- 1338d20: Accept a nil `rand.Rand` in `crypto.Seal` and `AppendSeal` for an AEAD without a nonce.
- 1338d20: Add `crypto/sign/mldsa` for ML-DSA-44, ML-DSA-65 and ML-DSA-87 signatures per FIPS 204.
- 1338d20: Add package `crypto/tsp` to build RFC 3161 time-stamp requests and verify their tokens offline.
- 1338d20: Check the idempotence of `Destroy` and its error for an unknown key in `cryptotest.AssertDestroyerContract`.
- 1338d20: Cut `ecdsap384.NewVerifierFromPKIX` and `ecdsap384.Resolve` from 50 allocations to 22.
- 1338d20: Copy the public key into an `ed25519.Verifier`, so the caller may reuse its buffer.
- 1338d20: Document that `epoch.Admissible` and `epoch.Watermark` require one holder per epoch.
- 1338d20: Encode `epoch.Epoch`, `fixed.Fixed64` and `errs.Class` as integers in kanon records.
- 1338d20: Add `epoch.EventCount` to wait until a monotonic `Epoch` is at least a target.
- 1338d20: Add epoch fencing with `epoch.Admissible`, `epoch.Watermark` and `epoch.ErrFenced`.
- 1338d20: Classify a joined error as the highest-ranked class of its branches in `errs.Classify`.
- 1338d20: Classify `fs.ErrPermission`, `fs.ErrExist`, `fs.ErrInvalid` and `fs.ErrClosed` in `errs.Classify`.
- 1338d20: Add `errs.RetryAfter` and `errs.WithRetryAfter` for the retry delay that a server sets.
- 1338d20: Add package `fsm` for validated finite state machines over `uint8` states and events.
- 1338d20: Add `httpclient.Client.AppendFetchBody` to send a pooled buffer as a request body.
- 1338d20: Implement `kanon.Appender` on `id.ID` and `clock.Instant` without an error result.
- 1338d20: Add `AppendBinary`, `MarshalBinary` and `UnmarshalBinary` to `id.ID`.
- 1338d20: Speed up `id.ID.IsZero` from 3.25 ns to 0.36 ns by comparing the size alone.
- 1338d20: Add `id/uuidv7` for RFC 9562 version-7 UUIDs that sort by creation time.
- 1338d20: Cut `mldsa.NewVerifier` and the entries of `mldsa.Resolver` from four allocations to three.
- 1338d20: Add `mldsa.Verifier.Context`, and refuse ML-DSA-44 cosignature keys with a non-empty context in `tlog/checkpoint`.
- 1338d20: Add package `net/httpclient` to call one HTTP dependency with limits and guards.
- 1338d20: Add package `net/httpserver` to serve HTTP with limits, a drain, panic recovery and telemetry.
- 1338d20: Add package `note` for C2SP signed notes.
- 1338d20: Add `note.TextOf` to read the text of a signed note without an allocation.
- 1338d20: Add options to `blobtest.AssertStore` and `castest.AssertStore` for durable and zero-allocation stores.
- 1338d20: Add `pool.Buffer`, whose `Reset` zeroes the capacity of its `bytes.Buffer`.
- 1338d20: Classify the argument errors of `pool`, `clock`, `id`, `task`, `batch` and the signers as `errs.Invalid`.
- 1338d20: Run each circuit of `resilience.Breaker` as an `fsm.Machine`.
- 1338d20: Make `resilience.Do` honour a retry delay of at most `RetryConfig.MaxRetryAfter`.
- 1338d20: Add `resilience.Failover` to call redundant targets one after another until one succeeds.
- 1338d20: Add the token bucket `resilience.Limiter` over `clock.Clock`.
- 1338d20: Add `sign.AllOf`, `sign.AtLeast` and `sign.NewPolicyTree` for nested signature policies.
- 1338d20: Add `sign.AppendSigner` and `sign.AppendSign` to append a signature to a buffer of the caller.
- 1338d20: Add `sign.ContextSigner` and `sign.SignContext` to bound a signature with a context.
- 1338d20: Cut `sign.NewPolicyTree` from 29 allocations to 4 for the tlog-policy example.
- 1338d20: Add `sign.Policy.SatisfiedBy` to test a policy with a bool instead of an allocated error.
- 1338d20: Add `sign.Resolver` and `sign.Policy` for k-of-n signature policies.
- 1338d20: Add `sign.Rule.AsParty` to mark the parties of a policy at any depth.
- 1338d20: Add `sign.Rules` and `sign.Policy.Reset` to rebuild a policy without an allocation.
- 1338d20: Add `sign.Signature.Complete` to detect a signature that no verifier can check.
- 1338d20: Generate a kanon codec for `sign.Signature`.
- 1338d20: Generate a canonical kanon codec for `tag.Tag`.
- 1338d20: Add `tlog.TaggedTree` and the functions that build, prove and verify tagged Merkle trees.
- 1338d20: Add `task.Every` for periodic work and `task.Quorum` for k-of-n calls.
- 1338d20: Add package `task` for structured concurrent work.
- 1338d20: Add `telemetry.GaugeAggregation` to declare how the attribute sets of a gauge combine.
- 1338d20: Add `telemetry.HeaderCarrier` and `telemetry.WithRemoteParent` to continue a trace from HTTP headers.
- 1338d20: Add `telemetry.ShardedCounter`, `BoundedHistogram` and `RateLimitHandler` for hot paths.
- 1338d20: Add `telemetrytest.ReleaseAllocsWithin` to bound the allocations of `Release`.
- 1338d20: Count required rules instead of parties in the `sign.ErrThreshold` error of `Policy.Check`.
- 1338d20: Assert with `go.dokimi.dev/assert` instead of testkit in the hand-written tests.
- 1338d20: Classify the sentinels of `crypto`, `kek`, `resilience` and `epoch` under `errs.Classify`.
- 1338d20: Add package `tlog/checkpoint` for the C2SP checkpoint, cosignature and policy formats.
- 1338d20: Add `tlog.TaggedFold` to compute a tagged root from leaves that arrive one at a time.
- 1338d20: Add `tlog.TaggedRangeProof` and `tlog.TaggedRangeRoot` to prove a range of consecutive leaves.
- 1338d20: Add package `tlog` for RFC 9162 Merkle trees stored as C2SP tlog-tiles.
- 1338d20: Add `tlog.TileVerifier` to check leaves in index order with one read of each tile.
- 1338d20: Add package `tlog/witness` with a client and a server of C2SP tlog-witness.
- 1338d20: Add `version.ErrOutcomeUnknown` for a write that may or may not have taken effect.
- 1338d20: Document `version.Version` as equality-only.
- 1338d20: Add `witness.AppendRequest`, `ParseRequest` and `Client.AppendCosignatures` for protocols that extend add-checkpoint.
- 1338d20: Send the body of `witness.Client.AddCheckpoint` from pooled memory instead of a copy per call.

### Patch Changes

- 1338d20: Report the FIPS 140-only refusal of `aesgcm.New` as `errs.Unsupported` instead of `crypto.ErrKeySize`.
- 1338d20: Apply the S3-FIFO hit rules of a `cache.Cache` eviction to pinned and expired entries.
- 1338d20: Grow `dst` once in `arena.RebaseSlicesTo`, so every entry refers to the returned slice.
- 1338d20: **Breaking:** Fix the timestamp bits of `ulid.Format` and `ulid.ParseULID` to match the ULID specification.
- 1338d20: Stop `btree.Map`, `MapFunc` and `Set` from keeping a deleted key as a separator.
- 1338d20: Convert a `rand/crypto.Rand` to `rand.Rand` without an allocation.
- 1338d20: Classify `version.ErrMismatch`, `version.ErrExists` and `epoch.ErrFenced` as Conflict in `errs.Classify`.
- 1338d20: Classify the sentinels of `fixed` as `errs.Invalid`.
- 1338d20: Close the request body on every path of `httpclient.Client.Do`, `Fetch` and `AppendFetch`.
- 1338d20: Classify a body read that ends at the `httpclient` timeout as `errs.Transient`.
- 1338d20: Make `localkey.New` work in FIPS 140-only mode.
- 1338d20: Fix the overflow of `rand.Shuffle` for an n of `math.MinInt`.
- 1338d20: Stop `resilience.Call` from recording an outcome for a call whose context ended.
- 1338d20: Release the half-open probe of `resilience.Call` when its function panics.
- 1338d20: Allocate the slice of `telemetry.HeaderCarrier.Keys` and `MapCarrier.Keys` once.
- 1338d20: Link `Arena.CapExceeds` in the `arena` documentation.
- 1338d20: Remove references to renamed and removed symbols from the package documentation.
- 1338d20: Limit each tile read of `tlog.BlobTiles` to the largest size of the tile.
- 1338d20: Reduce the allocations of `tlog` tile paths and of `tlog.BlobTiles` reads.
- 1338d20: Stop `tsp.Verifier` from keeping a reference to the token, so a caller can reuse its buffer.
- 1338d20: Return the empty string from `uuidv4.Format` for an ID that is not 128 bits.

## [0.6.1] - 2026-08-05

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

- Minimum Go version raised from 1.26.2 to 1.26.5. Go 1.26.2's
  standard library carries GO-2026-4980, an escaper bypass in
  `html/template` that is reachable through this module's test
  infrastructure; a foundation library must not instruct its
  dependents to build on a toolchain with a known escaper bypass.
- `clock/fake.Clock.AwaitWaiters` now panics after a two-second
  watchdog instead of spinning until something outside it
  intervenes. Awaiting goroutines that never register is a
  programmer error, and the panic names the count expected and the
  count reached; previously the failure surfaced as the whole test
  binary hitting its `go test` deadline, attributed to whichever
  test happened to be running.
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

[0.6.1]: https://github.com/thesm-os/core/releases/tag/v0.6.1
[0.5.0]: https://github.com/thesm-os/core/releases/tag/v0.5.0
