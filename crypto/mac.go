// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

// MAC computes keyed message authentication codes — fixed-size
// [Digest] outputs that prove the holder of a shared key wrote or
// approved the input bytes.
//
// MAC is the keyed-authentication peer of [Hasher]: same family
// of value type ([Digest]), same identifier model ([ID] +
// [Algorithm]), same streaming surface ([Stream]). The
// substantive differences from [Hasher] are:
//
//   - The output is keyed. Two MAC instances built over the same
//     algorithm but different keys produce uncorrelated outputs
//     for the same input.
//   - There is no [Hasher.Combine] analogue. HMAC and related
//     constructions do not compose tree-wise:
//     `MAC(K, a||b) ≠ Combine(MAC(K, a), MAC(K, b))`.
//   - Verification is a first-class operation. [MAC.Verify]
//     compares an expected MAC against a freshly-computed one in
//     constant time, closing the timing-oracle hazard that a
//     [Digest] equality check would expose for untrusted inputs.
//
// # Method semantics
//
//   - [MAC.ID] returns the implementation's stable build-local
//     identifier — same shape as [Hasher.ID].
//   - [MAC.Algorithm] returns the long-term cross-build algorithm
//     name (for example [AlgHMACSHA256]). Persist it in
//     artefacts that may outlive the producing build.
//   - [MAC.Size] returns the output size in bytes — one of
//     [DigestSize256], [DigestSize384], [DigestSize512]. Hot-path
//     callers preallocate fixed-size buffers (signature header,
//     fixed-width column) without consulting an algorithm table.
//   - [MAC.Sign] returns the [Digest] of the input bytes under the
//     instance's key. Hot path for one-shot signatures.
//   - [MAC.Verify] reports whether expected is the MAC of data
//     under the instance's key. The comparison is performed in
//     constant time over the active byte prefix; size mismatch
//     short-circuits to false. Use this in preference to
//     [MAC.Sign] followed by [Digest.Equal] when the expected
//     value is supplied by an untrusted party.
//   - [MAC.NewStream] returns a fresh [Stream] for streaming
//     inputs that don't fit in memory. Streaming verification is
//     [Stream.Sum] followed by [Digest.ConstantTimeEqual].
//
// # Concurrency
//
// Implementations of [MAC] must be safe for concurrent use;
// consumers share one MAC across many goroutines. The returned
// [Stream] is NOT safe for concurrent use — each goroutine that
// streams should hold its own.
//
// # Allocation contract
//
// [MAC.ID], [MAC.Algorithm], and [MAC.Size] are
// zero-allocation on every implementation in this module.
//
// [MAC.Sign] and [MAC.Verify] each allocate the underlying HMAC
// state once per call — the stdlib's only entry point is
// `hmac.New`, which returns a heap-allocated [hash.Hash]. There
// is no zero-allocation one-shot HMAC primitive in the stdlib,
// in contrast to `sha256.Sum256` for plain hashing. Hot-path
// callers should construct one [Stream] per goroutine via
// [MAC.NewStream] and reuse it across [Stream.Reset] cycles.
//
// [MAC.NewStream] allocates the wrapper plus the underlying
// stdlib HMAC state on construction; [Stream.Write],
// [Stream.Sum], and [Stream.Reset] are zero-allocation
// thereafter.
//
// # Verification timing
//
// [MAC.Verify] compares the freshly-computed MAC against the
// caller-supplied expected bytes using a constant-time
// comparison ([crypto/subtle.ConstantTimeCompare]). The size
// check before the comparison is *not* constant-time; size is
// public information determined by [MAC.Algorithm], so leaking
// it through timing is harmless.
//
// For the streaming case, finalise the [Stream] with
// [Stream.Sum] and use [Digest.ConstantTimeEqual] to compare
// against the expected [Digest]. Plain `==` and [Digest.Equal]
// are NOT constant-time and must not be used to compare a MAC
// against untrusted input.
type MAC interface {
	// ID returns the implementation's stable build-local
	// identifier. Persisted alongside MAC outputs so the same
	// implementation that produced a digest can be re-selected
	// when verifying.
	ID() ID

	// Algorithm returns the long-term cross-build algorithm
	// name (for example [AlgHMACSHA256]). Persist it in
	// artefacts that may outlive the producing build.
	Algorithm() Algorithm

	// Size returns the output size in bytes — one of
	// [DigestSize256], [DigestSize384], [DigestSize512].
	// Hot-path callers preallocate fixed-size buffers
	// (signature header, fixed-width column) without consulting
	// an algorithm-table dispatch.
	Size() int

	// Sign returns the [Digest] of data under the instance's
	// key. Hot path for one-shot signatures. Allocates the
	// underlying HMAC state once per call (stdlib constraint);
	// for sustained throughput, use [MAC.NewStream] with
	// [Stream.Reset] between messages.
	Sign(data []byte) Digest

	// Verify reports whether expected is the MAC of data under
	// the instance's key. The comparison is performed in
	// constant time over the active byte prefix; size mismatch
	// short-circuits to false. Use this in preference to
	// [MAC.Sign] followed by [Digest.Equal] when expected is
	// supplied by an untrusted party.
	Verify(data, expected []byte) bool

	// NewStream returns a fresh [Stream] for streaming inputs
	// that don't fit in memory. Streaming verification is
	// [Stream.Sum] followed by [Digest.ConstantTimeEqual]
	// against the expected digest.
	//
	//testkit:nondeterministic
	NewStream() Stream
}
