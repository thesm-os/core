// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package blob

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// MaxSignedLifetime is the longest lifetime of a signed request. A presigned
// URL of Amazon S3, a V4 signed URL of Cloud Storage and a user delegation SAS
// of Azure Blob Storage each have a lifetime of at most 7 days.
const MaxSignedLifetime = 7 * 24 * time.Hour

// MaxCreateSize is the size in bytes of the largest object that a signed
// create writes. Amazon S3 takes a body of at most 5 GB in one PUT.
const MaxCreateSize = 5_000_000_000

// The keys of the attributes of [SignedRequest.LogValue].
const (
	logMethod = "method"
	logURL    = "url"
	logExpiry = "expiry"
)

// SignedRequest is an HTTP request that a store signed for a client without
// the credentials of the store. The client sends Method to URL with each
// header of Header before Expiry.
//
// A SignedRequest is a bearer credential. Anyone with a copy can send it more
// than once until Expiry. The query of URL contains the signature and every
// credential of the request. String, GoString and LogValue return the
// method, the URL without its query and its fragment, and the expiry.
//
// # Allocation contract
//
// Value type; pass by value. String, GoString and LogValue allocate their
// text.
type SignedRequest struct {
	// Expiry is the time from which the backend refuses the request. The
	// backend compares Expiry with the time at which the request arrives.
	Expiry time.Time

	// Header contains the headers and the values that the client sends. The
	// Header of a request of SignGet has no entries. Header has no Host and
	// no Content-Length. An HTTP client sets both from the URL and the body.
	// A value of its type is assignable to the Header of an http.Request.
	Header map[string][]string

	// Method is GET for a request of SignGet and PUT for a request of
	// SignCreate.
	Method string

	// URL is the absolute URL of the request.
	URL string
}

// String returns the method, the URL without its query and its fragment, and
// the expiry in the layout of [time.RFC3339], each separated by a space.
//
// # Allocation contract
//
// One allocation, the text, for an expiry in the years 0 to 9999.
func (r SignedRequest) String() string {
	u := withoutQuery(r.URL)

	// The layout of RFC 3339 is as long as the longest text of an expiry in
	// the years 0 to 9999, so the text fits in an array on the stack.
	var stamp [len(time.RFC3339)]byte
	expiry := r.Expiry.AppendFormat(stamp[:0], time.RFC3339)

	var b strings.Builder
	b.Grow(len(r.Method) + len(u) + len(expiry) + 2)
	b.WriteString(r.Method)
	b.WriteByte(' ')
	b.WriteString(u)
	b.WriteByte(' ')
	b.Write(expiry)

	return b.String()
}

// GoString returns the text of String, so that the verb %#v of fmt does not
// print the query of the URL.
//
// # Allocation contract
//
// The allocation of String.
func (r SignedRequest) GoString() string {
	return r.String()
}

// LogValue returns a group of the method, the URL without its query and its
// fragment, and the expiry, under the keys method, url and expiry. A text
// handler and a JSON handler of log/slog then leave out the query.
//
// # Allocation contract
//
// One allocation, the attributes of the group.
func (r SignedRequest) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String(logMethod, r.Method),
		slog.String(logURL, withoutQuery(r.URL)),
		slog.Time(logExpiry, r.Expiry),
	)
}

// GetSigner is the optional capability of a [Store] that signs a request
// through which a client without the credentials of the store reads one
// object.
//
// A caller that gives a client its reads asserts for the capability with
// [AsGetSigner] when it wires the store, and refuses a store without it.
//
// # Time
//
// A GetSigner reads the time from the clock.Clock that its constructor
// takes. It measures the lifetime of a request from that time.
//
// # Concurrency
//
// The same requirements as [Store].
//
//dokimi:stub
//dokimi:out path=../coretest/blobtest/
type GetSigner interface {
	// SignGet returns a GET of the object under key that the backend serves
	// until expiry.
	//
	// SignGet does not read the object. The backend serves the object under
	// the key at the time that the request arrives. Its response has the
	// content type of the object, and the backend honours a Range header. The
	// backend responds with a status outside 2xx for a key without an object.
	//
	// Error modes:
	//
	//   - A key that [ValidKey] rejects classifies as
	//     [go.thesmos.sh/core/errs.Invalid].
	//   - An expiry less than one second after the time of the store
	//     classifies as errs.Invalid. So does an expiry more than
	//     [MaxSignedLifetime] after it.
	//   - A done ctx returns the context's error.
	SignGet(ctx context.Context, key string, expiry time.Time) (SignedRequest, error)
}

// AsGetSigner returns the first [GetSigner] in the chain that starts at s and
// follows each decorator's Unwrap() Store, and reports whether it found one.
//
// A decorator that implements GetSigner itself is found before the store it
// wraps. A decorator's Unwrap must not return a value earlier in its own
// chain, or AsGetSigner does not return, as errors.As does not for a cyclic
// error chain.
//
// # Allocation contract
//
// Zero alloc.
func AsGetSigner(s Store) (GetSigner, bool) {
	// A nil s matches neither assertion, so the loop ends at a nil store as
	// it ends at a store without Unwrap.
	for {
		if gs, ok := s.(GetSigner); ok {
			return gs, true
		}

		u, ok := s.(interface{ Unwrap() Store })
		if !ok {
			return nil, false
		}

		s = u.Unwrap()
	}
}

// CreateSigner is the optional capability of a [Store] that signs a request
// through which a client without the credentials of the store creates one
// object.
//
// A caller that gives a client its uploads asserts for the capability with
// [AsCreateSigner] when it wires the store, and refuses a store without it.
//
// A fenced Store does not implement CreateSigner. The backend writes the
// object when the request arrives, so the write cannot validate the fence of
// the store.
//
// # Time
//
// The same requirements as [GetSigner].
//
// # Concurrency
//
// The same requirements as [Store].
//
//dokimi:stub
//dokimi:out path=../coretest/blobtest/
type CreateSigner interface {
	// SignCreate returns a PUT that creates the object under key until
	// expiry. The body of the object has size bytes and the content type
	// contentType.
	//
	// The backend creates the object only when the key has no object at the
	// time that the request arrives. It refuses the request on a key with an
	// object, with a body of another size and with another content type. A
	// refused request leaves the key as it was. A created object is visible
	// atomically, as an object of [Store.Put] is.
	//
	// Error modes:
	//
	//   - The error modes of [GetSigner.SignGet].
	//   - An empty content type classifies as
	//     [go.thesmos.sh/core/errs.Invalid]. So does a content type that
	//     [ValidContentType] rejects.
	//   - A size below 1 or above [MaxCreateSize] classifies as errs.Invalid.
	SignCreate(ctx context.Context, key, contentType string, size int64, expiry time.Time) (SignedRequest, error)
}

// AsCreateSigner returns the first [CreateSigner] in the chain that starts at
// s and follows each decorator's Unwrap() Store, and reports whether it found
// one.
//
// A decorator that implements CreateSigner itself is found before the store
// it wraps. A decorator's Unwrap must not return a value earlier in its own
// chain, or AsCreateSigner does not return, as errors.As does not for a
// cyclic error chain.
//
// # Allocation contract
//
// Zero alloc.
func AsCreateSigner(s Store) (CreateSigner, bool) {
	// A nil s matches neither assertion, so the loop ends at a nil store as
	// it ends at a store without Unwrap.
	for {
		if cs, ok := s.(CreateSigner); ok {
			return cs, true
		}

		u, ok := s.(interface{ Unwrap() Store })
		if !ok {
			return nil, false
		}

		s = u.Unwrap()
	}
}

// withoutQuery returns u up to its query or its fragment, whichever comes
// first. The query of a signed URL contains its signature.
//
// # Allocation contract
//
// Zero alloc. The result is part of u.
func withoutQuery(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		return u[:i]
	}

	return u
}
