// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package memory provides an in-process [cas.Store] for tests and local
// development, as [go.thesmos.sh/core/crypto/localkey] does for key
// custody. Core runs the conformance suite of
// [go.thesmos.sh/core/coretest/castest] against it, so the laws of the
// seam are tested inside this module.
//
// A Store keeps its values in process memory, with no durability, no
// capacity bound and no eviction.
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

// These are the causes of the errors that this package returns. The
// contract of the seam is the class of an error, which callers read with
// [errs.Classify]. The causes are unexported, so no caller depends on
// them, and the package's own tests compare them.
var (
	errZeroDigest = errors.New("memory: the zero digest is not an address")
	errMismatch   = errors.New("memory: data does not hash to the supplied address")
	errAbsent     = errors.New("memory: address not present")
)

// reader is the [io.ReadCloser] that [Store.GetStream] returns: a
// [bytes.Reader] over a stored value, with the reader's WriterTo, Seeker
// and ReaderAt. The value belongs to the store, so Close releases
// nothing.
//
// # Concurrency
//
// Not safe for concurrent use, as a [bytes.Reader] is not. Readers of one
// value are independent.
//
// # Allocation contract
//
// Zero alloc. GetStream allocates the reader itself.
type reader struct {
	bytes.Reader
}

// Close returns nil. A read after Close reads the value as before.
func (*reader) Close() error {
	return nil
}

// Store is an in-process [cas.Store] behind one mutex.
//
// The map is keyed by [crypto.Digest]. The size of a digest is part of
// its identity, so a 256-bit and a 384-bit digest that share a byte
// prefix are two addresses. Each lookup hashes the whole struct.
//
// Put clones data before it stores it. The bytes were verified against
// the address at the time of the Put, and a clone keeps a later change
// of the caller's slice out of every later Get. Get appends into the
// caller's buffer, so a read into a buffer with room allocates nothing.
//
// Store implements [cas.Streamer]. PutStream hashes the bytes during the
// read, one pass over them, and GetStream reads the stored slice without
// a copy.
//
// # Concurrency
//
// Safe for concurrent use. One mutex guards the map. Put holds it across
// the presence check and the insert, so exactly one of concurrent Puts of
// one address reports wrote=true.
//
// # Allocation contract
//
//   - Put allocates one clone of data when it writes.
//   - Get allocates only when dst has no room for the value.
//   - Has and Hasher do not allocate.
//   - PutStream allocates the buffer that it commits, the reader that
//     hashes during the read, and the growth of the map.
//   - GetStream allocates one reader.
//
// The hash of a verification follows the allocation contract of the
// bound [crypto.Hasher], which is zero for every hasher of this module.
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

// New returns an empty [Store] that verifies addresses against h, for
// its whole lifetime: one address space and one algorithm, as the seam
// requires.
//
// # Allocation contract
//
// The Store and its map.
func New(h crypto.Hasher) *Store {
	return &Store{h: h, m: map[crypto.Digest][]byte{}}
}

// Hasher returns the algorithm of the addresses of the store, the one
// passed to [New].
//
// # Allocation contract
//
// Zero alloc.
func (s *Store) Hasher() crypto.Hasher { return s.h }

// Put stores data under d after it verifies with the bound hasher that
// data hashes to d.
//
// It returns (true, nil) when it wrote, and (false, nil) when d was
// present. A Put of a present address is a no-op, so a retry of a Put is
// safe, and the result counts deduplicated writes. Of concurrent Puts of
// one address, exactly one returns true.
//
// Error modes, each of which leaves the store unchanged:
//
//   - A done ctx returns the context's error, unwrapped.
//   - The zero d classifies as [errs.Invalid]. The zero [crypto.Digest]
//     is no address.
//   - Data that does not hash to d classifies as [errs.Integrity].
//
// # Allocation contract
//
// One clone of data when Put writes. Nothing for a present address or an
// error.
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

// Get appends the bytes stored under d to dst and returns the extended
// slice.
//
// The appended bytes are a copy, so a change by the caller and a later
// Put do not affect each other. Get does not verify the bytes again. Put
// verified them, and no code writes to a stored value.
//
// Error modes, each of which returns dst unchanged:
//
//   - A done ctx returns the context's error, unwrapped.
//   - The zero d classifies as [errs.Invalid].
//   - An absent d classifies as [errs.NotFound].
//
// # Allocation contract
//
// Zero alloc when dst has room for the value. One growth of dst when it
// has not.
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

// PutStream reads r to EOF, hashing as it reads, and commits the bytes
// under d only when they hash to d.
//
// The bytes are hashed on their way into the staging buffer, one pass
// over them. Nothing enters the map until the digest matches, so a
// failure of r, a mismatch or a ctx that ends during the read leaves d
// absent.
//
// A present address returns (false, nil) without a read of r. The
// caller keeps r, and must not assume that PutStream read it.
//
// Error modes, each of which leaves the store unchanged:
//
//   - A ctx that is done before or after the read returns the context's
//     error, unwrapped.
//   - The zero d classifies as [errs.Invalid].
//   - An error of r returns that error, unwrapped. PutStream discards
//     the bytes that it read.
//   - Bytes that do not hash to d classify as [errs.Integrity].
//
// # Allocation contract
//
// Three allocations for a new address: the staging buffer, which
// PutStream commits on success, the reader that hashes the bytes during
// the read, and the growth of the map. Nothing for a present address.
// The [crypto.Stream] comes from the pool of the hasher, and PutStream
// returns it before it returns.
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
		return false, err //nolint:wrapcheck // the error of r is the error of the call
	}

	// A transfer that outlived its context does not commit, whether or
	// not its bytes verify.
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

// GetStream opens the bytes stored under d, and the caller closes the
// reader.
//
// The reader reads the stored slice without a copy. Put and PutStream
// insert a new slice, and no code writes to a stored one, so an open
// reader reads the same bytes until it is closed.
//
// Error modes:
//
//   - A done ctx returns the context's error, unwrapped.
//   - The zero d classifies as [errs.Invalid].
//   - An absent d classifies as [errs.NotFound].
//
// # Allocation contract
//
// One reader. GetStream does not copy the value.
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

	r := new(reader)
	r.Reset(v)

	return r, nil
}

// Has reports whether d is present, without a transfer of the value.
//
// An absent d returns false and no error.
//
// Error modes:
//
//   - A done ctx returns the context's error, unwrapped.
//   - The zero d classifies as [errs.Invalid].
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
