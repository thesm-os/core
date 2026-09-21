// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package blob is the named-object storage seam: caller-keyed
// objects, streamed in both directions, with conditional writes
// supplied by the version vocabulary. It is the write half [io/fs]
// never defined.
//
// # The kind
//
// blob answers the storage-kind discriminators as its own kind:
// absence is an error; bodies stream in both directions, because
// the key is independent of the content and a body can flow
// through without ever being held whole; the caller mints the key;
// and anything may change after a write, guarded by the proof
// token carried on [Info].
//
// Queries, TTLs, lifecycle policy, and batch surfaces are
// deliberately absent. [Store.List] narrows by key prefix only —
// anything with a predicate over values or metadata needs an
// expression language, which is domain vocabulary; expiry is
// policy over a store, not a store; and batching is a transport
// optimisation adapters may exploit beneath the seam without
// shaping it.
//
// # Failure semantics
//
// Absence on read classifies as [go.thesmos.sh/core/errs.NotFound];
// a failed IfMatch precondition returns
// [go.thesmos.sh/core/version.ErrMismatch]; a failed create-only
// precondition returns [go.thesmos.sh/core/version.ErrExists]; the
// empty key classifies as [go.thesmos.sh/core/errs.Invalid] on
// every method. The seam introduces no sentinels of its own.
package blob

import (
	"context"
	"io"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/page"
	"go.thesmos.sh/core/version"
)

// Info is an object's metadata, without its body.
//
// Info carries no digest. Content identity paired with a name is
// consumer vocabulary: a consumer that wants verified blobs
// composes this seam with [go.thesmos.sh/core/cas], or hashes at
// its own boundary and records the (name → digest) pairing in its
// own types.
//
// # Allocation contract
//
// Value type; pass by value.
type Info struct {
	// Key is the object's name, as the caller minted it.
	Key string

	// Version is the proof token for conditional writes, per the
	// version package's contract: equality-only, and unique across
	// deletion cycles, so a stalled writer holding a token from
	// before a delete-and-recreate cannot clobber the successor.
	Version version.Version

	// ContentType is carried opaquely: stored as given on Put,
	// returned as stored, never inferred. Empty means unspecified.
	ContentType string

	// ModTime is the backend's record of the last write. Mapping
	// backend timestamps onto the instant type is the adapter's
	// concern; precision is whatever the backend affords.
	ModTime clock.Instant

	// Size is the body's byte length; -1 when the backend cannot
	// report it without reading the body. On the Info returned by
	// [Store.Put] it is never -1: the store just consumed the
	// body, so it knows.
	Size int64
}

// PutOptions carries a Put's metadata and preconditions.
//
// # Allocation contract
//
// Value type; pass by value.
type PutOptions struct {
	// ContentType is stored with the object and round-trips on
	// Get and Stat. Empty means unspecified.
	ContentType string

	// Write carries the conditional-write preconditions. The zero
	// value is an unconditional overwrite. A failing IfMatch
	// returns [version.ErrMismatch]; a failing IfNoneMatch
	// returns [version.ErrExists].
	Write version.WriteOptions
}

// Store is named object storage.
//
// Keys are non-empty strings; the empty key classifies as
// [go.thesmos.sh/core/errs.Invalid] on every method. Beyond
// non-emptiness the key space is the backend's, and prefix
// narrowing in [Store.List] is byte-prefix over whatever keys
// exist.
//
// # Fencing
//
// A Store implementation MAY be fenced: the fence epoch binds at
// handle construction and every write validates it atomically
// with the write, per the fence laws documented on
// [go.thesmos.sh/core/epoch.Admissible].
//
// # Concurrency
//
// Implementations must be safe for concurrent use. Concurrent
// writers to one key are serialised by the backend in some order;
// conditional writes are how a caller makes that order matter.
type Store interface {
	// Put stores the object under key, consuming r to EOF.
	//
	// Visibility is atomic even where the upload is not: a Put
	// that fails for ANY reason — a reader error mid-stream,
	// cancellation, a failed precondition — MUST leave the key
	// exactly as it was, and a concurrent Get during a Put MUST
	// observe the previous object or its absence, never a
	// truncated body. The returned [Info] describes the object as
	// written, including its new Version.
	Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (Info, error)

	// Get opens the object for reading; the caller closes the
	// reader. The bytes read are one consistent object — the
	// version named by the returned [Info] — even if the key is
	// overwritten while the reader is open. Absence classifies as
	// [go.thesmos.sh/core/errs.NotFound].
	Get(ctx context.Context, key string) (io.ReadCloser, Info, error)

	// Stat returns metadata without the body. Absence classifies
	// as [go.thesmos.sh/core/errs.NotFound].
	Stat(ctx context.Context, key string) (Info, error)

	// Delete removes the object subject to opts.
	//
	// An unconditional Delete of an absent key succeeds: the
	// caller's intent — that it be gone — holds. A conditional
	// Delete (IfMatch) of an absent key returns
	// [version.ErrMismatch]: the precondition names a version and
	// no version is present. IfNoneMatch on a Delete classifies as
	// [go.thesmos.sh/core/errs.Invalid] — a create-only
	// precondition on a removal is a category error, not a
	// request.
	Delete(ctx context.Context, key string, opts version.WriteOptions) error

	// List enumerates objects whose keys begin with prefix, one
	// page per call; the empty prefix enumerates everything.
	//
	// Order is unpromised, but a cursor chain is complete: over a
	// store with no concurrent mutation, walking pages to
	// exhaustion yields every matching object exactly once. Under
	// concurrent mutation, objects present for the walk's whole
	// lifetime are yielded exactly once; objects created or
	// deleted mid-walk may or may not appear.
	List(ctx context.Context, prefix string, p page.Page) (page.Cursor[Info], error)
}
