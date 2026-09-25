---
rfc: 0040
title: Ranged Reads on Named Object Storage
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-09-25
updated: 2026-09-25
discussion: none
supersedes: none
superseded-by: none
produces-adr: tbd
---

# RFC-0040: Ranged Reads on Named Object Storage

## Summary

We propose an optional capability `blob.RangeReader` that reads a byte
range of an object without transferring the rest. `blob.AsRangeReader`
finds the capability behind decorators, and a `blob.Store` decorator
gains the method `Unwrap() Store` for it. A read can require a version
of the object, so a caller that reads two or more ranges reads them
from one version. `blob/memory` implements the capability.
`coretest/blobtest` runs the cases of the capability on a store that
implements it, and its option `WithRangeReader` fails a store without
it. `blob.Store` does not change.

## Motivation

`Store.Get` opens the whole object. A caller that needs part of an
object transfers the whole object:

- A reader of one 64 KiB chunk of a 50 MB sealed object transfers
  50 MB, 800 times the bytes it uses.
- An object whose index is at a known offset, such as the trailer of an
  archive, costs a full transfer before the index can be read.
- The object stores this seam fronts read ranges natively. Amazon S3
  `GetObject` downloads one byte range per request through the HTTP
  `Range` header, and Google Cloud Storage `objects.get` accepts the
  same header. An adapter over either store serves a range at the cost
  of the range.

`blob.Store` has no way to express the need. A caller that reads
ranges declares its own interface beside the seam, and each adapter
implements a private spelling of the same operation.

## Detailed design

### The capability

```go
// RangeReader is the optional capability of a Store that reads part of
// an object without transferring the rest of it.
//
// A caller that needs ranged reads asserts for the capability with
// AsRangeReader when it wires the store, and refuses a store without
// it.
//
// # Concurrency
//
// The same requirements as Store. Concurrent ReadRange calls on one key
// are safe, as parallel ReadAt calls are.
type RangeReader interface {
    // ReadRange reads len(dst) bytes of the object under key, starting
    // at byte off, into dst. It returns the number of bytes read and the
    // Info of the version it read.
    //
    // The bytes come from one version of the object, the version the
    // returned Info names, even when the key is overwritten during the
    // call.
    //
    // A non-zero ifMatch makes the read conditional. When the stored
    // version is not ifMatch, ReadRange returns 0 and
    // version.ErrMismatch. The zero Version reads the stored version.
    //
    // The count and the error follow bytes.Reader.ReadAt over the
    // object's body:
    //
    //   - A range inside the object returns len(dst) and a nil error,
    //     also when the range ends at the last byte.
    //   - A range that ends past the end returns the bytes up to the
    //     end and io.EOF.
    //   - An offset at or past the end returns 0 and io.EOF.
    //   - An empty dst at an offset inside the object returns 0 and a
    //     nil error.
    //
    // Each of these results returns the object's Info. Every other
    // error returns the zero Info.
    //
    // ReadRange may write to any byte of dst during the call, as
    // io.ReaderAt may. The bytes of dst past the returned count are
    // unspecified.
    //
    // Error modes:
    //
    //   - A negative off classifies as errs.Invalid.
    //   - A key that ValidKey rejects classifies as errs.Invalid.
    //   - An absent object classifies as errs.NotFound, whatever
    //     ifMatch is.
    //   - A stored version other than a non-zero ifMatch returns
    //     version.ErrMismatch.
    //   - A done ctx returns the context's error.
    ReadRange(ctx context.Context, key string, off int64, dst []byte, ifMatch version.Version) (int, Info, error)
}

// AsRangeReader returns the first RangeReader in the chain that starts
// at s and follows each decorator's Unwrap() Store, and reports whether
// it found one.
//
// A decorator that implements RangeReader itself is found before the
// store it wraps. A decorator's Unwrap must not return a value earlier
// in its own chain.
//
// # Allocation contract
//
// Zero alloc.
func AsRangeReader(s Store) (RangeReader, bool)
```

The rules for the count and the error are the rules of
`bytes.Reader.ReadAt`, stated for an object. They also match HTTP: a
range is satisfiable only when its first byte is inside the object, and
a range that runs past the end is served up to the end. An adapter over
an HTTP backend maps a 416 (Range Not Satisfiable) response to 0 and
`io.EOF`.

### Reading two or more ranges of one version

A caller that reads two or more ranges of one version passes the
version from its first read as `ifMatch` to every later read. The store
refuses a read of any other version with `version.ErrMismatch`, and the
caller starts again from the first range. Without the precondition, an
overwrite between two reads returns ranges of two versions. The caller
would detect the overwrite only by comparing `Info.Version` after each
read.

HTTP defines the same use of `If-Match`. A client sends it on a range
request to complete a representation it has partly received, and it
receives 412 (Precondition Failed) when the representation no longer
matches. S3 `GetObject` evaluates `If-Match`, and Cloud Storage
`objects.get` evaluates `ifGenerationMatch`, so both backends check the
condition in the read request itself. An adapter whose `Version` is not
the backend's condition token compares the version it read with
`ifMatch` before it returns. A mismatch then costs the transfer of one
range.

A caller allocates one chunk buffer and reuses it for every read:

```go
const chunkSize = 64 << 10

rr, ok := blob.AsRangeReader(store)
if !ok {
    return errNeedsRanges
}

buf := make([]byte, chunkSize)
pinned := version.Unspecified

for i := first; i <= last; i++ {
    n, info, err := rr.ReadRange(ctx, key, i*chunkSize, buf, pinned)
    if err != nil && !errors.Is(err, io.EOF) {
        return err // version.ErrMismatch when the object changed after the first read
    }
    if err := use(buf[:n]); err != nil {
        return err
    }
    pinned = info.Version
}
```

### An absent object

`ReadRange` classifies an absent object as NotFound, also when
`ifMatch` is not zero:

- RFC 9110 requires an HTTP server to ignore every precondition when
  its response without them would be neither 2xx nor 412. A GET of a
  missing resource is a 404 whatever `If-Match` names, and an adapter
  maps it without a second request.
- `Get` and `Stat` classify absence as NotFound. A reader handles a
  deleted object the same way on every read path.

The conditional writes differ. `Put` with `IfMatch` and `Delete` with a
non-zero `ifMatch` return `version.ErrMismatch` for an absent key,
because a conditional write fails whenever the stored object is not the
version it names.

### Decorators

A decorator that wraps a `Store`, for example to add tracing,
implements `Unwrap() Store` and returns the store it wraps.
`AsRangeReader` follows `Unwrap` through any number of decorators, as
`cas.AsStreamer` does for `cas.Store`. The `blob` package documentation
gains a Decorators section that states the convention.

### `blob/memory`

```go
// ReadRange copies len(dst) bytes of the stored body, starting at off,
// into dst. Stored bodies are immutable, so the bytes belong to the
// version the returned Info names.
//
// # Allocation contract
//
// Zero alloc. It copies into dst.
func (s *Store) ReadRange(ctx context.Context, key string, off int64, dst []byte, ifMatch version.Version) (int, blob.Info, error)
```

The store takes the object that the key names under the lock, and
compares `ifMatch` with that object's version. The store replaces an
object on overwrite and never mutates a stored body, so the bytes it
copies after the lock is released belong to that version.

### Conformance

```go
// WithRangeReader requires the store to implement blob.RangeReader,
// found through blob.AsRangeReader. A store without the capability
// fails the run.
func WithRangeReader() Option
```

`AssertStore` runs the cases of the capability for every store in which
`blob.AsRangeReader` finds it, with or without the option. An adapter
that implements the capability cannot skip its cases.
`testing/fstest.TestFS` checks a file system the same way. It runs the
checks of `fs.ReadDirFS`, `fs.GlobFS`, `fs.StatFS`, `fs.ReadLinkFS` and
`fs.ReadFileFS` whenever the file system implements them.

The option catches an adapter whose method signature has drifted from
the interface. That adapter no longer implements the capability, and a
detection by type assertion skips its cases without a failure. An
adapter can also declare `var _ blob.RangeReader = (*Store)(nil)`, which
fails to compile after the same drift. `blob/memory` declares the same
assertion for `blob.Store`.

The cases are:

- Each of these ranges returns the bytes that `Get` returns for the
  same range, with the count and the error of `bytes.Reader.ReadAt`:
  lengths 0, 1 and the whole object, a range that ends at the last
  byte, a range that ends past the end, and offsets at and past the
  end.
- A negative offset and an invalid key classify as Invalid, and an
  absent object classifies as NotFound.
- A read with the stored version as `ifMatch` succeeds. A read with a
  version from before an overwrite returns `version.ErrMismatch`.
- A read that runs during overwrites returns bytes of the version its
  Info names.
- A done context returns the context's error.

The suite's own test gains a broken store for each case, and requires
each case to fail against the store that breaks it, as it does for
every case today. It also requires a run with the option to fail for a
store without the capability.

### Tests outside the suite

- `AsRangeReader` finds the capability on the store itself, behind one
  decorator and behind two, and reports false for a store without it
  and for a decorator without `Unwrap`.
- `BenchmarkAsRangeReader` and `BenchmarkReadRange` report no
  allocations.
- `blob/memory` passes `blobtest.AssertStore` with `WithRangeReader`.

### Migration

None. The capability is additive. Every existing `Store` compiles and
behaves as before.

## Alternatives considered

### A. A reader over the range

`GetRange(ctx, key, off, n int64) (io.ReadCloser, Info, error)` would
return a reader over the range. It streams a range too large for
memory, and it maps directly onto an HTTP response body.

**Why not:** the readers this capability serves read a bounded range
into memory anyway, because they authenticate a whole chunk before
they use any of it. Reading into `dst` does not allocate, and a reader
reuses one buffer for every chunk. A reader over the range allocates a
reader per call and needs a `Close` on every path.

### B. No precondition

`ReadRange` would take no `ifMatch`. A caller would compare
`Info.Version` after each read. The signature is one argument shorter.
A backend would not need conditional reads.

**Why not:** the caller detects an overwrite only after it has read the
range. A reader of a header and then of chunks has read two versions by
then, and it cannot ask the store to refuse the read. S3 and Cloud
Storage evaluate the condition in the read request itself, so the
parameter costs their adapters nothing. A backend without conditional
reads performs the same comparison the caller would.

### C. A fallback helper

`blob.ReadRange(ctx, s, key, off, dst)` would use the capability when
the store has it, and otherwise read through `Get` and discard the
bytes before `off`, as `cas.GetStream` buffers an object when a store
lacks `cas.Streamer`.

**Why not:** the capability exists to avoid transferring the rest of
the object. The fallback transfers it on every call, behind a call that
looks cheap. The `cas` fallback costs memory for an object that fits in
memory. This fallback costs a full transfer per range.

### D. A method on `Store`

Every backend the seam fronts supports ranged reads: S3, Cloud Storage,
a filesystem through `os.File.ReadAt`, and memory. `ReadRange` on
`Store` itself would not need an assertion or a decorator convention.

**Why not:** it breaks every `Store` implementation outside core for a
need that one class of callers has. An optional capability is additive,
and it can move into `Store` if every adapter implements it.

### E. `version.ErrMismatch` for an absent object

`ReadRange` would return `version.ErrMismatch` for an absent object
when `ifMatch` is not zero, as `Put` and `Delete` do. A caller that
passes a version would handle one error for every change to the object,
including its deletion.

**Why not:** an HTTP backend returns 404 for this request, so the
adapter would have to translate the response. `Get` and `Stat` would
still return NotFound for the same object, and a reader would handle a
deleted object differently on each read path. A reader that restarts
after `version.ErrMismatch` reads the first range again, and that read
returns NotFound anyway.

## Drawbacks

- `blob` gains three exported names: `RangeReader`, `AsRangeReader`
  and the decorator method `Unwrap() Store`.
- `blob/memory` gains one method. `coretest/blobtest` gains one option
  and 5 cases. Its own test gains 5 broken stores and one store without
  the capability.
- The cases run for every store that implements the capability. An
  adapter cannot run the suite with one of the cases turned off.
- An adapter over an HTTP backend receives 416 for a range that starts
  at or past the end, and has to read the object's Info with a second
  request to return it.
- A decorator without `Unwrap` hides the capability, as a `cas.Store`
  decorator without it hides `cas.Streamer`. The compiler does not
  detect it. The suite detects it only when the decorator's test passes
  `WithRangeReader`.
- A caller that asserts for the capability refuses stores without it,
  so an adapter must implement it to serve that caller.

## Open questions

None.

## Unresolved / future work

- Two or more ranges in one call. S3 serves one range per `GetObject`
  request.
- A reader over a range, as Alternative A describes, for a range too
  large for memory.

## References

- RFC-0028, named object storage.
- RFC-0038, conformance for durable adapters and decorators.
- ADR-0022, decorators expose what they wrap.
- Amazon S3 API Reference, `GetObject`,
  <https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html>.
- Google Cloud Storage JSON API, `objects.get`,
  <https://docs.cloud.google.com/storage/docs/json_api/v1/objects/get>.
- RFC 9110, HTTP Semantics, sections 13.1.1, 13.2.1, 14.1.2 and 15.5.17,
  <https://www.rfc-editor.org/rfc/rfc9110.html>.
- Go 1.27.1: `io.ReaderAt`, `bytes.Reader.ReadAt`,
  `testing/fstest.TestFS`.
