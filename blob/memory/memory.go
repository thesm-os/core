// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package memory provides an in-process [blob.Store] for tests and local
// development. Core runs the conformance suite of
// [go.thesmos.sh/core/coretest/blobtest] against it.
//
// A Store keeps its objects in process memory, with no durability and no
// capacity bound.
package memory

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/btree"
	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/page"
	"go.thesmos.sh/core/version"
)

// defaultPageSize is the size of a List page whose limit is zero or
// negative, which the page package defines as the implementation's
// default.
const defaultPageSize = 50

// These are the causes of the errors that this package returns. The
// contract of the seam is the class of an error, and for a conditional
// write the sentinel of the version package. The causes are unexported,
// so no caller depends on them.
var (
	errBadKey         = errors.New("memory: key is not a valid object name")
	errBadContentType = errors.New("memory: content type is not a valid content type")
	errAbsent         = errors.New("memory: object not present")
	errBadOffset      = errors.New("memory: negative offset")
)

// object is a stored body and its metadata. No code writes to the body
// after the object is built. An overwrite replaces the whole object, so
// an open reader returns its version without a copy.
type object struct {
	data []byte
	info blob.Info
}

// Store is an in-process [blob.Store] behind one mutex.
//
// Put reads the whole body before it takes the lock, so the map contains
// only complete objects, and a failed Put leaves the previous object as
// it was. Get returns a reader over the stored body. An overwrite
// replaces the object and leaves the body unchanged, so an open reader
// returns its version.
//
// Versions come from a counter that never resets, so no version repeats
// for a key, also after the key is deleted and written again. A version
// is compared by equality only, and its digits define no order.
//
// The clock passed to [New] supplies [blob.Info.ModTime]. The package
// does not read the wall clock itself.
//
// A [btree.Map] orders the objects by key, so [Store.List] reads a page
// of p objects in O(log n + p) for n objects.
//
// # Concurrency
//
// Safe for concurrent use. One mutex guards the map and the counter.
// Put buffers a body before it locks the mutex, so each method locks
// it only around the map operation.
//
// # Allocation contract
//
//   - Put allocates the buffered body, and map nodes as the map grows.
//   - Get allocates one reader wrapper and does not copy the body.
//   - ReadRange, Stat and Delete allocate nothing on their happy paths.
//   - List allocates the page snapshot.
type Store struct {
	c   clock.Clock
	m   btree.Map[string, object]
	seq uint64
	mu  sync.Mutex
}

// Compile-time interface checks.
var (
	_ blob.Store       = (*Store)(nil)
	_ blob.RangeReader = (*Store)(nil)
)

// New returns an empty [Store] that reads the time of each write from c.
// A test that passes a fake clock controls [blob.Info.ModTime].
func New(c clock.Clock) *Store {
	return &Store{c: c}
}

// Put stores the object under key, and reads r to EOF.
//
// Put reads the whole body before it examines the store. A failed Put
// leaves the key as it was, and a concurrent Get returns the previous
// object or its absence.
//
// Error modes, each of which leaves the store unchanged:
//
//   - A done ctx returns the context's error, unwrapped.
//   - A key that [blob.ValidKey] rejects classifies as [errs.Invalid].
//     Put does not read r.
//   - A content type that [blob.ValidContentType] rejects classifies as
//     [errs.Invalid]. Put does not read r.
//   - An error of r returns that error, unwrapped. Put discards the bytes
//     that it read.
//   - An opts.Write.IfMatch that names a version other than the stored
//     one, or any version of an absent key, returns
//     [version.ErrMismatch].
//   - An opts.Write.IfNoneMatch that is the wildcard for an existing key,
//     or that names the stored version, returns [version.ErrExists].
//
// # Allocation contract
//
// One buffer for the body and one Info. An error path does not allocate
// more.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
	if err := ctx.Err(); err != nil {
		return blob.Info{}, err
	}

	if !blob.ValidKey(key) {
		return blob.Info{}, errs.WithClass(errBadKey, errs.Invalid)
	}

	if !blob.ValidContentType(opts.ContentType) {
		return blob.Info{}, errs.WithClass(errBadContentType, errs.Invalid)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return blob.Info{}, err //nolint:wrapcheck // the reader's own failure is the whole story
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, exists := s.m.Get(key)

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
	s.m.Set(key, object{data: data, info: info})

	return info, nil
}

// Get opens the object for reading. The reader reads the stored body
// without a copy. An overwrite replaces the object and leaves the body
// unchanged, so the reader returns the version that the returned Info
// names.
//
// Error modes:
//
//   - A done ctx returns the context's error.
//   - A key that [blob.ValidKey] rejects classifies as [errs.Invalid].
//   - An absent key classifies as [errs.NotFound].
//
// # Allocation contract
//
// One reader. Get does not copy the body.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, blob.Info{}, err
	}

	if !blob.ValidKey(key) {
		return nil, blob.Info{}, errs.WithClass(errBadKey, errs.Invalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	obj, ok := s.m.Get(key)
	if !ok {
		return nil, blob.Info{}, errs.WithClass(errAbsent, errs.NotFound)
	}

	return io.NopCloser(bytes.NewReader(obj.data)), obj.info, nil
}

// ReadRange copies len(dst) bytes of the stored body, starting at off,
// into dst, with the count and the error of [bytes.Reader.ReadAt].
//
// The store takes the object that the key names under the lock, and
// compares ifMatch with that object's version. No code writes to a stored
// body, so the bytes that ReadRange copies after it releases the lock
// belong to the version that the returned Info names.
//
// Error modes, each with the zero Info:
//
//   - A done ctx returns the context's error, unwrapped.
//   - A key that [blob.ValidKey] rejects classifies as [errs.Invalid].
//   - A negative off classifies as [errs.Invalid].
//   - An absent key classifies as [errs.NotFound], whatever ifMatch
//     names.
//   - A non-zero ifMatch that names a version other than the stored one
//     returns [version.ErrMismatch].
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
	obj, ok := s.m.Get(key)
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
// Error modes:
//
//   - A done ctx returns the context's error.
//   - A key that [blob.ValidKey] rejects classifies as [errs.Invalid].
//   - An absent key classifies as [errs.NotFound].
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

	obj, ok := s.m.Get(key)
	if !ok {
		return blob.Info{}, errs.WithClass(errAbsent, errs.NotFound)
	}

	return obj.info, nil
}

// Delete removes the object, subject to ifMatch.
//
// The zero [version.Version] deletes unconditionally, and an
// unconditional Delete of an absent key succeeds.
//
// Error modes:
//
//   - A done ctx returns the context's error, unwrapped.
//   - A key that [blob.ValidKey] rejects classifies as [errs.Invalid].
//   - A non-zero ifMatch for an absent key, or one that names a version
//     other than the stored one, returns [version.ErrMismatch].
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

	cur, exists := s.m.Get(key)

	if m := ifMatch; !m.IsZero() {
		if !exists || cur.info.Version != m {
			return version.ErrMismatch
		}
	}

	s.m.Delete(key)

	return nil
}

// List returns one page of the objects under prefix, in key order.
//
// The seam promises no order. This store lists in key order, and the
// token of a page is its last key, so a walk over a store without
// concurrent writes returns every object once. A write during a walk
// changes only which of the objects that the walk has not reached
// appear. A walk returns each object at most once.
//
// List reads from the first key after the token that has the prefix, and
// stops at the first key without it, so a page of p objects costs
// O(log n + p).
//
// Error modes: a done ctx returns the context's error.
//
// # Allocation contract
//
// One copy of the page per call.
func (s *Store) List(ctx context.Context, prefix string, p page.Page) (page.Cursor[blob.Info], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p = p.WithDefault(defaultPageSize)

	s.mu.Lock()
	defer s.mu.Unlock()

	// A continuation token exists only when this page was truncated,
	// that is when a matching key follows its last object. An
	// untruncated page — even an exactly-full one — is the last, and
	// emitting a token for it would cost every walker one spurious
	// empty round trip.
	infos := make([]blob.Info, 0, min(p.Limit, s.m.Len()))
	next := ""
	for k, obj := range s.m.Ascend(max(prefix, p.Token)) {
		if !strings.HasPrefix(k, prefix) {
			break
		}
		if k == p.Token {
			continue
		}
		if len(infos) == p.Limit {
			next = infos[len(infos)-1].Key

			break
		}
		infos = append(infos, obj.info)
	}

	return page.NewSliceCursor(infos, next), nil
}
