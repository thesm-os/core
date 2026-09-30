---
adr: 0042
title: A Content Type Is Bounded
status: Accepted
date: 2026-09-30
supersedes: none
superseded-by: none
---

# ADR-0042: A Content Type Is Bounded

## Status

Accepted

## Context

`blob.PutOptions.ContentType` is a string that `Put` stores with an
object, and `Get` and `Stat` return it as stored. `blob.Store` does not
bound it, and `blob/memory` stores any string. Other stores have limits
of their own:

- S3 limits the system-defined metadata of a PUT, which includes
  `Content-Type`, to 2 KB.
- The `net/http` transport of Go returns an error for a request with a
  header value that contains a control character other than a horizontal
  tab. An adapter that sends the content type as an HTTP header fails
  such a value only when it sends the request.
- A store that keeps an object's metadata in a field of fixed size
  cannot store a longer value.

A content type that one store accepts can fail on another, and the
contract does not tell a caller which content types every store accepts.
Keys had the same problem. `ValidKey` bounds a key to what every backend
of `blob.Store` can store, and every method classifies a key that
`ValidKey` rejects as `errs.Invalid`.

RFC 6838 limits the type name and the subtype name of a media type to
127 characters each, and recommends at most 64. A type and subtype pair
has at most 255 bytes. Of the 2,361 media types in the IANA registry on
2026-09-30, the longest has 84 bytes.

## Decision

We will bound a content type to 255 bytes of printable ASCII, checked by
`blob.ValidContentType` on every `Put`, because a content type that one
store accepts has to be one that every store accepts:

- `blob.MaxContentTypeLen` is 255.
- `blob.ValidContentType(contentType)` reports whether a content type
  has at most `MaxContentTypeLen` bytes, each from space (0x20) to tilde
  (0x7E). The empty string is valid and means unspecified.
- `Store.Put` classifies a content type that `ValidContentType` rejects
  as `errs.Invalid`, and returns before it touches storage, as it does
  for a key that `ValidKey` rejects.
- `ValidContentType` does not parse a content type, and no store infers
  or normalises one.
- The bound applies to `Put`. `Get`, `Stat` and `List` return the stored
  value, which can fail `ValidContentType` when a writer outside the
  seam stored it.
- `coretest/blobtest` checks every store at both edges. `Put` of an
  invalid content type returns `errs.Invalid` and leaves the key as it
  was, and a valid content type of 255 bytes round-trips.

## Alternatives Considered

### No bound

Each store would return an error of its own for a content type that it
cannot store.

Rejected. A store's limit would show first as a failed `Put` in
production, and a content type that works on one store would fail on
another.

### A bound in each adapter

The adapter over a store with a limit would return an error for a longer
content type.

Rejected. Each adapter would choose its own bound, and the stores would
disagree about which content types they accept.

### A larger bound, such as 1,024 bytes

Rejected. It admits parameter lists that no registered media type needs,
and a store with 255 bytes of metadata per object could not store such a
value.

### A check of the media type's syntax

`ValidContentType` would parse the value, for example with
`mime.ParseMediaType`.

Rejected. `blob.Store` stores a content type as given, and the bound
concerns what every store can store, not what a valid media type is.
`mime.ParseMediaType` also returns the media type in lowercase and
allocates a map for the parameters.

## Consequences

**Positive:**

- Every store that passes the conformance suite stores a content type
  that `ValidContentType` accepts.
- A content type that a store could not send or keep fails at `Put` with
  `errs.Invalid`, before the store touches storage.

**Negative:**

- Every implementation of `blob.Store` has to add the check, or it fails
  the conformance suite.
- A caller that stores a content type longer than 255 bytes, or one with
  a horizontal tab or a byte from 0x80 up, receives `errs.Invalid` from
  `Put`. HTTP permits both of those bytes in a header value.
- A caller that copies `Info.ContentType` from a store that other
  writers fill into another `Put` can receive `errs.Invalid`.
- Parameters count toward the 255 bytes.

**Neutral:**

- No store parses, lowercases or infers a content type.

## References

- RFC-0028, named object storage.
- RFC 6838, "Media Type Specifications and Registration Procedures",
  section 4.2, <https://www.rfc-editor.org/rfc/rfc6838#section-4.2>.
- RFC 9110, "HTTP Semantics", section 5.5, field values,
  <https://www.rfc-editor.org/rfc/rfc9110#section-5.5>.
- IANA, Media Types registry,
  <https://www.iana.org/assignments/media-types/media-types.xhtml>.
- Amazon S3 user guide, "Working with object metadata",
  <https://docs.aws.amazon.com/AmazonS3/latest/userguide/UsingMetadata.html>.
- Go 1.27.1, `net/http/transport.go`, which validates each header value
  with `httpguts.ValidHeaderFieldValue`.
