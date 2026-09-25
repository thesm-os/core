// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package memory provides an in-process [blob.Store] for tests and
// local development — the role [go.thesmos.sh/core/crypto/localkey]
// plays for key custody. It is also the conformance suite's first
// subject, which keeps the blob laws tested inside this module's
// own gates rather than deferred to the first external adapter.
//
// memory is for wiring tests and local runs only. Contents live in
// process memory with no durability and no capacity bound.
package memory

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/page"
	"go.thesmos.sh/core/version"
)

// defaultPageSize bounds a List page when the request leaves the
// limit unset, per the page package's convention that a limit at or
// below zero means "the implementation's default".
const defaultPageSize = 50

// Sentinel causes for this implementation's failures.
//
// The seam's contract is the classification — and, for conditional
// writes, the version package's sentinels — not these causes. They
// are unexported so no consumer couples to one implementation's
// spelling; they exist so this package's own tests can assert cause
// identity as well as class.
var (
	errBadKey    = errors.New("memory: key is not a valid object name")
	errAbsent    = errors.New("memory: object not present")
	errBadOffset = errors.New("memory: negative offset")
)

// object pairs a stored body with its metadata. The body slice is
// never mutated after the object is constructed — overwrites
// replace the whole object — which is what lets an open reader keep
// serving its version after the key moves on, with no copy.
type object struct {
	data []byte
	info blob.Info
}

// Store is a mutex-guarded in-process [blob.Store].
//
// Atomic visibility falls out of the shape: Put buffers the entire
// body BEFORE taking the lock, so the map only ever holds complete
// objects, and a failed read, precondition, or context leaves the
// previous object untouched because nothing was swapped. Reader
// snapshots fall out of immutability: Get wraps the stored slice,
// and since overwrites replace objects rather than mutating them,
// an open reader keeps its version for free.
//
// Versions come from a monotonic counter that never resets and
// never reuses a value — including across delete-and-recreate of
// the same key — per the version package's uniqueness requirement.
// The tokens are equality-only like every Version; their numeric
// look carries no ordering contract.
//
// The clock is injected because [blob.Info.ModTime] is an instant
// and this module never reads wall time ambiently.
//
// # Concurrency
//
// Safe for concurrent use. One mutex guards the map and the
// counter; bodies are buffered outside it, so the lock is held only
// for pointer-sized work.
//
// # Allocation contract
//
// Put allocates the buffered body; Get allocates one reader wrapper
// and no body copy; ReadRange, Stat and Delete allocate nothing on
// their happy paths; List allocates the page snapshot.
type Store struct {
	m   map[string]object
	c   clock.Clock
	seq uint64
	mu  sync.Mutex
}

// Compile-time interface checks.
var (
	_ blob.Store       = (*Store)(nil)
	_ blob.RangeReader = (*Store)(nil)
)

// New returns an empty [Store] reading wall time from c. The
// clock is injected so a test drives [blob.Info.ModTime]
// deterministically; the value is stdlib time, not an instant.
func New(c clock.Clock) *Store {
	return &Store{c: c, m: map[string]object{}}
}

// Put stores the object under key, consuming r to EOF.
//
// The body is buffered in full before any state is examined, so
// every failure — a reader error mid-stream, a failed precondition,
// a done context — leaves the key exactly as it was, and a
// concurrent Get can never observe a partial body.
//
// Error modes, all leaving the store untouched:
//
//   - ctx already done — the context's error, unwrapped.
//   - key fails [blob.ValidKey] — classifies as [errs.Invalid].
//   - r fails mid-stream — the reader's error, unwrapped; the
//     bytes consumed so far are discarded.
//   - opts.Write.IfMatch names a version other than the stored one,
//     or names any version while the key is absent —
//     [version.ErrMismatch].
//   - opts.Write.IfNoneMatch is the wildcard and the key exists, or
//     names the version the key currently holds —
//     [version.ErrExists].
//
// # Allocation contract
//
// One buffer for the body, one Info; nothing further on error
// paths.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
	if err := ctx.Err(); err != nil {
		return blob.Info{}, err
	}

	if !blob.ValidKey(key) {
		return blob.Info{}, errs.WithClass(errBadKey, errs.Invalid)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return blob.Info{}, err //nolint:wrapcheck // the reader's own failure is the whole story
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, exists := s.m[key]

	if m := opts.Write.IfMatch; !m.IsZero() {
		if !exists || cur.info.Version != m {
			return blob.Info{}, version.ErrMismatch
		}
	}

	if nm := opts.Write.IfNoneMatch; !nm.IsZero() {
		if exists && (nm.IsWildcard() || cur.info.Version == nm) {
			return blob.Info{}, version.ErrExists
		}
	}

	s.seq++
	info := blob.Info{
		Key:         key,
		Size:        int64(len(data)),
		Version:     version.Version(strconv.FormatUint(s.seq, 10)),
		ModTime:     s.c.Time(),
		ContentType: opts.ContentType,
	}
	s.m[key] = object{data: data, info: info}

	return info, nil
}

// Get opens the object for reading.
//
// The returned reader wraps the stored body directly — no copy —
// which is safe because stored bodies are immutable: an overwrite
// replaces the object, so an open reader keeps serving the version
// named by the Info it was returned with.
//
// Error modes: a done ctx returns the context's error; a key
// [blob.ValidKey] rejects classifies as [errs.Invalid]; absence
// classifies as [errs.NotFound].
//
// # Allocation contract
//
// One reader wrapper; the body is not copied.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, blob.Info{}, err
	}

	if !blob.ValidKey(key) {
		return nil, blob.Info{}, errs.WithClass(errBadKey, errs.Invalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	obj, ok := s.m[key]
	if !ok {
		return nil, blob.Info{}, errs.WithClass(errAbsent, errs.NotFound)
	}

	return io.NopCloser(bytes.NewReader(obj.data)), obj.info, nil
}

// ReadRange copies len(dst) bytes of the stored body, starting at off,
// into dst, with the count and the error of [bytes.Reader.ReadAt].
//
// The store takes the object that the key names under the lock, and
// compares ifMatch with that object's version. Stored bodies are
// immutable, so the bytes it copies after the lock is released belong
// to the version the returned Info names.
//
// Error modes, each with the zero Info:
//
//   - ctx already done — the context's error, unwrapped.
//   - key fails [blob.ValidKey] — classifies as [errs.Invalid].
//   - off is negative — classifies as [errs.Invalid].
//   - the key is absent — classifies as [errs.NotFound], whatever
//     ifMatch names.
//   - a non-zero ifMatch names a version other than the stored one —
//     [version.ErrMismatch].
//
// # Allocation contract
//
// Zero alloc on the happy path. It copies into dst.
func (s *Store) ReadRange(
	ctx context.Context, key string, off int64, dst []byte, ifMatch version.Version,
) (int, blob.Info, error) {
	if err := ctx.Err(); err != nil {
		return 0, blob.Info{}, err
	}

	if !blob.ValidKey(key) {
		return 0, blob.Info{}, errs.WithClass(errBadKey, errs.Invalid)
	}

	if off < 0 {
		return 0, blob.Info{}, errs.WithClass(errBadOffset, errs.Invalid)
	}

	s.mu.Lock()
	obj, ok := s.m[key]
	s.mu.Unlock()

	if !ok {
		return 0, blob.Info{}, errs.WithClass(errAbsent, errs.NotFound)
	}

	if !ifMatch.IsZero() && obj.info.Version != ifMatch {
		return 0, blob.Info{}, version.ErrMismatch
	}

	if off >= int64(len(obj.data)) {
		return 0, obj.info, io.EOF
	}

	n := copy(dst, obj.data[off:])
	if n < len(dst) {
		return n, obj.info, io.EOF
	}

	return n, obj.info, nil
}

// Stat returns metadata without the body.
//
// Error modes: a done ctx returns the context's error; a key
// [blob.ValidKey] rejects classifies as [errs.Invalid]; absence
// classifies as [errs.NotFound].
//
// # Allocation contract
//
// Zero alloc on the happy path.
func (s *Store) Stat(ctx context.Context, key string) (blob.Info, error) {
	if err := ctx.Err(); err != nil {
		return blob.Info{}, err
	}

	if !blob.ValidKey(key) {
		return blob.Info{}, errs.WithClass(errBadKey, errs.Invalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	obj, ok := s.m[key]
	if !ok {
		return blob.Info{}, errs.WithClass(errAbsent, errs.NotFound)
	}

	return obj.info, nil
}

// Delete removes the object, subject to ifMatch.
//
// The zero [version.Version] deletes unconditionally, and an
// unconditional delete of an absent key succeeds — the caller's
// intent holds. Error modes:
//
//   - ctx already done — the context's error, unwrapped.
//   - key fails [blob.ValidKey] — classifies as [errs.Invalid].
//   - ifMatch set while the key is absent, or naming a version
//     other than the stored one — [version.ErrMismatch].
//
// # Allocation contract
//
// Zero alloc on the happy path.
func (s *Store) Delete(ctx context.Context, key string, ifMatch version.Version) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if !blob.ValidKey(key) {
		return errs.WithClass(errBadKey, errs.Invalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, exists := s.m[key]

	if m := ifMatch; !m.IsZero() {
		if !exists || cur.info.Version != m {
			return version.ErrMismatch
		}
	}

	delete(s.m, key)

	return nil
}

// List enumerates objects under prefix in key order, one page per
// call.
//
// Key order is an implementation detail, not a seam promise — it
// exists so the continuation token (the last key of the page) is
// stable across calls, which is what makes a walked cursor chain
// complete over a quiescent store. The token names a key, so a
// mid-walk mutation shifts only which not-yet-visited objects
// appear, never re-yielding a visited one.
//
// Error modes: a done ctx returns the context's error.
//
// # Allocation contract
//
// One snapshot of the matching page per call.
func (s *Store) List(ctx context.Context, prefix string, p page.Page) (page.Cursor[blob.Info], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p = p.WithDefault(defaultPageSize)

	s.mu.Lock()

	keys := make([]string, 0, len(s.m))
	for k := range s.m {
		if strings.HasPrefix(k, prefix) && k > p.Token {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	truncated := len(keys) > p.Limit
	if truncated {
		keys = keys[:p.Limit]
	}

	infos := make([]blob.Info, 0, len(keys))
	for _, k := range keys {
		infos = append(infos, s.m[k].info)
	}

	s.mu.Unlock()

	// A continuation token exists only when this page was truncated:
	// the pre-truncation set was every remaining match, so an
	// untruncated page — even an exactly-full one — is the last, and
	// emitting a token for it would cost every walker one spurious
	// empty round trip.
	next := ""
	if truncated {
		next = keys[len(keys)-1]
	}

	return page.NewSliceCursor(infos, next), nil
}
