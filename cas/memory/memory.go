// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package memory provides an in-process [cas.Store] for tests and
// local development — the role [go.thesmos.sh/core/crypto/localkey]
// plays for key custody. It is also the conformance suite's first
// subject, which is what keeps the CAS laws tested inside this
// module's own gates rather than deferred to the first external
// adapter.
//
// memory is for wiring tests and local runs only. Contents live in
// process memory with no durability, no capacity bound, and no
// eviction — an unbounded cache of everything ever put.
package memory

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"

	"go.thesmos.sh/core/cas"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
)

// Sentinel causes for this implementation's failures.
//
// The seam's contract is the classification, not the cause: callers
// dispatch on [errs.Classify], and these sentinels are unexported
// because no consumer should couple to one implementation's
// spelling. They exist so this package's own tests can assert cause
// identity as well as class.
var (
	errZeroDigest = errors.New("memory: the zero digest is not an address")
	errMismatch   = errors.New("memory: data does not hash to the supplied address")
	errAbsent     = errors.New("memory: address not present")
)

// Store is a mutex-guarded in-memory [cas.Store].
//
// The map is keyed by [crypto.Digest] directly. The type is
// comparable by design, and its size participates in identity: a
// 256-bit and a 384-bit digest that share a byte prefix are
// distinct addresses, which a key built from the active bytes alone
// would collapse. Hashing the full struct per lookup is the price
// of that correctness, and in a test double it is the right trade.
//
// Put clones data before storing, because the bytes were verified
// against the address at put time and an aliased caller slice
// mutated afterwards would silently break every future Get's
// integrity. Get appends into the caller's buffer, so a caller
// reading at rate allocates nothing.
//
// Store implements [cas.Streamer]. Storage is memory, so a streamed
// value ends up resident either way, but the native paths are not
// the buffering fallback: PutStream hashes during the read rather
// than after it, which is one pass over the bytes instead of two,
// and GetStream serves the stored slice without copying it.
//
// # Concurrency
//
// Safe for concurrent use. One mutex guards the map; holding it
// across the presence check and the insert is what makes the
// exactly-one-wrote law trivial rather than a compare-and-swap
// protocol. Contention is not this package's concern — it is a test
// double, and a production adapter makes its own locking argument.
//
// # Allocation contract
//
// One clone per written Put. Get allocates only when dst is too
// small to hold the value; Has, Hasher and GetStream allocate
// nothing beyond one reader wrapper. PutStream allocates the staged
// buffer it commits. The verification hash follows the bound
// [crypto.Hasher]'s own allocation contract, which is zero for
// every implementation in this module.
type Store struct {
	h  crypto.Hasher
	m  map[crypto.Digest][]byte
	mu sync.Mutex
}

// Compile-time interface checks.
var (
	_ cas.Store    = (*Store)(nil)
	_ cas.Streamer = (*Store)(nil)
)

// New returns an empty [Store] whose addresses are verified against
// h. The store is bound to h for its lifetime: one address space,
// one algorithm, per the seam contract.
func New(h crypto.Hasher) *Store {
	return &Store{h: h, m: map[crypto.Digest][]byte{}}
}

// Hasher returns the algorithm this store's addresses are computed
// under, which is the one passed to [New].
//
// # Allocation contract
//
// Zero alloc.
func (s *Store) Hasher() crypto.Hasher { return s.h }

// Put stores data under its digest, verifying with the bound hasher
// that the digest of data equals d before anything is stored.
//
// Returns (true, nil) when the write happened, and (false, nil)
// when the address was already present — the idempotent no-op that
// makes retrying a Put safe and the signal that lets deduplication
// accounting exist at the seam. Under concurrent Puts of one
// address, exactly one caller observes true.
//
// Error modes, all leaving the store untouched:
//
//   - ctx already done — the context's error, unwrapped.
//   - d is the zero digest — classifies as [errs.Invalid]; the zero
//     [crypto.Digest] is the "no digest computed" sentinel, not an
//     address.
//   - digest(data) != d — classifies as [errs.Integrity]; storing
//     would let garbage be served under a trusted address to every
//     holder of d.
//
// # Allocation contract
//
// One clone of data on the written path; nothing on the duplicate
// or error paths.
func (s *Store) Put(ctx context.Context, d crypto.Digest, data []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	if d.IsZero() {
		return false, errs.WithClass(errZeroDigest, errs.Invalid)
	}

	if !s.h.Hash(data).Equal(d) {
		return false, errs.WithClass(errMismatch, errs.Integrity)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.m[d]; ok {
		return false, nil
	}
	s.m[d] = bytes.Clone(data)

	return true, nil
}

// Get appends the bytes stored under d to dst and returns the
// extended slice.
//
// The appended bytes are a copy, so neither the caller mutating
// them nor any later Put can affect the other. This implementation
// does not re-verify on read — the bytes were verified at put time
// and are unreachable for mutation afterwards, so a read-side check
// would re-prove an invariant the type system already holds.
//
// Error modes, all returning dst unchanged:
//
//   - ctx already done — the context's error, unwrapped.
//   - d is the zero digest — classifies as [errs.Invalid].
//   - d is absent — classifies as [errs.NotFound]; an address is
//     minted from bytes that existed, so absence here means the
//     caller holds an address this store never stored.
//
// # Allocation contract
//
// Zero alloc when dst has room for the value; one growth of dst
// when it does not.
func (s *Store) Get(ctx context.Context, d crypto.Digest, dst []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return dst, err
	}

	if d.IsZero() {
		return dst, errs.WithClass(errZeroDigest, errs.Invalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.m[d]
	if !ok {
		return dst, errs.WithClass(errAbsent, errs.NotFound)
	}

	return append(dst, v...), nil
}

// PutStream reads r to EOF, hashing as it reads, and commits the
// bytes under d only once they hash to d.
//
// The read is a single pass: the bytes are hashed on their way into
// the staging buffer rather than re-walked afterwards. Nothing
// reaches the map until the digest agrees, so a reader failure, a
// digest mismatch or a context cancelled during the transfer leaves
// d absent.
//
// A present address returns (false, nil) without reading r at all.
// Not transferring a duplicate is the point of content addressing,
// so the caller owns r and must not assume it was consumed.
//
// Error modes, all leaving the store untouched:
//
//   - ctx done before or during the read — the context's error,
//     unwrapped.
//   - d is the zero digest — classifies as [errs.Invalid].
//   - r fails mid-stream — the reader's error, unwrapped; the bytes
//     consumed so far are discarded.
//   - digest(bytes) != d — classifies as [errs.Integrity].
//
// # Allocation contract
//
// One staging buffer the size of the object, committed as-is on
// success. One [crypto.Stream] borrowed from the hasher's pool and
// returned before PutStream returns.
func (s *Store) PutStream(ctx context.Context, d crypto.Digest, r io.Reader) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	if d.IsZero() {
		return false, errs.WithClass(errZeroDigest, errs.Invalid)
	}

	s.mu.Lock()
	_, present := s.m[d]
	s.mu.Unlock()

	if present {
		return false, nil
	}

	hash := s.h.NewStream()
	defer hash.Close()

	staged, err := io.ReadAll(io.TeeReader(r, hash))
	if err != nil {
		return false, err //nolint:wrapcheck // the reader's own failure is the whole story
	}

	// A transfer that outlived its context must not commit, however
	// well its bytes verify.
	if err := ctx.Err(); err != nil {
		return false, err
	}

	if !hash.Sum().Equal(d) {
		return false, errs.WithClass(errMismatch, errs.Integrity)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.m[d]; ok {
		return false, nil
	}
	s.m[d] = staged

	return true, nil
}

// GetStream opens the bytes stored under d; the caller closes the
// reader.
//
// The reader serves the stored slice directly, with no copy. That is
// safe because a stored value is immutable once committed: Put and
// PutStream insert a fresh slice and nothing ever writes into one
// already in the map, so an open reader keeps its bytes for its
// whole lifetime.
//
// Error modes:
//
//   - ctx already done — the context's error, unwrapped.
//   - d is the zero digest — classifies as [errs.Invalid].
//   - d is absent — classifies as [errs.NotFound].
//
// # Allocation contract
//
// One reader wrapper; the body is not copied.
func (s *Store) GetStream(ctx context.Context, d crypto.Digest) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if d.IsZero() {
		return nil, errs.WithClass(errZeroDigest, errs.Invalid)
	}

	s.mu.Lock()
	v, ok := s.m[d]
	s.mu.Unlock()

	if !ok {
		return nil, errs.WithClass(errAbsent, errs.NotFound)
	}

	return io.NopCloser(bytes.NewReader(v)), nil
}

// Has reports whether d is present, without transferring the body.
//
// Absence is a false report here, not an error — Has exists
// precisely to ask the question cheaply, and only the zero digest
// (classifying as [errs.Invalid]) or a done ctx produce errors.
//
// # Allocation contract
//
// Zero alloc.
func (s *Store) Has(ctx context.Context, d crypto.Digest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	if d.IsZero() {
		return false, errs.WithClass(errZeroDigest, errs.Invalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.m[d]

	return ok, nil
}
