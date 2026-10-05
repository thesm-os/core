# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `crypto.Role` and the tagged hashing pair `Hasher.HashTagged` /
  `Hasher.CombineTagged`: a one-byte domain separator before every
  leaf and every interior hash, so a caller-chosen payload of two
  digest widths can no longer hash to a legitimate interior node.
  The role's high bit is reserved for arity — unary roles are
  `0x00`–`0x7F` and binary roles `0x80`–`0xFF`, each refused by the
  other's method — which makes a leaf role and a node role unable to
  share a first byte. Core ships no roles, as it ships no domains.
  See RFC-0029 and ADR-0013.
- `blob` package: the named-object storage seam — caller-keyed,
  streamed both directions, conditional writes via the `version`
  vocabulary, and three laws practice left undefined: atomic
  visibility on `Put`, one consistent object per open reader, and
  cursor-chain completeness over a quiescent store. `blob/memory`
  is the reference implementation; `coretest/blobtest` holds every
  implementation to the laws. `PutBytes` and `GetBytes` sit beside
  the seam for values that fit in memory, so a small object does
  not cost its caller a reader at every call site.

  `ValidKey` defines the key space by `io/fs.ValidPath` plus a
  1024-byte key bound and a 255-byte element bound, so a key a
  caller writes travels between an object store and a filesystem
  rather than working on one and escaping the root on the other.
  Nesting is permitted, because the object stores this seam fronts
  permit it; a backend whose namespace cannot hold `a/b` beside
  `a/b/c` encodes around it. `Delete` takes one optional version
  rather than the whole write-options struct, so a create-only
  precondition on a removal is no longer a value a caller can pass
  and the store then reject. `Info.ModTime` is `time.Time`, which
  is what the clock package's own documentation asks for, and it
  MAY be zero on the `Info` a `Put` returns: a backend that reports
  no timestamp on write would otherwise owe a second request per
  write. See RFC-0028.
- `cas` package: the content-addressed storage seam. A `Store` is
  bound to one hashing algorithm and reports it through `Hasher`,
  so a store received through injection can be written to without
  a second parameter carrying the binding. `Put` verifies that the
  data hashes to its address, stores nothing on disagreement, and
  reports a write exactly once under concurrency. `Get` appends
  into a caller-supplied buffer, so a reader at rate allocates
  nothing. There is no `Delete` — deletion is consumer-side garbage
  collection, and erasure of meaning is crypto-shred.

  Large objects use the optional `Streamer` capability. The
  `PutStream` and `GetStream` functions take a store's native path
  when it has one and buffer when it does not, so both are correct
  against any `Store`. `cas/memory` is the reference implementation
  and implements `Streamer` natively: it hashes during the read
  rather than after it, and serves stored bytes without copying
  them. `coretest/castest` holds every implementation to the laws,
  including the streaming ones. See RFC-0027.
- `epoch` fencing (RFC-0026): `Admissible` and the `Watermark`
  adapter kit apply the admit-equal fence laws over the existing
  `Epoch` type; `ErrFenced` reports revoked authority and
  classifies as Conflict; `Epoch` gains the canonical 8-byte
  big-endian binary encoding for persisted watermarks.
- `coretest/epochtest`: conformance suite driving a consumer's
  entire fenced write surface through supersession, asserting
  rejection without mutation, admit-equal, zero-bypass, and the
  reseed law.
- `coretest/versiontest`: `OrderingTraps` fixtures that make any
  ordering assumption over opaque `version.Version` tokens
  observable in a consumer's suite.
- `task` package: concurrent work that ends before the call that
  started it returns. `All` runs a fixed set of functions, `Each`
  and `Map` call one function for every element of a slice, `Stream`
  calls one for every element of an `iter.Seq2[E, error]` such as a
  `page.Cursor`, and `Run` starts tasks one at a time through
  `Group.Go`. Every function cancels its context on the first error
  and returns that error, and records the cause for work it skipped,
  so a nil result means every task ran and returned nil. A task that
  panics crashes the process from its own goroutine, and a task that
  calls `runtime.Goexit` records `ErrExited`. `Each`, `Map` and
  `Stream` run on a fixed set of workers and do not allocate per
  element. See RFC-0030.
- `fsm` package: finite state machines over `uint8` states and events
  with `String` methods. `NewBuilder` declares edges with optional
  guards and actions, entry and exit actions, and terminal states.
  `Build` validates the declaration and reports every problem at once:
  unreachable states, states without an outgoing edge that are not
  terminal, terminal states with an edge, and edges that follow an
  unguarded edge. The resulting `Spec` is immutable and answers
  `Next`, `Allows` and `Terminal`, so a caller that stores a status
  checks a transition before its compare-and-swap. A `Machine` holds a
  state and its data and runs one event at a time through `Fire`, in
  exit, edge and entry order. `Fire` costs about 5 ns and allocates
  nothing. See RFC-0031.
- `aesgcm.NewRandomNonce`: AES-GCM whose nonces the standard library's
  FIPS 140-3 module generates. It is the only AES-GCM construction
  FIPS 140-only mode accepts. Its envelopes and those of `aesgcm.New`
  open under either constructor with the same key.
- `crypto.GenerateKey` returns a fresh data key in the clear and
  wrapped. It uses the custodian's `KeyGenerator` when there is one,
  and otherwise draws the key from a `rand.Rand` and calls `Wrap`.
- `pool.Buffer`: a `bytes.Buffer` whose `Reset` zeroes its capacity.
- `sign.ContextSigner` and `sign.SignContext`. A signer backed by a
  hosted key service or a hardware module implements `ContextSigner`,
  so a caller can bound the wait with a context. `SignContext` uses
  the capability when a signer has it. Otherwise it returns the
  context's cause when the context has ended, and calls `Sign` when it
  has not. `cryptotest.ContextSignerAssertion` checks an
  implementation. See RFC-0035.
- `crypto/sign/mldsa`: ML-DSA-44, ML-DSA-65 and ML-DSA-87 signers and
  verifiers per FIPS 204, over the standard library's `crypto/mldsa`.
  The FIPS 204 context string is fixed when a signer or verifier is
  built, and a private key is its 32-byte seed. `crypto` gains
  `AlgMLDSA44`, `AlgMLDSA65` and `AlgMLDSA87`. See RFC-0033.
- `sign.Resolver` and `sign.Policy`. A Resolver maps an algorithm name
  to a verifier constructor, from a table the caller writes, and
  refuses an unlisted name with `sign.ErrUnknownAlgorithm`. A Policy
  requires valid signatures from k of n parties, where a party is one
  or more keys that must all sign. That covers approver thresholds,
  witness quorums and hybrid signatures. `Policy.Check` counts each key
  once and verifies at most one signature per key of the policy.
  `ed25519.Resolve`, `ecdsap384.Resolve` and `mldsa.Resolver` build the
  Resolver entries. See RFC-0039.
- `task.Every` and `task.Quorum`. `Every` calls a function, waits a
  period plus a random jitter after each call returns, and calls it
  again, until its context ends or the function fails. It reads time
  through `clock.Clock`, so a test can run it against the fake clock.
  `Quorum` calls a function for every element and returns as soon as k
  calls succeed, cancelling the rest. It returns `task.ErrNoQuorum`,
  joined with the failures, as soon as k successes are impossible. See
  RFC-0037.
- Capabilities behind decorators: `cas.AsStreamer`,
  `crypto.AsDestroyer`, `crypto.AsKeyGenerator`,
  `sign.AsStreamingSigner`, `sign.AsStreamingVerifier` and
  `sign.AsContextSigner` find a capability through decorators that
  implement `Unwrap`, or `UnwrapKeeper` for a `crypto.Keeper`.
  `cas.PutStream`, `cas.GetStream`, `crypto.GenerateKey` and
  `sign.SignContext` use them, so a tracing or metrics decorator no
  longer hides the capability of the value it wraps. See RFC-0038.
- `clock.UTCSource` and `clock.UTCReading`: a reading of UTC with a
  bound on its error and whether the clock is synchronised, so a caller
  can refuse to stamp a record when the clock is outside a limit.
  `clock/kernel.Source` reads the bound from the Linux kernel through
  adjtimex(2) at most once per refresh interval, and a read costs about
  39 ns without allocating. `fake.Clock` is a `UTCSource` whose error a
  test sets with `SetUTCError`. See RFC-0036.
- `tlog`: the Merkle tree of RFC 9162 over any `crypto.Hasher`, stored
  as the tiles of C2SP tlog-tiles. `LeafHash`, `NodeHash`, `Root`,
  `InclusionProof` and `ConsistencyProof` work on a tree in memory, and
  `VerifyInclusion` and `VerifyConsistency` check a proof against a
  root. `Builder` and `Update` integrate batches of leaves into tiles,
  `BlobTiles` reads tiles from a `blob.Store`, and `TreeRoot`,
  `ProveInclusion` and `ProveConsistency` build a proof from one
  batched read. With SHA-256 the bytes match `golang.org/x/mod/sumdb/tlog`
  and RFC 6962. `Integrate` costs about 100 ns per leaf and allocates
  nothing. See RFC-0032.
- Options for `blobtest.AssertStore` and `castest.AssertStore`.
  `WithReopen` and `WithCrash` let a durable adapter prove that every
  write that returned survives a restart, that no version recurs after
  one, and that a crash leaves each object as it was or whole.
  `castest.WithZeroAllocGet` requires `Get` into a buffer with room
  not to allocate. Existing calls compile unchanged. Core's tests run
  each suite against a broken store for every case and require that
  case to fail. See RFC-0038.
- `blob.RangeReader` and `blob.AsRangeReader`. A store that implements
  `RangeReader` reads a byte range of an object without transferring
  the rest. A non-zero `ifMatch` refuses a read of any other version
  with `version.ErrMismatch`, so a caller reads two or more ranges from
  one version. The count and the error follow `bytes.Reader.ReadAt`,
  and an absent object classifies as NotFound whatever `ifMatch` names.
  `AsRangeReader` finds the capability through decorators that
  implement `Unwrap() Store`. `blob/memory` implements it without
  allocating. `blobtest.AssertStore` runs its cases for every store
  that implements it, and `blobtest.WithRangeReader` fails a store
  without it. See RFC-0040.
- Chunked messages in `crypto`. `AppendSealChunk` and `AppendOpenChunk`
  seal and open one chunk of a message at a time, so a reader of a
  range opens only the chunks it reads. A `ChunkHeader` from
  `NewChunkHeader` identifies the message with a random 16-byte ID and
  fixes its chunk size. Every chunk binds the header, its index, a
  last-chunk flag and the caller's associated data, so a reordered,
  dropped, truncated or extended chunk fails to open, and so does a
  chunk of another message under the same key. `AppendSealChunk`
  refuses a chunk that breaks the size rules of its header. `SealedSize`
  returns the length of the envelope `AppendSeal` writes, so a reader
  computes chunk offsets without opening a chunk. Both functions work
  in FIPS 140-only mode with `aesgcm.NewRandomNonce` and allocate
  nothing when `dst` has capacity. `crypto/testdata/chunk_vectors.txt`
  records the bytes. See RFC-0041.
- `crypto/kek`: a `crypto.Keeper` over an intermediate key-encryption
  key (KEK) that a parent `Keeper` wraps. A `kek.Keeper` unwraps its KEK
  once and then wraps each data key in process memory, so a caller makes
  one custodian call per KEK instead of one per data key. It derives a
  wrapping key with HKDF-SHA-256 from the KEK and a random salt, and
  derives the next one after 2^30 wraps. A wrap costs about 175 ns and
  one allocation. The parent wraps a record that binds the KEK to its
  key ID, so `kek.New` refuses a record swapped with another key ID's
  with `kek.ErrKeyIDMismatch`. `Close` zeroes the KEK, and a cleanup
  zeroes the KEK of a keeper dropped without `Close`. See RFC-0042.
- `crypto.AADKeeper` and `crypto.AsAADKeeper`: the optional capability
  of a `Keeper` whose custodian binds associated data to a wrapped key,
  as the AWS KMS encryption context does. `kek.GenerateAAD` and
  `kek.NewAAD` pass the key ID to such a parent, so the custodian's
  policy can refuse a record for another tenant. `localkey.Keeper`
  implements the capability, and `cryptotest.AssertAADKeeperContract`
  checks an implementation. See RFC-0042.
- `arena.List`: an append-only sequence of typed values in chunks. The
  first chunk doubles from 1 to 4,096 elements, and every later chunk is
  allocated whole with 4,096, so once the first chunk is full no chunk
  moves and `Append` copies no element. Building 65,536 records of 96
  bytes takes 79% less time and allocates 79% fewer bytes than appending
  to a slice. `Truncate` zeroes the elements it drops and keeps every
  chunk, so a List that is emptied and filled again allocates nothing.
  `Ptr` returns the address of an element, and `Chunks` yields a chunk at
  a time within 5% of the cost of ranging over a slice. `Append`, `At`
  and `Ptr` inline. With 65,536 records of 96 bytes, an `Append` into
  kept chunks takes 2.9 ns, an `At` 1.6 ns, a `Ptr` 0.75 ns and a read
  through `Chunks` 0.6 ns per element, none of them allocating. Reading a
  field of a 96- or 152-byte record through `At` takes 2.8 to 5.2 times
  as long as a slice index, because `At` copies the element, and `Ptr`
  narrows that to 1.4 to 1.9 times.
- Tagged trees in `tlog`. `TaggedRoot`, `TaggedInclusionProof` and
  `VerifyTaggedInclusion` build, prove and verify a tree of RFC 9162's
  shape whose interior nodes are `CombineTagged` under a binary role that
  the caller assigns. They share the split, the path and the verifier's
  shift with the RFC 9162 functions. Tests match them against the
  recorded vectors through RFC 9162's node hash. A `TaggedTree` keeps
  every node of one tree. It returns the paths of all 1,000 leaves of a
  batch in 148 µs, against 111 to 115 ms for 1,000 calls to
  `TaggedInclusionProof`, and allocates nothing when reused. A tagged
  tree over no leaves has no root, and `TaggedRoot` panics on one.
  `VerifyTaggedInclusion` checks the size of each proof hash before it
  hashes it, and returns `ErrProof` for one of the wrong size. An
  untrusted proof never makes `CombineTagged` panic. See ADR-0025 and
  ADR-0026.
- `btree` package: ordered maps and sets as in-memory B+ trees. `Map`
  orders its keys by `cmp.Compare`, `MapFunc` by a function that
  `NewMapFunc` receives, and `Set` is a `Map` without values. Each has
  point operations, `Floor` and `Ceil`, `PopMin` and `PopMax`, `At` and
  `Rank` in O(log n), iterators over the whole collection and over key
  ranges in both directions, `DeleteRange`, and `Clone` in O(1) with
  copy-on-write nodes. A write during an iteration does not end it: the
  iteration continues after the last key it yielded. A leaf has no
  pointer field, so the garbage collector marks a leaf of keys and values
  without pointers without scanning it. On 65,536 random `int` keys, a
  `Get` takes 66.8 ns, a `Delete` and a `Set` of the same key 156 ns, and
  a full iteration 1.9 ns per key, and none of them allocates. Building
  the map allocates 1,545 objects, and a `Clone` followed by one `Set`
  allocates the 3,200 bytes of the path it copies. A `Get` on a `MapFunc`
  takes 1.41 times as long as on a `Map`. `Reset` empties a map and keeps
  its nodes for the next fill: a `Reset` followed by a fill of the 65,536
  keys takes 5.09 ms and does not allocate, against 5.44 ms and 1,545
  allocations for a new map. See RFC-0043 and ADR-0027.
- `errs.RetryAfter` and `errs.WithRetryAfter`. An error reports the
  delay that a server set through a `RetryAfter() time.Duration` method,
  which `RetryAfter` finds in the error tree as `errs.Classify` finds a
  class, and `WithRetryAfter` attaches a delay to an error whose type
  the producer did not define. `errs.Retryable` still reports whether
  to retry. `RetryAfter` does not allocate. See ADR-0029.
- A text encoding for `errs.Class`: `AppendText`, `MarshalText` and
  `UnmarshalText` over the names `String` returns, and
  `errs.ErrUnknownClass` for a value outside the eight classes or text
  that names none. `encoding/json` and the JSON handler of `log/slog`
  wrote a class as its number and now write its name. The names are
  frozen. See ADR-0030.
- `version.ErrOutcomeUnknown` for a write that may or may not have taken
  effect, because the store lost its connection or the caller's
  deadline passed after the write left the process. `errs.Classify`
  recognises it as `Transient`, since a retry of the identical write
  with the same idempotency key returns the original outcome. See
  ADR-0032.
- `id.ID.AppendBinary`, `MarshalBinary` and `UnmarshalBinary`. An ID
  encodes as its bytes, with the size given by their length, the layout
  that ADR-0016 freezes, and the zero ID encodes as no bytes.
  `UnmarshalBinary` accepts empty data as the zero ID, which `FromBytes`
  rejects. Codecs that use a type's binary methods, such as
  `encoding/gob`, now encode an `ID`, whose fields are unexported.
  `AppendBinary` into a buffer with room and `UnmarshalBinary` do not
  allocate.
- `sign.AllOf`, `sign.AtLeast` and `sign.NewPolicyTree`. A `sign.Policy`
  is a tree of key sets and of thresholds over them, such as the nested
  groups of a C2SP tlog-policy, and `NewPolicy` builds the tree of one
  level. `Check` verifies at most one signature per key of the tree, and
  none when the signatures it received cannot satisfy the root. The
  children of the root are the parties that an excluded key removes. A
  tree is at most 64 rules deep, and `Check` does not allocate on
  success for up to 64 keys and 128 rules. See RFC-0044 and ADR-0034.
- `epoch.Epoch`, `fixed.Fixed64` and `errs.Class` have a `ValidateKanon`
  method that kanon generates, so a kanon record encodes each of them as
  its integer: an epoch as a varint, a `Fixed64` as a zigzag varint of
  its raw value, and a class as its number. An epoch of 7 takes 2 bytes
  of a record instead of 10, and a class 2 bytes instead of 8 to 13. A
  record with one field of each encodes in 6.4 ns instead of 14.5 ns.
  The binary and text forms of the types do not change. See RFC-0045
  and ADR-0036.
- `sign.Signature` has a codec that kanon generates. It records the
  field numbers `Algorithm` 1, `Value` 2 and `KeyID` 3, so the record of
  every consumer encodes a signature alike. The codec adds nine methods
  to `*Signature`: `SizeKanon`, `EncodeKanon`, `AppendBinary`,
  `MarshalBinary`, `UnmarshalBinary`, `DecodeKanon`, `MergeKanon`,
  `Reset` and `CloneKanon`. See RFC-0045.
- `crypto.Digest.SizeKanon`, `id.ID.SizeKanon` and
  `clock.Instant.SizeKanon` return the length of the binary form, so
  kanon writes a field of these types once, in place. A record with a
  digest, an ID and an instant encodes in 10.5 ns instead of 20.6 ns,
  with the same bytes.
- `telemetry.GaugeAggregation` and `InstrumentSpec.Aggregation`. A gauge
  declares whether the values of its attribute sets combine as a sum,
  as for a depth, or as a maximum, as for a lag, so an adapter that
  caps the attribute sets of an instrument gives the gauge's overflow
  series a defined value. In a sum gauge each bound instrument has a
  value of its own, and the value of a set is the sum of its
  instruments' values. The zero value, `GaugeAggregationUnspecified`,
  leaves the meaning of an existing gauge unchanged. See RFC-0046 and
  ADR-0038.
- `telemetrytest.ReleaseAllocsWithin`, a benchmark plug-in that fails
  when the first `Release` of a bound instrument allocates more than a
  given number of times. It binds a new instrument for every call that
  it measures.
- `epoch.EventCount`, a monotonically increasing `Epoch` that goroutines
  wait on. `Wait` blocks until the count is at least a target, and
  returns the error of `Fail` or the context's error when either comes
  first. `Wait` does not allocate, and `Advance` allocates one channel
  when a waiter is blocked. A `Wait` on a met target takes 10 ns. See
  ADR-0040.
- `crypto.KeyCreator` and `crypto.AsKeyCreator`: the optional capability
  of a custodian that creates wrapping keys and opens them by key ID, so
  a caller rotates its wrapping key without an operator.
  `crypto/localkey` implements it with a key table that all its Keepers
  share, and `Keeper.Destroy` destroys any key of that table.
  `cryptotest.AssertKeyCreatorContract` checks an implementation. See
  ADR-0041.
- `blob.ValidContentType` and `blob.MaxContentTypeLen`: a content type of
  at most 255 bytes of printable ASCII, which every store accepts. See
  ADR-0042.
- `id/uuidv7`: a generator of the version-7 UUIDs of RFC 9562, whose
  bytes sort in the order of their creation. `Generate` writes the Unix
  milliseconds, the fraction of the millisecond in 12 bits, and 62
  random bits. The IDs of one `Generator` increase in byte order, also
  when its clock repeats a time or moves backwards. With `clock/hlc` and
  `rand/crypto`, `Generate` takes 91 ns and does not allocate.
  `TimestampMillis` returns the milliseconds of an ID, `Valid` checks
  its version and variant, and `Format` and `Parse` convert the text
  form. `Parse` does not allocate.
- `crypto.Digest`, `id.ID` and `clock.Instant` declare `kanon.Exact` with
  an `ExactKanon` method. The append method of each returns no error and
  appends `SizeKanon` bytes for every value but the zero `Digest`, and its
  decode method accepts only the bytes that the append method writes for
  the decoded value. kanon's generated code then writes a field of these
  types without an error path, and a canonical decode does not encode the
  value a second time. `kanontest.RunExact` checks both guarantees.
- `tag.Tag` has a kanon codec that core generates, with the fields `Key` 1
  and `Value` 2, so the record of every consumer encodes a tag alike. Its
  decode accepts only the canonical encoding. The codec adds nine methods
  to `*Tag`: `SizeKanon`, `EncodeKanon`, `AppendBinary`, `MarshalBinary`,
  `UnmarshalBinary`, `DecodeKanon`, `MergeKanon`, `Reset` and
  `CloneKanon`. See ADR-0043.
- `id.ID` and `clock.Instant` implement `kanon.Appender` with an
  `AppendKanon` method that has no error result. kanon's generated code
  then writes an ID or an instant without an error path in every
  position, the elements of a slice, the values of a map and the target of
  a pointer included. `AppendBinary` appends the bytes of `AppendKanon`,
  and `kanontest.RunExact` checks that the two agree for every value.
- `sign.Signature.Complete` reports whether a signature has an algorithm,
  a value and a key ID, the fields that a verifier reads. A record that
  persists a signature calls it to refuse one that no verifier can check.
  It does not verify the signature, and a nil `*Signature` is not
  complete.
- `note` package: C2SP signed-note. A `Key` is a verifier key, a `Name`
  a key name and a `Type` a signature type, and `NewType` encodes a type
  that signed-note assigns no byte as 0xff, a length byte and an
  identifier. A note key is a `sign.Verifier` whose `KeyID` follows from
  its name and its key ID, so `Note.Check` converts each signature line
  to a `sign.Signature` and verifies a note against any `sign.Policy` of
  note keys, with at most one verification per key. A `Resolver`, a
  table that the caller writes, builds the `Verifier` of a key from a
  `sign.Resolver` entry through `Text` or a format of `tlog/checkpoint`.
  `Parse` accepts exactly the notes that `Note.AppendText` writes. Every
  operation has a path without an allocation through memory of the
  caller: `Key.Set` and `Key.UnmarshalText` into a `Key` of the same key,
  `Note.UnmarshalText` into a `Note` of the same keys, `Note.Sign` into a
  reused `Note`, `TextSigner.Reset` into a signer of the caller, and a
  `Keyring` that keeps the `Verifier` of each key across the reloads of
  a configuration. `Key`, `Signature` and `Note` have canonical kanon
  codecs. See RFC-0047.
- `tlog/checkpoint` package: the C2SP formats of the signed tree heads
  of a transparency log. `Body` is the text of tlog-checkpoint, with a
  root of 32, 48 or 64 bytes. `CosignatureV1` and `SubtreeV1` build the
  `note.Resolver` entries of the two timestamped messages of
  tlog-cosignature, and `CosignatureV1Signer` and `SubtreeV1Signer` sign
  them with the time of a `clock.UTCSource` within an error bound.
  `Policy` is a tlog-policy file. A `Verifier` keeps one `sign.Policy`
  per log origin, which requires one of the origin's log keys and the
  quorum. `Verifier.Verify` checks a checkpoint without an allocation,
  and `Verifier.Reset` rebuilds the trees of a reloaded policy without
  one. `Policy.UnmarshalText` of an unchanged file allocates nothing,
  `ParsePolicy` allocates six times for tlog-policy's example, and
  `NewVerifier` 18 times for one log and three witnesses. See RFC-0047.
- `sign.AppendSigner`, `sign.AppendSign` and `sign.AsAppendSigner`. A
  signer with the capability appends its signature to a buffer of the
  caller. `AppendSign` uses the capability when a signer has it, also
  behind decorators, and otherwise signs through `SignContext` and
  appends a copy. The signers of `ed25519`, `mldsa` and `ecdsap384`
  implement it, and the Ed25519 signer appends without an allocation.
  `cryptotest.AppendSignerAssertion` and
  `cryptotest.AppendSignerContextAssertion` check an implementation.
- `sign.Rules` and `sign.Policy.Reset`. `Rules` keeps the memory of the
  keys and the children of a tree of rules, and its `AllOf` and
  `AtLeast` copy into it, so a caller that builds a tree of the same
  size again allocates nothing. `Policy.Reset` builds a policy again in
  its own memory, and allocates nothing for a tree of up to 64 keys that
  fits the memory of the policy before.
- `mldsa.Verifier.Context` returns the FIPS 204 context string of a key.
  The signer of type 0x06 of `tlog/checkpoint` refuses an ML-DSA-44 key
  under any context other than the empty one.
- `arena.Slabs`: byte slices from slabs, taken back one at a time in any
  order. `Alloc` returns a slice whose capacity is a power-of-two size
  class of at least 64 bytes. `Free` zeroes the class and keeps it for
  the next `Alloc` of the class, so a store whose values come and go
  reuses the space of a removed value. A class larger than a slab takes
  a slab of its own size, and the rest of a slab without room for a
  class serves the smaller classes. `All` yields every slab, free space
  included, and `InUse` returns the bytes in use. Once the free lists
  have grown to the working set, an `Alloc` followed by a `Free` does
  not allocate. `Free` returns `arena.ErrSizeClass`, classified
  `errs.Invalid`, for a slice whose capacity is not a size class.
- `note.TextOf` returns the text of a signed note without building a
  `Note`, and checks the note as `Parse` does, with the same errors. It
  allocates nothing for a note that it accepts, whatever its key names,
  where `Note.UnmarshalText` allocates the names that change. See
  RFC-0047.
- `cache` package: a bounded map whose entries leave by eviction, expiry
  or removal. A `Cache` bounds the sum of the costs of its entries, and
  evicts with S3-FIFO, which missed fewer requests than LRU on eight of
  nine simulated traces. `Get` and `Pin` take no lock and allocate
  nothing for a string key or a key without pointers. A `Pinned` entry
  is not evicted before `Unpin`, an entry expires at a time that the
  `clock.Clock` of the `Config` measures, and `Config.Evicted` receives
  every entry that leaves, once, outside the cache's lock. See RFC-0049.
- `resilience.Limiter`: a token bucket of a rate per second and a burst,
  over `clock.Clock`. `AllowN` takes units when the bucket has them, and
  `WaitN` reserves them and waits on a timer of the clock. The bucket is
  one time in int64 nanoseconds, as in the generic cell rate algorithm,
  and each cost rounds up, so the Limiter admits at most its rate.
  `WaitN` returns `resilience.ErrUnits`, classified `errs.Invalid`, for a
  negative number of units or one above the burst. See RFC-0050.
- `resilience.Failover`: calls redundant targets one after another under
  their circuits of a `Breaker`, from a start that a caller rotates, until
  one succeeds. It skips a target whose circuit refuses the call and
  records each outcome as `Call` does. When every target fails, its error
  contains each target's error, and it classifies as `errs.Transient`
  when any target's error is Transient, with the shortest delay among
  them, so a `Do` around it retries. A success allocates nothing. See
  RFC-0052.
- `crypto/tsp` package: the Time-Stamp Protocol of RFC 3161, offline.
  `AppendRequest` encodes a request, `ParseResponse` checks a response
  against the request and returns its token, and a `Verifier` verifies a
  token against the caller's roots and policies, with RSA, ECDSA,
  Ed25519 and ML-DSA signatures. `Info.Time` is a `clock.UTCReading` of
  genTime and the token's accuracy, and `Info.Chain` is the verified
  chain of the authority's certificate, which `VerifierConfig.Check`
  receives in the `Info`. A token of a known certificate verifies without
  an allocation for Ed25519 and ML-DSA. See RFC-0048.
- `coretest/tsptest`: a time-stamp authority for tests, with a
  certificate chain of its own. `Respond` returns the response to a
  request, and `Token` builds the malformed and forged tokens that a
  verifier refuses from a `Spec`.
- `telemetry.ShardedCounter`, `telemetry.BoundedHistogram` and
  `telemetry.RateLimitHandler`. A `ShardedCounter` spreads the adds to a
  `Counter` over cells of 128 bytes and adds their sum on `Flush`. A
  `BoundedHistogram` records every value of a failed call and one value
  in N of the calls that succeeded, N recomputed from the measured rate.
  A `RateLimitHandler` is a `slog.Handler` that passes one record per
  interval of each message and subject value, and every record at
  `slog.LevelError` and above. Their hot paths allocate nothing, and
  their constructors return `telemetry.ErrConfig`, classified
  `errs.Invalid`. See RFC-0051.
- `net/httpserver` package: serves HTTP on `net/http` with a limit on
  every phase of a connection, a drain, recovery from panics,
  cross-origin protection and the telemetry of each request. `New`
  requires `WithClock`, `WithLogger`, `WithReporter` and `WithPropagator`,
  and every limit has a default, such as a header timeout of 5 s and a
  body limit of 4 MiB. A request that declares a body beyond the limit
  receives 413 before the handler runs. `Error` writes the problem
  details of RFC 9457 with the status of the error's class, and
  `Annotate` adds attributes to the log record and the span of a request.
  `Run` keeps serving for the drain delay while `Ready` responds with 503,
  and returns `httpserver.ErrShutdown` when the shutdown timeout elapses.
  The chain allocates 2 objects per request. See RFC-0053 and ADR-0045.
- `net/httpclient` package: calls one HTTP dependency on a transport of
  its own. `New` requires the same four dependencies and `WithHosts`. A
  `Client` of `ReachPublic`, the default, checks every address that it
  connects to, and returns `httpclient.ErrBlocked`, classified
  `errs.Denied`, for an address that is not public. `WithBreaker` and
  `WithRetrier` guard and retry each call through `resilience`, and retry
  only a request that is safe to send again. The default classification
  refuses a status other than 2xx with a `*httpclient.StatusError`, whose
  class follows the status and whose `RetryAfter` reads both forms of the
  header. `Fetch` returns the body within `WithMaxResponseBytes`, or
  `httpclient.ErrTooLarge`. `AppendFetch` appends the body to a buffer of
  the caller, and does not allocate the body when the buffer has room for
  it. `WithDialContext` connects a client through a dial function of the
  caller, such as one end of a `net.Pipe` in a test that counts the
  allocations of a call, and `New` refuses it for `ReachPublic`. An
  attempt allocates 2 objects of the client. See RFC-0053.
- `telemetry.HeaderCarrier`, `telemetry.WithRemoteParent` and
  `telemetry.RemoteParent`. `HeaderCarrier` is a `Carrier` over the
  headers of an HTTP message, to which an `http.Header` converts without
  a copy. It canonicalises its keys as `net/textproto` does, so `Get`
  does not allocate. `WithRemoteParent` starts a span as the child of a
  context that a `Propagator` extracted, and a tracer reads it with
  `RemoteParent`. See RFC-0053.
- `tlog/witness` package: both sides of C2SP tlog-witness. A `Client`
  sends a checkpoint and a consistency proof to `add-checkpoint`, and
  returns the cosignature lines of the witness after it verifies a line
  of every witness key. `Client.Checkpoint` reads the monitor retrieval
  route. A `Server` is a witness: `AddCheckpoint` and `Checkpoint` are
  its handlers, and `Advance` advances up to 4,096 origins under one note
  in one commit. The server commits each checkpoint to a journal in a
  `blob.Store` before it cosigns it, at the time of the commit, so
  processes that share one store never cosign two inconsistent
  checkpoints of one origin. Snapshots move the latest updates of idle
  origins into groups, retire the origins that `Logs` refuses after
  `Retention`, and delete the objects that they replace. A circuit
  of a `resilience.Breaker` per cosigner stops the commits while a
  cosigner fails. A call returns once its signatures exist. The commit
  then stores the lines while the next commit runs, and the monitor
  retrieval route serves an update once its lines are stored. Each
  cosigner signs the notes of a commit on up to GOMAXPROCS goroutines.
  A commit allocates 9 objects of the package, which its calls share,
  and its concurrent signatures 8 more for a commit of two or more
  calls. The monitor retrieval route allocates nothing for an update in
  its cache. Every error classifies under `errs`. See RFC-0054.
- `checkpoint.Cosigner`: a cosigner that signs at a time of its caller.
  `CosignatureV1Signer` and `SubtreeV1Signer` implement it. `AppendSignAt`
  signs without a reading of the UTC source, and returns
  `checkpoint.ErrTimestamp` for a time whose whole seconds since the Unix
  epoch are not positive. `CheckText` returns the error that
  `AppendSignAt` returns for a text that the format does not sign. Both
  allocate nothing for Ed25519. See RFC-0054.

### Changed

- **Breaking:** `blob.Store.Put` classifies a content type that
  `blob.ValidContentType` rejects as `errs.Invalid`, and returns before
  it touches storage. `blob/memory` implements the check, and
  `coretest/blobtest` fails a store without it. See ADR-0042.
- **Breaking:** an open reader of `blob.Store.Get` can fail with an error
  that wraps `version.ErrMismatch` once its version is replaced or
  deleted, so a store that destroys the bytes of a removed version can
  implement the seam. The reader still returns only bytes of the version
  that `Get` named. A caller of `Get`, `blob.GetBytes` or
  `tlog.BlobTiles` that races a writer reads the object again after the
  error. `coretest/blobtest` checks both rules after an overwrite and
  after a delete, and `blob/memory` returns the old version as before.
  See ADR-0044.
- **Breaking:** the kanon codec of `sign.Signature` decodes only the
  canonical encoding of a signature. `DecodeKanon`, `MergeKanon` and
  `UnmarshalBinary` return an error that wraps `kanon.ErrNotCanonical` for
  any other input that the wire format lets a decoder accept, such as
  fields in another order or a field number that core does not record. A
  consumer's canonical record can then contain a signature. The encoding
  does not change. See ADR-0043.
- **Breaking:** `encoding/gob` encodes a `tag.Tag` through its kanon
  codec, whose methods have pointer receivers. gob fails for a `Tag` in a
  value that it cannot address, such as a struct passed to `Encode` by
  value. A pointer to the struct and a `tag.Tags` slice encode.
- **Breaking:** `fsm.Builder.Build` rejects a state that has an outgoing
  edge but no path to a terminal state, when the Spec declares a
  terminal state. A cycle of states without an exit built before. A Spec
  without terminal states, such as the circuit of `resilience.Breaker`,
  builds as before. See ADR-0024.
- **Breaking:** core requires Go 1.27.0, up from 1.26.6, so its
  packages can use the Go 1.27 standard library, including
  `crypto/mldsa`.
- **Breaking:** `crypto.Destroyer.Destroy` returns the time at which
  the destruction becomes irreversible, as
  `Destroy(ctx, keyID) (time.Time, error)`. Hosted custodians schedule
  deletion days ahead and allow cancellation until then. The zero time
  means the custodian cannot yet say when. `Destroy` is idempotent and
  returns the time already set, and `crypto.ErrKeyDestroyed` also
  covers a key scheduled for destruction. See RFC-0034.
- **Breaking:** `localkey.New` takes a `clock.Clock`, which supplies
  the time `Keeper.Destroy` returns.
- `cryptotest.AssertDestroyerContract` checks that a second `Destroy`
  returns the first call's time, and that `Destroy` of an unknown key
  returns `crypto.ErrKeyID`.
- `batch.Loader.LoadAll` runs its batches through `task.Each`. A
  failed batch now cancels the other batches of the same call, whose
  results were discarded anyway, and the batch function receives a
  context derived from the caller's: it carries the caller's values
  and deadline.
- `resilience.Breaker` runs each circuit as an `fsm.Machine`, and
  every `Breaker` shares one `fsm.Spec` built when the package
  initialises. Behaviour is unchanged: the existing tests pass
  unchanged, and a differential test matched the hand-written
  version on 600,000 random operations. An `Allow` and `Record` pair
  costs 3 to 5 ns more. `Allow`, `Record` and `State` do not allocate
  for a target that has a circuit, and `TestZeroAlloc` asserts it.
- `coretest/castest` returns the errors of its concurrent-`Put`
  races to the test goroutine. A failed `Put` inside the race called
  `t.Fatalf` on its own goroutine, which the `testing` package
  forbids, and `sync.WaitGroup.Go` counted the resulting
  `runtime.Goexit` as a normal return.

- **Breaking:** `crypto.Hasher.Combine` is removed. An unprefixed
  `H(left || right)` is indistinguishable from the hash of a
  caller-chosen payload of two digest widths, which is how a
  fabricated entry verifies against a shortened authentication
  path; every correct use is now `CombineTagged`. The zero
  `Digest` goes with it — it is the uninitialised value, valid
  nowhere, and a chain's first link is a unary role over one
  operand rather than a combine with an absent one. `Hash` is
  unchanged and stays untagged for content addressing. See
  RFC-0029, ADR-0013 and ADR-0014.
- `version.Version` is equality-only by documented law: it proves
  identity, never order. Ordering across time is `epoch.Epoch`'s
  axis. See RFC-0026.
- **Breaking:** `pool.NewBufferPool` returns a `ResetPool` of
  `*pool.Buffer` instead of `*bytes.Buffer`. `bytes.Buffer.Reset`
  only truncates, so a pooled buffer handed the next user the
  previous user's bytes through `AvailableBuffer`. `pool.Buffer`
  embeds `bytes.Buffer`, so its methods are unchanged.
- `arena.Arena.Reset` zeroes every byte written since the previous
  `Reset`, including bytes a `TruncateTo` rewind or a failed
  `AppendVia` left past the length. `AppendVia` hands its appender
  the arena's spare capacity, which held the previous user's bytes.
  `Reset` now runs in time proportional to the bytes written.
- `crypto.Seal` and `crypto.AppendSeal` read nothing from their
  `rand.Rand` for an AEAD with `NonceSize` 0, so the source may be
  nil. `AppendSeal` does not allocate when its buffer has capacity.
- `epoch.Admissible` and `epoch.Watermark` document their
  precondition: the issuer grants each epoch to at most one holder.
- `crypto.Digest.IsZero` compares the size alone. The constructors
  are the only code that sets a size, so the zero value is the only
  `Digest` of size 0, and the result is unchanged for every `Digest` a
  caller can build. A call costs 0.3 ns, down from 5.1 ns for the
  comparison of the whole 65-byte value.
- `id.ID.IsZero` compares the size alone, as `crypto.Digest.IsZero`
  does. The constructors are the only code that sets a size, so the zero
  value is the only `ID` of size 0, and the result is unchanged for every
  `ID` a caller can build. A call takes 0.36 ns, down from 3.25 ns for
  the zero `ID`. For an `ID` whose first byte is not zero, the
  comparison of the whole 33-byte value took 1.26 to 1.28 ns.
- `crypto.Digest.UnmarshalBinary` and `id.ID.UnmarshalBinary` decode into
  the receiver. They assigned the value that `DigestFromBytes` or
  `FromBytes` returned, which copied the whole value twice. A decode of 32
  bytes takes 2.1 ns, down from 7.5 ns, for a digest, and 0.57 ns, down
  from 7.3 ns, for an ID. Each method clears the bytes after its input, so
  the decoded value equals, under `==`, the value that the constructor
  builds from the same bytes. The accepted inputs, the errors and the
  allocations do not change.
- `arena.Arena.Alloc` clears only the part of its region that a
  `TruncateTo` rewind or a failed `AppendVia` left written. The other
  bytes are already zero from the allocation or from `Reset`, so on a new
  or reset arena `Alloc` writes nothing and the caller's fill is the first
  write to the region. On a new arena an `Alloc` of 64 KiB takes under
  30 ns, down from 705 ns. Reserving and filling 64 KiB in a reused arena
  takes 784 ns, down from 1,042 ns. `AppendVia` documents that its
  appender writes no byte past the slice it returns, which `Reset` and
  `Alloc` rely on.
- `blob/memory.Store` keeps its objects in a `btree.Map` ordered by
  key, so `List` reads a page of p objects in O(log n + p) instead of
  sorting all n keys on every call. Reading one page of 50 from the
  middle of 65,536 objects takes 826 to 864 ns and 2 allocations of
  4,144 bytes, down from 4.1 to 4.5 ms and 3 allocations of 1,052,720
  bytes.
- `pool.ErrLimit`, `clock.ErrInstantSize`, `id.ErrSize`,
  `task.ErrLimit`, `batch.ErrConfig` and the sentinels of `id/ulid`,
  `id/ksuid`, `id/uuidv4`, `crypto/sign/ecdsap384` and
  `crypto/sign/ed25519` classify as `errs.Invalid`, like core's other
  errors for an argument the caller got wrong. They classified as
  Unspecified. `errs.Retryable` is false for both classes. A
  `resilience.Breaker` whose `TripOn` lists `errs.Unspecified` no
  longer counts these errors as dependency failures.
- **Breaking:** `page.MapCursor[K, V]` is an alias of
  `page.SliceCursor[page.Entry[K, V]]`, so the two are one type. The
  methods and their contracts are unchanged. A type switch with a case
  for each no longer compiles.
- `errs.Classify` classifies an error whose `Unwrap` returns `[]error`,
  such as one from `errors.Join`, as the class of highest rank among its
  branches: `Integrity`, `Denied`, `Invalid`, `Unsupported`, `NotFound`,
  `Conflict`, then `Transient`. It took the class of the first
  classified branch, so the class of a `task.Quorum` failure depended on
  which call failed first, and `errs.Retryable` could report true for a
  join with an `Integrity` failure in it. See ADR-0028.
- `errs.Classify` recognises `fs.ErrPermission` as `Denied`,
  `fs.ErrExist` as `Conflict`, and `fs.ErrInvalid` and `fs.ErrClosed` as
  `Invalid`, so `EACCES`, `EPERM`, `EEXIST` and `ENOTEMPTY` classify
  through `syscall.Errno`. An error that matches two recognised
  sentinels takes the class of higher rank. See ADR-0031.
- `resilience.Do` waits the longer of its backoff and the delay of a
  failure, and returns at once a failure whose delay exceeds the new
  `RetryConfig.MaxRetryAfter`. The zero value of `MaxRetryAfter` makes
  `Do` return every failure with a delay. The documentation of `Do`
  states that a transient failure does not mean `fn` had no effect. See
  ADR-0029.
- The sentinels of `crypto` classify under `errs.Classify`.
  `ErrCiphertextShort` and `ErrAlgorithmSize` classify as `Integrity`,
  `ErrEnvelopeVersion` as `Unsupported`, `ErrKeyID` as `NotFound` and
  `ErrKeyDestroyed` as `Denied`. `ErrDigestSize`, `ErrDigestZero`,
  `ErrKeySize`, `ErrAlgorithmMismatch`, `ErrXOFSqueezing`, `ErrChunkSize`
  and `ErrChunkHeader` classify as `Invalid`. `kek.ErrKeyIDMismatch`
  classifies as `Integrity`, `resilience.ErrConfig` as `Invalid`, and
  `errs.Classify` recognises `epoch.ErrSize` as `Invalid`. They
  classified as Unspecified, so a `resilience.Breaker` whose `TripOn`
  lists `errs.Unspecified` no longer counts them as dependency failures.
  Every exported sentinel of core has a class, apart from
  `task.ErrNoQuorum`, `resilience.ErrFull` and `resilience.ErrWaitTimeout`,
  whose documentation states why. See ADR-0033.
- **Breaking:** once a consumer regenerates its kanon code, its records
  encode a field of type `epoch.Epoch`, `fixed.Fixed64` or `errs.Class`
  as the integer, and records written with the binary or text form of
  the type do not decode. See RFC-0045.
- **Breaking:** `encoding/gob` encodes a `sign.Signature` through its
  kanon codec, whose methods have pointer receivers. gob fails for a
  `Signature` in a value that it cannot address, such as a struct passed
  to `Encode` by value. A pointer to the struct and a slice of
  signatures encode.
- Core's production code imports `go.thesmos.sh/kanon` and
  `go.thesmos.sh/kanon/wire` at the pseudo-version of kanon's commit
  098def3, and its tests import kanon's conformance suite and
  `go.dokimi.dev/assert`. The generated files declare version 2 of the
  generator. CI checks that the generated files are current and that a
  pull request keeps every recorded field number. See ADR-0035.
- **Breaking:** `telemetry.Counter`, `telemetry.Gauge` and
  `telemetry.Histogram` declare `Release`, which ends a bound instrument
  that `With` returned. A released instrument records nothing, and an
  adapter that keeps state per attribute set may forget a set after the
  last instrument bound to it is released. Every implementation adds the
  method. `telemetry/noop` implements it as an empty method, and the
  contract assertions of `coretest/telemetrytest` check it. See RFC-0046
  and ADR-0037.
- **Breaking:** `telemetry.Counter.Add` discards a negative value, and a
  method that emits does not panic for any argument. The contract asked
  production-grade implementations to panic for a negative increment.
  The backend of an implementation defines how `Gauge.Set`, `Gauge.Add`
  and `Histogram.Record` treat NaN, an infinity and a negative histogram
  value. The contract assertions of `coretest/telemetrytest` pass these
  values, so an adapter that panics for one of them fails. See ADR-0039.
- `sign.NewPolicyTree` checks the shape of a tree and counts its keys
  and rules before it copies the tree, and sizes its memory once: four
  allocations for the tree of tlog-policy's example, down from 29.
  `sign.NewPolicy` builds the rules of up to 16 parties on the stack.
- `ed25519.NewVerifier`, `ed25519.NewVerifierFromBytes` and
  `ed25519.Resolve` copy the 32-byte public key into the `Verifier`, so
  a caller may reuse the source buffer, and allocate once instead of
  twice.
- `mldsa.NewVerifier` and the entries of `mldsa.Resolver` allocate three
  times instead of four: a `Verifier` keeps its FIPS 204 options by
  value.
- `ecdsap384.NewVerifierFromPKIX` and `ecdsap384.Resolve` parse the PKIX
  key once and do not encode it again: 22 allocations instead of 50.

### Fixed

- `aesgcm.New` in FIPS 140-only mode returns the standard library's
  refusal of caller-supplied nonces, classified `errs.Unsupported`. It
  reported `crypto.ErrKeySize` for a valid key.
- `uuidv4.Format` returns the empty string for an ID that is not 128
  bits, as RFC-0009 specifies. It returned the text form of the first 16
  bytes of a 160- or 256-bit ID.
- Converting a `rand/crypto.Rand` to `rand.Rand` no longer allocates.
  `Rand` holds its reader behind a pointer, so the interface stores it
  directly. Every call that passed `randcrypto.New()` to a function
  taking a `rand.Rand` paid one allocation, which the `AppendSeal`
  documentation attributed to the entropy read.
- The `arena` package documentation links `Arena.CapExceeds`, and the
  `Arena` example shows that a grown buffer holds a copy of the
  earlier bytes.
- `errs.Classify` now actually recognises `version.ErrMismatch`
  and `version.ErrExists` as Conflict. Both sentinels documented
  the classification since RFC-0015, but `Classify` only ever
  recognised the two standard-library sentinels — a bare mismatch
  from an adapter classified as Unspecified. `epoch.ErrFenced`
  joins the recognised set.
- `localkey.New` succeeds in FIPS 140-only mode. The `Keeper` wraps
  with `aesgcm.NewRandomNonce`, whose nonces the standard library's
  FIPS 140-3 module generates. It returned the refusal of
  caller-supplied nonces. Material wrapped before this change still
  unwraps, because both AES-GCM constructions write the nonce at the
  same offset. `Wrap` does not read from the `rand.Rand` given to
  `New`, which supplies only the keys `GenerateKey` returns.
- `fixed.ErrOverflow`, `ErrDivZero`, `ErrRange`, `ErrSyntax`,
  `ErrPrecision` and `ErrSize` classify as `errs.Invalid`, as the
  package documentation and RFC-0025 state. They classified as
  Unspecified.
- The package documentation of `rand`, `epoch`, `id` and
  `crypto/sign/ecdsap384` refers only to packages and symbols that
  exist: `rand/constant` for the renamed `rand/fixed`, `Counter` for
  `Epoch.Counter`, and `tlog.LeafHash` and `tlog.Root` for the removed
  `crypto.Hasher.Combine`. The `id` documentation no longer refers to
  a `crypto/kem` package.
- `resilience.Call` records no outcome for a call whose context ended
  with an error. It recorded a success, which cleared the failure count
  of a closed circuit, so a dependency that hangs never opened it, and
  counted the abandoned probe of a half-open circuit toward closing it.
  The circuit now admits its next probe and keeps its counts. See
  RFC-0052.
- `resilience.Call` releases the probe of a half-open circuit when its
  function panics. The circuit refused every later call once a caller
  recovered from such a panic. The panic still reaches the caller.
- `tsp.Verifier` parses copies of the certificates of a token, and keeps
  no reference to the token after `Verify` returns. It kept a chain whose
  certificates referred to the caller's buffer, so a caller that reused
  the buffer made every later token of that authority fail with
  `tsp.ErrSignature`. `Info.Chain` no longer refers to the token.
- `httpclient.Client.Fetch` and `AppendFetch` classify a read of a body
  that ends at the client's `WithTimeout` as `errs.Transient`, as their
  documentation states. They checked the context of `resp.Request`, which
  `http.Client` also ends at its timeout, so the error was unclassified,
  and a breaker and a retrier did not count it.
- `btree.Map`, `MapFunc` and `Set` refer to no key that `Delete` or
  `DeleteRange` removed. A removal of the first key of a leaf gives the
  separator of that key the new first key of the leaf. The separator kept
  the removed key, so a caller that reused the memory of a `[]byte` key
  of a `MapFunc` changed the separator, and the map no longer found the
  keys of the leaves before it.

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

[Unreleased]: https://github.com/thesmos-ai/core/compare/v0.6.1...HEAD
[0.6.1]: https://github.com/thesmos-ai/core/releases/tag/v0.6.1
[0.5.0]: https://github.com/thesmos-ai/core/releases/tag/v0.5.0
