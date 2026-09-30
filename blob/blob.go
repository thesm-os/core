// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package blob defines named object storage. The caller chooses the key of
// each object, a body streams in both directions, and the version package
// makes a write conditional.
//
// # The kind
//
// A blob store has these properties:
//
//   - Reading an absent key is an error.
//   - A body streams in both directions, because the key does not depend
//     on the content. A store never needs a whole body in memory.
//   - The caller chooses the key.
//   - A write can replace any object. The version on [Info] makes a write
//     conditional on the object that it replaces.
//
// The seam has no queries, expiry, lifecycle policy or batch methods.
// [Store.List] narrows by key prefix only. An adapter may batch the
// requests that it sends to its backend.
//
// # Ranged reads
//
// A store that reads part of an object without transferring the rest
// implements the optional [RangeReader] capability. A caller that needs
// ranged reads finds the capability with [AsRangeReader] when it wires
// the store, and refuses a store without it.
//
// # Decorators
//
// A decorator that wraps a Store, for example to add tracing,
// implements Unwrap() Store and returns the store it wraps.
// [AsRangeReader] follows Unwrap to find a RangeReader behind any number
// of decorators, so a decorator does not hide the capability of the
// store it wraps.
//
// # Failure semantics
//
//   - Absence on a read classifies as [go.thesmos.sh/core/errs.NotFound].
//   - A failed IfMatch precondition returns
//     [go.thesmos.sh/core/version.ErrMismatch].
//   - A failed create-only precondition returns
//     [go.thesmos.sh/core/version.ErrExists].
//   - A key that [ValidKey] rejects classifies as
//     [go.thesmos.sh/core/errs.Invalid] on every method.
//   - A content type that [ValidContentType] rejects classifies as
//     [go.thesmos.sh/core/errs.Invalid] on [Store.Put].
//
// The package defines no sentinel errors of its own.
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

// Key bounds. The backends of the seam, the object stores of the S3
// family and filesystems, store every key within them.
const (
	// MaxKeyLen is the longest key in bytes. Object stores in the S3
	// family limit a key to this length.
	MaxKeyLen = 1024

	// MaxKeyElemLen is the longest slash-separated element of a key in
	// bytes. Most filesystems limit a path component to this length, and
	// cannot store a key with a longer element.
	MaxKeyElemLen = 255
)

// MaxContentTypeLen is the longest content type in bytes that
// [ValidContentType] accepts. RFC 6838 limits the type name and the
// subtype name of a media type to 127 characters each, so every type and
// subtype pair fits.
const MaxContentTypeLen = 255

// ValidKey reports whether key names an object.
//
// A key is a slash-separated path that [io/fs.ValidPath] accepts: UTF-8,
// unrooted, with no empty element and no "." or ".." element. The root,
// ".", is not a key, because it names a place and not an object. A key
// has at most [MaxKeyLen] bytes, and each of its elements at most
// [MaxKeyElemLen] bytes.
//
// Every backend of the seam stores a key that ValidKey accepts, so a
// caller writes a key without knowing the backend.
//
// ValidKey checks keys, not the prefixes of [Store.List]. A prefix may
// end inside an element: "pho" is a valid prefix and not a valid key.
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

// ValidContentType reports whether contentType can be the content type of
// an object: at most [MaxContentTypeLen] bytes, each a printable ASCII
// character from space (0x20) to tilde (0x7E). The empty string is valid
// and means unspecified.
//
// Every backend of the seam stores a content type that ValidContentType
// accepts. ValidContentType does not parse the media type.
//
// # Allocation contract
//
// Zero alloc.
func ValidContentType(contentType string) bool {
	if len(contentType) > MaxContentTypeLen {
		return false
	}

	for i := range len(contentType) {
		if contentType[i] < ' ' || contentType[i] > '~' {
			return false
		}
	}

	return true
}

// Info is the metadata of an object, without its body.
//
// Info has no digest. A caller that verifies the bodies of its objects
// composes this seam with [go.thesmos.sh/core/cas], or hashes each body
// itself and records the key and the digest in its own types.
//
// # Allocation contract
//
// Value type; pass by value.
type Info struct {
	// Key is the name of the object, as the caller chose it.
	Key string

	// Version is the token of conditional writes, as the version package
	// defines it: compared by equality only, and unique across deletions.
	// A writer with a version from before a key was deleted and written
	// again cannot overwrite the new object.
	Version version.Version

	// ModTime is the time of the last write, as the backend records it,
	// with the backend's precision. It is a time.Time and not a clock
	// instant, because the backend reports it and no event of this
	// deployment produced it.
	//
	// The Info that [Store.Put] returns MAY have a zero ModTime, because
	// the S3 family does not report a modification time on a write.
	// [Store.Get] and [Store.Stat] MUST report it.
	ModTime time.Time

	// ContentType is the content type that [Store.Put] stored, as given.
	// No store infers or normalises it, and the empty string means
	// unspecified. A store that writers outside the seam also fill can
	// return a content type that [ValidContentType] rejects.
	ContentType string

	// Size is the length of the body in bytes, or -1 when the backend
	// cannot report it without reading the body. The Info that
	// [Store.Put] returns never has -1, because the store read the body.
	Size int64
}

// PutOptions are the metadata and the preconditions of a Put.
//
// # Allocation contract
//
// Value type; pass by value.
type PutOptions struct {
	// ContentType is stored with the object, and [Store.Get] and
	// [Store.Stat] return it. It must satisfy [ValidContentType]. The
	// empty string means unspecified.
	ContentType string

	// Write is the precondition of a conditional write. The zero value
	// overwrites unconditionally. A failed IfMatch returns
	// [version.ErrMismatch], and a failed IfNoneMatch returns
	// [version.ErrExists].
	Write version.WriteOptions
}

// Store is named object storage.
//
// # Keys
//
// A key satisfies [ValidKey]. Every method classifies a key that does not
// as [go.thesmos.sh/core/errs.Invalid], and returns before it touches
// storage.
//
// A store may contain "a/b" and "a/b/c" at once, because the object
// stores behind the seam do. A backend whose namespace cannot, such as a
// filesystem, where a file cannot contain a file, MUST encode its keys so
// that it can: for example, with a directory per key, or with a layout by
// a hash of the key. The contract cannot forbid the pair instead, because
// two concurrent writes of "a/b" and "a/b/c" would each pass their own
// check.
//
// The prefix of [Store.List] is a byte prefix and need not satisfy
// [ValidKey]. A prefix may end inside an element.
//
// # Content types
//
// [PutOptions.ContentType] satisfies [ValidContentType]. Put classifies a
// content type that does not as [go.thesmos.sh/core/errs.Invalid], and
// returns before it touches storage.
//
// # Context
//
// ctx cancels the call and bounds its duration. A method returns the
// context's error when ctx is already done, and abandons its work when
// ctx is done during the call. The seam does not read values from ctx. An
// implementation that reads one adds vocabulary that the seam does not
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
// Implementations must be safe for concurrent use. The backend orders
// concurrent writes to one key, and a conditional write makes that order
// matter to the caller.
type Store interface {
	// Put stores the object under key, and reads r to EOF.
	//
	// A Put is visible atomically, also when the upload is not. A Put
	// that fails for any reason, such as an error of r, cancellation or
	// a failed precondition, MUST leave the key as it was. A concurrent
	// Get MUST return the previous object or its absence, and never a
	// truncated body. The returned [Info] describes the object as
	// written, with its new Version.
	Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (Info, error)

	// Get opens the object for reading, and the caller closes the
	// reader. The reader returns the bytes of one object, the version
	// that the returned [Info] names, also when the key is overwritten
	// while the reader is open. Absence classifies as
	// [go.thesmos.sh/core/errs.NotFound].
	Get(ctx context.Context, key string) (io.ReadCloser, Info, error)

	// Stat returns metadata without the body. Absence classifies
	// as [go.thesmos.sh/core/errs.NotFound].
	Stat(ctx context.Context, key string) (Info, error)

	// Delete removes the object, subject to ifMatch.
	//
	// The zero [version.Version] deletes unconditionally, and an
	// unconditional Delete of an absent key succeeds. A non-zero ifMatch
	// that names any other version returns [version.ErrMismatch], also
	// for an absent key.
	//
	// Delete takes one version and not a [version.WriteOptions], because
	// a create-only precondition has no meaning for a removal.
	Delete(ctx context.Context, key string, ifMatch version.Version) error

	// List returns one page of the objects whose keys begin with prefix.
	// The empty prefix lists every object.
	//
	// The order is unspecified, but a cursor chain is complete. Over a
	// store without concurrent writes, a walk of the pages to the end
	// yields every matching object exactly once. Under concurrent
	// writes, a walk yields exactly once each object that exists for the
	// whole walk. An object created or deleted during the walk may or
	// may not appear.
	List(ctx context.Context, prefix string, p page.Page) (page.Cursor[Info], error)
}

// RangeReader is the optional capability of a [Store] that reads part of
// an object without transferring the rest of it.
//
// # Concurrency
//
// The same requirements as [Store]. Concurrent ReadRange calls on one
// key are safe, as parallel [io.ReaderAt.ReadAt] calls are.
type RangeReader interface {
	// ReadRange reads len(dst) bytes of the object under key, starting
	// at byte off, into dst. It returns the number of bytes read and the
	// [Info] of the version it read.
	//
	// The bytes come from one version of the object, the version the
	// returned Info names, even when the key is overwritten during the
	// call.
	//
	// A non-zero ifMatch makes the read conditional. When the stored
	// version is not ifMatch, ReadRange returns 0 and
	// [version.ErrMismatch]. The zero Version reads the stored version.
	//
	// The count and the error follow [bytes.Reader.ReadAt] over the
	// object's body:
	//
	//   - A range inside the object returns len(dst) and a nil error,
	//     also when the range ends at the last byte.
	//   - A range that ends past the end returns the bytes up to the
	//     end and [io.EOF].
	//   - An offset at or past the end returns 0 and io.EOF.
	//   - An empty dst at an offset inside the object returns 0 and a
	//     nil error.
	//
	// Each of these results returns the object's Info. Every other
	// error returns the zero Info.
	//
	// ReadRange may write to any byte of dst during the call, as
	// [io.ReaderAt] may. The bytes of dst past the returned count are
	// unspecified.
	//
	// Error modes:
	//
	//   - A negative off classifies as [go.thesmos.sh/core/errs.Invalid].
	//   - A key that [ValidKey] rejects classifies as
	//     [go.thesmos.sh/core/errs.Invalid].
	//   - An absent object classifies as
	//     [go.thesmos.sh/core/errs.NotFound], whatever ifMatch is.
	//   - A stored version other than a non-zero ifMatch returns
	//     [version.ErrMismatch].
	//   - A done ctx returns the context's error.
	ReadRange(ctx context.Context, key string, off int64, dst []byte, ifMatch version.Version) (int, Info, error)
}

// AsRangeReader returns the first [RangeReader] in the chain that starts
// at s and follows each decorator's Unwrap() Store, and reports whether
// it found one.
//
// A decorator that implements RangeReader itself is found before the
// store it wraps. A decorator's Unwrap must not return a value earlier
// in its own chain, or AsRangeReader does not return, as errors.As does
// not for a cyclic error chain.
//
// # Allocation contract
//
// Zero alloc.
func AsRangeReader(s Store) (RangeReader, bool) {
	for s != nil {
		if rr, ok := s.(RangeReader); ok {
			return rr, true
		}

		u, ok := s.(interface{ Unwrap() Store })
		if !ok {
			break
		}

		s = u.Unwrap()
	}

	return nil, false
}
