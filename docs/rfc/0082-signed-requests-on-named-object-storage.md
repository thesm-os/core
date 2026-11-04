---
rfc: 0082
title: Signed Requests on Named Object Storage
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-31
updated: 2026-10-31
discussion: https://github.com/thesm-os/core/issues/109
supersedes: none
superseded-by: ADR-0077
produces-adr: tbd
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0082: Signed Requests on Named Object Storage

## Summary

We propose two optional capabilities of a `blob.Store`:

- `blob.GetSigner` signs a GET of one object.
- `blob.CreateSigner` signs a PUT that creates one object of an exact size
  and content type. The backend refuses the PUT when the key has an object.

A client without the credentials of the store sends the signed request to
the backend, so the body does not pass through the process.
`blob.AsGetSigner` and `blob.AsCreateSigner` find each capability behind
decorators. The query of the URL of a `blob.SignedRequest` contains the
signature, and the text forms of the type leave the query out. `blob/memory`
gains a `Server` that implements both capabilities, and `coretest/blobtest`
gains their suites and doubles. `blob.Store` does not change.

## Motivation

### Every body passes through the process

`blob.Store` moves every body through the process that has the credentials
of the store:

- An application that serves a document of 50 MB to a browser reads 50 MB
  from the store and writes the same 50 MB to the browser. An upload of the
  same size takes the reverse path.
- `httpserver` limits a request body to 4 MiB by default
  (`WithMaxBodyBytes`). An upload through the application needs a higher
  limit on every replica.
- The replicas spend memory and bandwidth on bytes that they never inspect.

### The seam has no capability for the signed requests of the backends

Amazon S3 signs presigned URLs, Cloud Storage signs V4 signed URLs and Azure
Blob Storage signs shared access signatures. A caller signs with the SDK of
its backend today:

- The caller keeps the client of the backend beside the store, so a test
  cannot replace both with `blob/memory`.
- The signed request bypasses `ValidKey` and `ValidContentType`.
- A signed PUT replaces an existing object. The S3 user guide states: "If an
  object with the same key already exists in the bucket as specified in the
  presigned URL, Amazon S3 replaces the existing object with the uploaded
  object." The caller adds the precondition of each backend itself.

### A signed request is a bearer credential

The S3 user guide states that "presigned URLs are bearer tokens that grant
access to those who possess them". A holder can use a presigned URL
"multiple times, up to the expiration date and time". A signed PUT without a
bound on its size lets its holder write the largest body of one PUT, 5 GB on
S3. A signed PUT without a bound on its content type lets its holder choose
how a browser renders the object.

The Go CDK v0.47.0 signs a URL with `Bucket.SignedURL`. The method returns a
URL without headers. Its signed PUT has no precondition and no bound on the
size. Its S3 driver returns `Unimplemented` for a signed PUT with a content
type.

## Detailed design

### Components

| Component | Responsibility |
|---|---|
| `blob.GetSigner` | Signs a GET of the object under a key |
| `blob.CreateSigner` | Signs a PUT that creates the object under a key with an exact size and content type |
| `blob.SignedRequest` | The method, the URL, the headers and the expiry of a signed request |
| `blob.AsGetSigner`, `blob.AsCreateSigner` | Find a capability behind decorators through `Unwrap() Store` |
| `blob.MaxSignedLifetime` | The longest lifetime of a signed request |
| `blob.MaxCreateSize` | The largest object of a signed create |
| `memory.Server` | A `memory.Store` that serves its objects over HTTP and signs requests for them |
| `blobtest` | The suites and the doubles of both capabilities, and the `Backend` of a store under test |

### Invariants

1. A signed request permits one method on one key. The backend refuses the
   request after a change of its method, its key or its query.
2. A request of `SignCreate` creates the object only when the key has no
   object at the time that the request arrives. The backend refuses the
   request on a key with an object and leaves that object as it was.
3. A request of `SignCreate` creates an object of the signed size and
   content type. The backend refuses a body of another size or another
   content type and leaves the key absent.
4. The backend refuses a request that arrives after its `Expiry`. `Expiry`
   is at or before the expiry of the call and at most `MaxSignedLifetime`
   after the time of the store.
5. A signed request addresses only a key that `ValidKey` accepts. The
   content type of a create is not empty, and `ValidContentType` accepts it.
6. A created object is visible atomically, as an object of `Put` is.
7. The text forms of a `SignedRequest` do not contain a byte of the query of
   its URL. Every backend puts the signature in the query.
8. A request of `SignGet` has no headers, so a client can use its URL as a
   link.

### The capabilities

```go
package blob

// MaxSignedLifetime is the longest lifetime of a signed request. A presigned
// URL of Amazon S3, a V4 signed URL of Cloud Storage and a user delegation SAS
// of Azure Blob Storage each have a lifetime of at most 7 days.
const MaxSignedLifetime = 7 * 24 * time.Hour

// MaxCreateSize is the size in bytes of the largest object that a signed
// create writes. Amazon S3 takes a body of at most 5 GB in one PUT.
const MaxCreateSize = 5_000_000_000

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

func (r SignedRequest) String() string
func (r SignedRequest) GoString() string
func (r SignedRequest) LogValue() slog.Value

// GetSigner is the optional capability of a Store that signs a request
// through which a client without the credentials of the store reads one
// object.
//
// A caller that gives a client its reads asserts for the capability with
// AsGetSigner when it wires the store, and refuses a store without it.
//
// # Time
//
// A GetSigner reads the time from the clock.Clock that its constructor
// takes. It measures the lifetime of a request from that time.
//
// # Concurrency
//
// The same requirements as Store.
//
//dokimi:stub
//dokimi:suite
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
	//   - A key that ValidKey rejects classifies as errs.Invalid.
	//   - An expiry less than one second after the time of the store
	//     classifies as errs.Invalid. So does an expiry more than
	//     MaxSignedLifetime after it.
	//   - A done ctx returns the context's error.
	SignGet(ctx context.Context, key string, expiry time.Time) (SignedRequest, error)
}

// CreateSigner is the optional capability of a Store that signs a request
// through which a client without the credentials of the store creates one
// object.
//
// A caller that gives a client its uploads asserts for the capability with
// AsCreateSigner when it wires the store, and refuses a store without it.
//
// A fenced Store does not implement CreateSigner. The backend writes the
// object when the request arrives, so the write cannot validate the fence of
// the store.
//
// # Time
//
// The same requirements as GetSigner.
//
// # Concurrency
//
// The same requirements as Store.
//
//dokimi:stub
//dokimi:suite
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
	// atomically, as an object of Put is.
	//
	// Error modes:
	//
	//   - The error modes of SignGet.
	//   - An empty content type classifies as errs.Invalid. So does a content
	//     type that ValidContentType rejects.
	//   - A size below 1 or above MaxCreateSize classifies as errs.Invalid.
	SignCreate(ctx context.Context, key, contentType string, size int64, expiry time.Time) (SignedRequest, error)
}

// AsGetSigner returns the first GetSigner in the chain that starts at s and
// follows each decorator's Unwrap() Store, and reports whether it found one.
// A decorator's Unwrap must not return a value earlier in its own chain.
//
// # Allocation contract
//
// Zero alloc.
func AsGetSigner(s Store) (GetSigner, bool)

// AsCreateSigner returns the first CreateSigner in the chain that starts at
// s and follows each decorator's Unwrap() Store, and reports whether it
// found one. A decorator's Unwrap must not return a value earlier in its own
// chain.
//
// # Allocation contract
//
// Zero alloc.
func AsCreateSigner(s Store) (CreateSigner, bool)
```

An application that receives an upload wires the capability once and signs
one request for each upload:

```go
cs, ok := blob.AsCreateSigner(store)
if !ok {
	return errNeedsSignedCreates
}

// size and contentType come from the client, and the application checks
// both against its own rules before it signs.
req, err := cs.SignCreate(ctx, key, contentType, size, clk.Time().Add(10*time.Minute))
if err != nil {
	return err
}

// The application returns req to the client. The client sends req.Method
// to req.URL with each header of req.Header and a body of size bytes.
```

### Decorators

The section Decorators of the package documentation of `blob` becomes:

> A decorator that wraps a Store, for example to add tracing, implements
> Unwrap() Store and returns the store it wraps. AsRangeReader, AsGetSigner
> and AsCreateSigner follow Unwrap through any number of decorators.
>
> Each capability that an As function finds behind a decorator bypasses the
> decorator. ReadRange calls the store that the decorator wraps, and the
> backend serves a signed request without the process. A decorator does not
> implement Unwrap when it changes the bytes that a store keeps or returns,
> or which writes a store admits. An encrypting decorator and a quota are
> examples.

ADR-0022 states the convention for decorators that add tracing or metrics. A
decorator of that kind does not change a body and admits every write. A
signed request that bypasses it does not bypass a rule of the store.

### The backends

| Backend | `SignGet` | `SignCreate` |
|---|---|---|
| Amazon S3 | `PresignGetObject` of aws-sdk-go-v2 | `PresignPutObject` with `IfNoneMatch` `*`, `ContentType` and `ContentLength` |
| Cloud Storage | A V4 signed URL of GET | A V4 signed URL of PUT with `ContentType` and the headers `x-goog-if-generation-match:0` and `x-goog-content-length-range:<size>,<size>` |
| Azure Blob Storage | A user delegation SAS with the permission `r` | None |
| `blob/memory` | `Server.SignGet` | `Server.SignCreate` |

- **Amazon S3.** A PUT with `If-None-Match: *` fails with
  `412 Precondition Failed` when the key has an object. The signer of
  aws-sdk-go-v2 signs each header of the request outside a list of five
  ignored headers, so the presigned PUT signs `if-none-match` and
  `content-type`. It signs `content-length` for a length above 0.
- **Cloud Storage.** A generation-match precondition of `0` lets a request
  proceed "only if no object with the specified name exists in the bucket or
  if there are only noncurrent versions of the object in the bucket".
  `x-goog-content-length-range` refuses a body outside its range with
  `400 Bad Request`. The Go client takes the expiry as a `time.Time`. It
  computes the lifetime from its own reading of the system clock. A store
  over Cloud Storage must read the same system clock.
- **Azure Blob Storage.** The permission `c` of a SAS permits a client to
  "write a new blob". A SAS has no field that bounds the size or the content
  type of a write. Its field `rsct` sets the content type of a response. A
  store over Azure implements `GetSigner` alone. A user delegation key is
  valid "for a period of up to seven days". A SAS is valid only within the
  interval of its key.

### The lifetime of a request

- `SignGet` and `SignCreate` take the expiry as a `time.Time`. The client and
  the records of the caller receive the same instant.
- The store measures the lifetime from the time of its clock. Every backend
  signs whole seconds. The shortest lifetime is one second, and the longest
  is `MaxSignedLifetime`.
- The seam has no default lifetime. The zero `time.Time` is more than a
  second before the time of any store, so a call without an expiry
  classifies as Invalid.

The store rounds the lifetime down to whole seconds, so `Expiry` can be
earlier than the expiry of the call. The credentials of the store can end the
request earlier still. A presigned URL of S3 "expires when the role session
expires, even if you specify a longer expiration time". A store over
temporary credentials returns the end of its session when the session ends
first. A caller that needs a lifetime of some duration compares `Expiry` with
that duration.

Amazon S3 compares the expiry with the time at which a request arrives. A
download that starts before the expiry "continues even if the expiration
time passes during the download".

### The size and the content type of a create

- `SignCreate` takes the exact size of the body. S3 signs one
  `Content-Length`, so a range of sizes has no form on S3. Cloud Storage
  signs a range whose two bounds are the size. A client that uploads a file
  knows its size.
- The size is at least 1. The S3 signer does not sign `content-length` for a
  length of 0, and `PresignPutObject` removes the content type of a request
  with a length of 0. On S3 the signature of an empty create would not
  cover its size or its content type. A caller creates an empty object
  with `Put`, because an empty body does not need a transfer by a client.
- `MaxCreateSize` is 5,000,000,000 bytes. The S3 user guide states that one
  PUT uploads "a single object up to 5 GB in size". We take 5 GB in decimal
  bytes, because a binary reading of the unit would be larger. A larger
  object needs a multipart upload (alternative K).
- The content type is not empty. A client that chooses the content type of
  an object chooses how a browser renders the object when a later `SignGet`
  serves it. A client could choose `text/html` and make the browser render
  the body as a page.

### `blob/memory`

```go
package memory

// Server is a Store that serves its objects over HTTP at a base URL and
// signs requests for them. A developer mounts it in the HTTP server of an
// application, and the suites of GetSigner and CreateSigner run against it.
//
// crypto.Framer frames the method, the key, the expiry, the Content-Length
// and the Content-Type of a request. The signature of the request is the
// HMAC-SHA-256 of the frame. NewServer reads the key of the HMAC from
// crypto/rand, so a request is valid only while its Server exists.
//
// # Concurrency
//
// Safe for concurrent use, as a Store is.
type Server struct {
	*Store

	// ...unexported fields
}

// NewServer returns a Server over s whose requests address base.
//
// Error modes: a nil s classifies as errs.Invalid. So does a base without a
// scheme or a host, and a base with a query or a fragment.
func NewServer(s *Store, base *url.URL) (*Server, error)

// SignGet returns a GET of the object under key until expiry.
func (srv *Server) SignGet(ctx context.Context, key string, expiry time.Time) (blob.SignedRequest, error)

// SignCreate returns a PUT that creates the object under key until expiry.
func (srv *Server) SignCreate(ctx context.Context, key, contentType string, size int64, expiry time.Time) (blob.SignedRequest, error)

// ServeHTTP serves the signed requests under the path of base.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request)
```

- The URL of a request is the base URL with the escaped key at the end of
  its path. Its query contains the expiry in Unix seconds and the signature.
- `ServeHTTP` rebuilds the signed fields from the request: the method, the
  key of the path, the expiry of the query, and the `Content-Length` and the
  `Content-Type` of a PUT. `MAC.Verify` compares the signature in constant
  time. `ServeHTTP` then compares the expiry with the time of the clock of
  the Store.
- A PUT calls `Put` with `IfNoneMatch` set to `version.Wildcard`, so `Put`
  refuses the PUT of a key with an object.
- A test builds the base URL from the listener of
  `httptest.NewUnstartedServer`, and then starts the server with the Server
  as its handler.

| Request | Response |
|---|---|
| A GET of a key with an object | 200, or 206 for a Range header, from `http.ServeContent`, with the content type, the modification time and an ETag of the version |
| A GET of a key without an object | 404 |
| A PUT of a key without an object | 201 |
| A PUT of a key with an object | 412, and the object as it was |
| A PUT whose body ends before its `Content-Length` | 400, and the key as it was |
| A request whose signature fails | 403 |
| A PUT with another `Content-Length` or another `Content-Type` than the signed ones | 403, because the signature covers both |
| A request after its expiry | 403 |
| A method other than GET and PUT | 405 |

### Conformance

dokimi generates `RunGetSignerSuite`, `RunCreateSignerSuite` and the doubles
`GetSignerStub` and `CreateSignerStub` into `coretest/blobtest` from the
directives. The subject of each check is a `blob.Store` too, because the
check writes and reads the objects of its requests. A check of `RangeReader`
does the same.

A check sends each signed request to the backend and compares the result
with the state of the store. The test of the store supplies the client of
the backend, the clock of the store and the passage of time on the backend
through a `Backend`:

```go
package blobtest

// Backend is the side of a store under test that serves its signed
// requests.
type Backend interface {
	// Do sends a signed request to the backend of the store.
	Do(req *http.Request) (*http.Response, error)

	// Time returns the time of the clock of the store.
	Time() time.Time

	// Elapse returns once d has elapsed on the clock of the backend. It
	// returns the error of ctx when ctx ends first.
	Elapse(ctx context.Context, d time.Duration) error
}
```

- The `Backend` of the test of `memory.Server` sends requests to an httptest
  server. Its `Time` reads the fake clock of the Store, and its `Elapse`
  advances that clock.
- The `Backend` of a store over S3 sends requests with a client of its own
  and reads the system clock. Its `Elapse` calls `clock.Wait`.
- A check receives the `Backend` of its subject from a door of conform. A
  door is a method of the handle that only the code that builds a subject
  can supply. conform v0.8.0 has the doors `Reference`, `Second`, `Restart`
  and `Crash`, and none returns a value of another type than the subject.
  The test of a store passes the door as an option, and a planted defect
  gets its door from the directive of its check, as a restart does. The
  build starts with requests for the door in dokimasia/dokimi-go, for
  conform, and in dokimasia/dokimi, for the directive. It waits for their
  releases.

The checks of `GetSigner` are:

- A request of `SignGet` returns the body and the content type that `Put`
  stored. The request has no headers, and its `Expiry` is at or before the
  expiry of the call.
- A request with a Range header returns the bytes of the range.
- The backend refuses the request with another method, under another key
  and without its query.
- The backend refuses the request after its `Expiry`. The check signs a
  lifetime of one second and lets two seconds elapse.
- An invalid key classifies as Invalid. So do a lifetime below one second
  and a lifetime above `MaxSignedLifetime`.
- A done context returns the context's error.

The checks of `CreateSigner` are:

- A request of `SignCreate` creates the object with the body, the size and
  the content type of the call.
- The backend refuses the request a second time. It also refuses the
  request on a key with an object. Both leave the object as it was.
- The backend refuses a body of another size and a body with another
  content type, and the key is absent after each.
- The backend refuses the request with another method, under another key
  and without its query.
- The backend refuses the request after its `Expiry`.
- The invalid calls of `SignGet` classify as Invalid. So do an empty content
  type, a content type that `ValidContentType` rejects, a size of 0 and a
  size above `MaxCreateSize`.
- A done context returns the context's error.

Each check has a planted defect over `memory.Server`. The test of the suites
runs each check on every defect, as the test of the suite of `Store` does.

### Bounds

| Quantity | Bound |
|---|---|
| The lifetime of a signed request | 1 s to `MaxSignedLifetime`, 7 days |
| The size of a signed create | 1 byte to `MaxCreateSize`, 5,000,000,000 bytes |
| The reads of an object by a signing call | 0 |
| The uses of a request of `SignGet` | Any number until `Expiry` |
| The objects that a request of `SignCreate` creates | At most 1 |
| The size of a program that imports `blob` | 274,432 bytes more for the `log/slog` of `LogValue` |

The last row compares two stripped builds with Go 1.27.3. A program that
imports `blob` measured 1,523,872 bytes and 71 packages. A program that also
declares a type with a `LogValue` method measured 1,798,304 bytes and 87
packages.

### Failure handling

| Condition | Call | Result |
|---|---|---|
| A key that `ValidKey` rejects | `SignGet`, `SignCreate` | `errs.Invalid` |
| A lifetime below one second or above `MaxSignedLifetime` | `SignGet`, `SignCreate` | `errs.Invalid` |
| An empty content type, or one that `ValidContentType` rejects | `SignCreate` | `errs.Invalid` |
| A size below 1 or above `MaxCreateSize` | `SignCreate` | `errs.Invalid` |
| A done context | `SignGet`, `SignCreate` | The context's error |
| A request after its `Expiry` | The backend | A refusal |
| A request with another method, key or query | The backend | A refusal |
| A create on a key with an object | The backend | A refusal. The object is as it was |
| A body of another size or another content type | The backend | A refusal. The key is absent |
| A GET of a key without an object | The backend | A status outside 2xx |
| A nil store, or a base URL that is not absolute | `memory.NewServer` | `errs.Invalid` |

### Allocation contract

| Call | Objects |
|---|---|
| `AsGetSigner`, `AsCreateSigner` | 0 |
| `SignedRequest.String`, `SignedRequest.GoString` | The text |
| `SignedRequest.LogValue` | The attributes of its group |
| `Server.SignGet`, `Server.SignCreate` | The URL, and the `Header` of a create |

The tests pin the count of each call that a profile measures, and the
benchmarks state the same ceilings.

### Tests outside the suites

| # | Guarantee |
|---|---|
| 1 | `AsGetSigner` and `AsCreateSigner` find the capability on the store, behind one decorator and behind two. They report false for a store without it and for a decorator without `Unwrap` |
| 2 | `String`, `GoString` and `LogValue` return the method, the URL without its query and the expiry. A text handler and a JSON handler of `log/slog` do not write a byte of the query |
| 3 | `memory.Server` passes `RunGetSignerSuite` and `RunCreateSignerSuite`. Each check fails on its planted defect |
| 4 | `memory.Server` refuses a request after a change of each signed field. It refuses a request of another Server |
| 5 | `memory.NewServer` refuses a nil store and each kind of base URL that is not absolute |
| 6 | The allocation tests and the benchmarks state the counts of the allocation contract |
| 7 | The suites pass under the race detector and cover every statement. dokimi-mutate-go detects every mutant of `blob` and `blob/memory` |

### Migration

None. The capabilities are additive. Every existing `Store` compiles and
behaves as before.

## Alternatives considered

### A. One `Signer` with both methods

`blob.Signer` would have `SignGet` and `SignCreate`, as issue 109 proposes. A
store over Azure would return `errs.Unsupported` from every `SignCreate`.

**Why not:** a caller that needs signed creates would find the gap at its
first upload, and not when it wires the store. The suite could not require
`SignCreate` of a store with `Signer` without an option that excuses the
checks. With two capabilities, `AsCreateSigner` refuses a store over Azure
when the caller wires it. `AsRangeReader` refuses a store without ranged
reads in the same way.

### B. A create without a size

`SignCreate` would bind the key and the precondition alone. `Bucket.SignedURL`
of the Go CDK binds a PUT in this way.

**Why not:** the request would let its holder write the largest body of one
PUT, 5 GB on S3. The S3 presigner also removes the content type of a request
without a length, so the content type would have no bound either.

### C. A maximum size

`SignCreate` would take the largest size of the body, and the client would
send any body up to it.

**Why not:** S3 signs one `Content-Length`, so a store over S3 could not
bound a body by a maximum. The exact size is the bound that S3 and Cloud
Storage share.

### D. A lifetime as a duration

`SignGet` would take a `time.Duration`. `SignedURLOptions.Expiry` of the Go
CDK is one.

**Why not:** a duration starts at a time that the caller does not see. The
caller gives an instant to the client anyway, and `SignedRequest.Expiry` is
that instant.

### E. `http.Header` for `Header`

`SignedRequest.Header` would be an `http.Header`, as issue 109 proposes.

**Why not:** `blob` would import `net/http`. A program that imports `blob`
and declares a struct with a field of `http.Header` measured 3,604,743 bytes
and 197 packages, against 1,523,872 bytes and 71 packages without the field.
Both builds were stripped and used Go 1.27.3. A `map[string][]string` is
assignable to the `Header` of an `http.Request` without a conversion.

### F. A GET of one version

`SignGet` would take a version, and the backend would serve that version
alone.

**Why not:** a version in a header makes the URL unusable as a link, because
a browser does not send a header of the caller with a link. A version in the
query needs an identifier of the backend, such as the version ID of S3 or
the generation of Cloud Storage. A `Version` of the seam need not be either.
A caller that serves only the version that it checked writes each version
under a key of its own.

### G. A signed DELETE

`Bucket.SignedURL` of the Go CDK signs a DELETE.

**Why not:** a deletion does not transfer a body, so the process deletes the
object itself with one request. The Cloud Storage documentation states that for a
deletion, "creating a signed URL and giving it to someone to use is an
unnecessary extra step".

### H. A call that revokes a request

`GetSigner` and `CreateSigner` would have a method that revokes one signed
request before its `Expiry`.

**Why not:** no backend revokes one signed request. A presigned URL of S3
"expires when the credential you used to create it is revoked, deleted, or
deactivated". A signed URL of Cloud Storage is valid until "the key used to
sign the URL is rotated". Each revokes every request of a credential at
once. A caller that must end a request early chooses a short lifetime.

### I. A file name for a download

`SignGet` would take the `Content-Disposition` of the response, so that a
browser saves the object under a file name. Cloud Storage takes
`response-content-disposition` in the query of a signed URL, and Azure takes
the field `rscd` of a SAS.

**Why not:** the seam does not keep a presentation of an object, and `Info`
has no field for one. The verdict changes when a caller must serve a
download under a file name that differs from its key.

### J. Methods on `Store`

`Store` would have `SignGet` and `SignCreate`.

**Why not:** the change breaks every `Store` outside core for a need of one
class of callers. A `memory.Store` without a server has no HTTP endpoint,
and a store over a filesystem has none either.

### K. A signed multipart or resumable upload

A capability would sign the requests of an upload above `MaxCreateSize`.

**Why not:** a multipart upload of S3 needs a signed request for each part
and a call by the process that completes the upload. A resumable upload of
Cloud Storage starts with a session URI that "the server can initiate".
Each is a capability with laws of its own. A body up to `MaxCreateSize`
takes one signed PUT.

## Drawbacks

- `blob` gains seven exported names: `GetSigner`, `CreateSigner`,
  `SignedRequest`, `AsGetSigner`, `AsCreateSigner`, `MaxSignedLifetime` and
  `MaxCreateSize`. Its import of `log/slog` adds 274,432 bytes and 16
  packages to a program that imports `blob`.
- A store over Azure implements `GetSigner` alone.
- The process does not see the transfer of a signed request, so its
  telemetry does not record the transfer. The access logs of the backend
  record it.
- A browser sends the headers of a create in a cross-origin request. The
  CORS configuration of the bucket must allow `Content-Type`, and
  `If-None-Match` on S3 or the two `x-goog-` headers on Cloud Storage.
- The signature of a create covers the headers in `Header` alone. A client
  can add another header to the request. A backend that keeps such a header
  with the object returns it in the response of a later `SignGet`, and
  `Info` does not report it. `Content-Disposition` is one example.
- A caller that needs a lifetime of some duration compares `Expiry`, because
  the role credentials of S3 can end a request early.
- A client cannot create an empty object.
- A decorator that changes the bytes or the admitted writes of a store hides
  `RangeReader` too.
- The suites need a door that conform v0.8.0 does not have.
- The check of the lifetime waits two seconds of real time for a store over
  a real backend.

## Open questions

None.

## Unresolved / future work

None.

## References

- Issue 109, <https://github.com/thesm-os/core/issues/109>.
- RFC-0028, named object storage.
- RFC-0038, conformance for durable adapters and decorators.
- RFC-0040, ranged reads on named object storage.
- RFC-0080, test generation with dokimi.
- ADR-0022, decorators expose what they wrap.
- dokimasia/dokimi RFC-0008, the doors of a subject and of a planted defect,
  <https://github.com/dokimasia/dokimi/blob/main/docs/rfc/0008-the-doors-of-a-subject-and-of-a-planted-defect.md>.
- conform v0.8.0, `handle.go` (`Handle`, `Doors`),
  <https://github.com/dokimasia/dokimi-go/tree/conform/v0.8.0/conform>.
- Amazon S3 User Guide, "Download and upload objects with presigned URLs",
  <https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-presigned-url.html>.
- Amazon S3 User Guide, "How to prevent object overwrites with conditional
  writes",
  <https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html>.
- Amazon S3 User Guide, "Uploading objects",
  <https://docs.aws.amazon.com/AmazonS3/latest/userguide/upload-objects.html>.
- aws-sdk-go-v2 v1.47.5: `aws/signer/v4/v4.go` (`buildCanonicalHeaders`),
  `aws/signer/internal/v4/headers.go` (`IgnoredHeaders`) and
  `aws/transport/http/content_type.go` (`RemoveContentTypeHeader`),
  <https://github.com/aws/aws-sdk-go-v2/tree/v1.47.5>.
- aws-sdk-go-v2 `service/s3` v1.114.6: `api_op_PutObject.go`
  (`PutObjectInput.IfNoneMatch`, `PresignPutObject`),
  <https://github.com/aws/aws-sdk-go-v2/tree/service/s3/v1.114.6/service/s3>.
- Cloud Storage, "Signed URLs",
  <https://cloud.google.com/storage/docs/access-control/signed-urls>.
- Cloud Storage, "Request preconditions",
  <https://cloud.google.com/storage/docs/request-preconditions>.
- Cloud Storage, "HTTP headers and query string parameters for XML API",
  <https://cloud.google.com/storage/docs/xml-api/reference-headers>.
- `cloud.google.com/go/storage` v1.70.0: `storage.go`
  (`SignedURLOptions`, `validateOptions`, `utcNow`),
  <https://github.com/googleapis/google-cloud-go/blob/storage/v1.70.0/storage/storage.go>.
- Azure Storage, "Create a user delegation SAS",
  <https://learn.microsoft.com/en-us/rest/api/storageservices/create-user-delegation-sas>.
- Go CDK v0.47.0: `blob/blob.go` (`Bucket.SignedURL`, `SignedURLOptions`),
  `blob/s3blob/s3blob.go` (`SignedURL`), `blob/fileblob/fileblob.go`
  (`URLSignerHMAC`) and `blob/drivertest/drivertest.go` (`testSignedURL`),
  <https://github.com/google/go-cloud/tree/v0.47.0/blob>.
