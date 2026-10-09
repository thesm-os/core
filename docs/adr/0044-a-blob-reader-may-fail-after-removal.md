---
adr: 0044
title: A Blob Reader May Fail Once Its Version Is Removed
status: Accepted
date: 2026-10-01
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0044: A Blob Reader May Fail Once Its Version Is Removed

## Status

Accepted

## Context

`blob.Store.Get` returns a reader and the `Info` of the version that the
reader returns. The contract required the reader to return that version
to its end, also when the key was overwritten while the reader was open.
`coretest/blobtest` checked the rule: it opened a reader, overwrote the
key, drained the reader and expected the original body.

`blob/memory` and Amazon S3 meet that rule:

- `blob/memory` returns a reader over the stored slice. An overwrite
  replaces the object and leaves the slice unchanged, so the reader
  returns the old body.
- Amazon S3 applies an update to one key atomically. A GET that runs
  concurrently with a PUT of the same key returns the old data or the
  new data, and never partial data.

A store that destroys the bytes of a removed version cannot meet it. The
contract of such a store requires a removed byte to be unreadable once
the removal is visible, and gone from every medium within a bound. An
engine that zeroes the chunks of a replaced object when the overwrite
commits has a bound of zero. A store of wrapped keys has the same duty,
because crypto-shredding deletes a key by destroying its wrapped form.
To return the old version, such a store would have to keep the removed
bytes until the reader closes. A reader that never closes would keep
them without a bound.

`blob.RangeReader.ReadRange` already reads only the version that a
non-zero ifMatch names, and returns `version.ErrMismatch` when the
stored version differs.

## Decision

We will let an open reader of `blob.Store.Get` fail with an error that
wraps `version.ErrMismatch` once its version is replaced or deleted,
because a store that destroys the bytes of a removed version within a
bound cannot keep them for a reader that is still open:

- The reader returns only bytes of the version that the `Info` of `Get`
  names. It never returns a byte of another version.
- Once that version is replaced or deleted, a read either returns more
  bytes of it or fails with an error that wraps `version.ErrMismatch`.
- `coretest/blobtest` checks both rules after an overwrite and after a
  delete. A reader that drains must return the whole opened body. A
  reader that fails must have returned a prefix of it, and its error
  must wrap `version.ErrMismatch`.
- Core's tests run the suite against a store whose readers fail once
  their version is removed, and require the store to pass.
- `blob.GetBytes` and the `TileReader` of `tlog.BlobTiles` return the
  error of the reader to their callers.

## Alternatives Considered

### The store keeps a version while a reader of it is open

The store pins the version at `Get`, and destroys its bytes when the
last reader of it closes.

Rejected. The bound on destruction would start at the last close, and a
reader that never closes would keep removed bytes, wrapped keys
included, without a bound. Every store with such a duty would also need
a pin per open reader.

### Get reads the whole object into memory

Rejected. A blob store streams a body in both directions and never needs
a whole body in memory. An object can be larger than the memory of the
process that reads it.

### Get copies small objects and fails large ones

Rejected. The rule would apply only to objects below a size that each
store chooses, so a caller could not rely on it for any object.

## Consequences

**Positive:**

- A store that destroys the bytes of a removed version within a bound
  can implement `blob.Store` and pass the conformance suite.
- A reader never returns the bytes of two versions. A caller tells a
  removed version from other failures with
  `errors.Is(err, version.ErrMismatch)`.
- `blob/memory` and the stores that keep a version for an open reader,
  such as S3, pass the suite unchanged.

**Negative:**

- A read can fail where it completed before. A caller of `Get` that
  races a writer has to handle `version.ErrMismatch` from `Read` and
  open the object again.
- A caller that reads a large object which writers replace often can
  fail on every attempt. The earlier rule guaranteed that each read
  completes.
- The suite accepts a reader that fails, so it does not detect a store
  that could keep the version for an open reader and does not.

**Neutral:**

- `version.ErrMismatch` classifies as `errs.Conflict`. A removed version
  on a reader classifies as a failed precondition of `Put`, `Delete` or
  `ReadRange` does.

## References

- RFC-0028, named object storage, which states the earlier rule as one
  consistent body per open reader.
- Amazon S3 user guide, "Amazon S3 data consistency model",
  <https://docs.aws.amazon.com/AmazonS3/latest/userguide/Welcome.html#ConsistencyModel>.
