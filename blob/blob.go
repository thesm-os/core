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
// precondition returns [go.thesmos.sh/core/version.ErrExists]; a
// key [ValidKey] rejects classifies as
// [go.thesmos.sh/core/errs.Invalid] on every method. The seam
// introduces no sentinels of its own.
package blob

import (
	"context"
	"io"
	"io/fs"
	"strings"
	"time"

	"go.thesmos.sh/core/page"
	"go.thesmos.sh/core/version"
)

// Key bounds, chosen so a key that satisfies [ValidKey] travels
// between the backends this seam is meant to sit in front of.
const (
	// MaxKeyLen is the longest key in bytes. Object stores in the
	// S3 family cap a key at this length.
	MaxKeyLen = 1024

	// MaxKeyElemLen is the longest single slash-separated element
	// in bytes. Most filesystems cap one path component here, and a
	// key that ignores it cannot be laid out on disk at all.
	MaxKeyElemLen = 255
)

// ValidKey reports whether key names an object.
//
// A key is a slash-separated path in the [io/fs.ValidPath] sense:
// UTF-8, unrooted, with no empty element and no "." or ".."
// element. The root itself is excluded, because "." names a place
// rather than an object. Beyond that a key is at most [MaxKeyLen]
// bytes and each element is at most [MaxKeyElemLen] bytes.
//
// The rules are the standard library's because a key has to be
// writable by a caller who does not know which backend is behind
// the seam. Left to each backend, a key that works against an
// object store escapes the root on a filesystem, and no consumer
// can write a portable one.
//
// This governs keys, not [Store.List] prefixes. A prefix may end
// part-way through an element — "pho" is a legal prefix and not a
// legal key — so validating a prefix with this predicate rejects
// the narrowing a caller is entitled to ask for.
//
// # Allocation contract
//
// Zero alloc.
func ValidKey(key string) bool {
	if key == "." || len(key) > MaxKeyLen || !fs.ValidPath(key) {
		return false
	}

	for elem := range strings.SplitSeq(key, "/") {
		if len(elem) > MaxKeyElemLen {
			return false
		}
	}

	return true
}

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

	// ModTime is the backend's record of the last write, and is
	// stdlib time rather than an instant: it is an external fact
	// reported by storage, not an event in this deployment's causal
	// chain. Precision is whatever the backend affords.
	//
	// On the Info returned by [Store.Put] it MAY be zero. A backend
	// that does not report a timestamp on write — the S3 family
	// returns an entity tag and no last-modified — would otherwise
	// have to read the object back to fill the field in. [Store.Get]
	// and [Store.Stat] MUST report it.
	ModTime time.Time

	// ContentType is carried opaquely: stored as given on Put,
	// returned as stored, never inferred. Empty means unspecified.
	ContentType string

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
// # Keys
//
// A key satisfies [ValidKey]. One that does not classifies as
// [go.thesmos.sh/core/errs.Invalid] on every method, and the
// method rejects it before touching storage.
//
// Nesting is not restricted. A store may hold "a/b" and "a/b/c" at
// once, because the object stores this seam fronts do. A backend
// whose native namespace cannot hold both — a filesystem, where a
// file cannot sit inside a file — MUST encode keys so that it can,
// by giving each key its own directory or sharding under a hash
// rather than mapping a key onto a path. Pushing the restriction
// up into the contract would narrow the seam to its least capable
// backend, cost a lookup and a prefix scan on every write, and
// still not hold: two concurrent writes of "a/b" and "a/b/c" each
// pass their own check.
//
// Prefix narrowing in [Store.List] is byte-prefix and is not
// governed by [ValidKey]. A prefix may end part-way through an
// element.
//
// # Context
//
// ctx cancels the call and bounds its duration. A method returns
// the context's error when it is already done, and abandons work
// in progress when it becomes done mid-call. It carries no
// authorisation and selects no backend; an implementation that
// reads values from it is adding vocabulary this seam does not
// define.
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

	// Delete removes the object, subject to ifMatch.
	//
	// The zero [version.Version] deletes unconditionally, and an
	// unconditional Delete of an absent key succeeds: the caller's
	// intent — that it be gone — holds. A non-zero ifMatch naming
	// any other version, including against an absent key, returns
	// [version.ErrMismatch].
	//
	// The parameter is one version rather than the full
	// [version.WriteOptions] because only one of that struct's
	// fields means anything here. A create-only precondition on a
	// removal is a category error, and a parameter that accepts one
	// in order to reject it is a mistake the caller can write.
	Delete(ctx context.Context, key string, ifMatch version.Version) error

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
