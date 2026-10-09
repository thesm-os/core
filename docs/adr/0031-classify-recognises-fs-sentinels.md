---
adr: 0031
title: Classify Recognises the File-System Sentinels
status: Accepted
date: 2026-09-28
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0031: Classify Recognises the File-System Sentinels

## Status

Accepted

## Context

`errs.Classify` recognised two sentinels of the standard library,
`fs.ErrNotExist` as `NotFound` and `errors.ErrUnsupported` as
`Unsupported`. The other failures of a file system classified as
`Unspecified` unless the adapter tagged them: a permission refused, a
create-only target that exists, an invalid argument and a use of a
closed file.

Package `io/fs` names each of these failures: `ErrPermission`,
`ErrExist`, `ErrInvalid` and `ErrClosed`. In the Go 1.27.1 source,
`syscall.Errno.Is` maps `EACCES` and `EPERM` to `ErrPermission`, and
`EEXIST` and `ENOTEMPTY` to `ErrExist`, on Unix and on Windows. `errs`
already imports `io/fs`.

## Decision

We will make `Classify` recognise `fs.ErrPermission` as `Denied`,
`fs.ErrExist` as `Conflict`, and `fs.ErrInvalid` and `fs.ErrClosed` as
`Invalid`, and check the recognised sentinels in the rank order of
ADR-0028, because these are the standard library's names for failures
whose handling is fixed, and `errs` already depends on the package that
defines them.

## Alternatives Considered

### The `Timeout` and `Temporary` methods

`Classify` would classify an error whose `Timeout` method reports true
as `Transient`.

Rejected. `context.DeadlineExceeded` reports true for both methods, so
the caller's own deadline would classify as `Transient`, which RFC-0015
rejects. Go deprecated `net.Error.Temporary` because "Temporary errors
are not well-defined".

### `io.ErrUnexpectedEOF`

`Classify` would recognise a truncated read.

Rejected. A truncated read of data at rest is `Integrity`, and a
truncated read from a socket is `Transient`. No single class fits.

### The errors of `net`

`Classify` would recognise errors such as `net.ErrClosed`.

Rejected. Almost every core package imports `errs`, so `net` would
become a dependency of each of them.

### Classification in each adapter

Each file-system adapter would tag the errors it returns.

Rejected. Every adapter would write the same mapping, and an error that
an adapter wraps without tagging would classify as `Unspecified`.

## Consequences

**Positive:**

- A raw errno and a `*fs.PathError` classify without a tag.
- A failed `O_EXCL` create classifies as `Conflict`, as the create-only
  failure `version.ErrExists` does.

**Negative:**

- An adapter that returns `fs.ErrExist` for anything other than a
  create-only target that exists gets `Conflict`, and tags the error
  with `errs.WithClass` to change it.
- `ENOTEMPTY` classifies as `Conflict`. That fits the removal of a
  directory that gained entries, and not every other use of the errno.

**Neutral:**

- An error that matches two recognised sentinels takes the class of
  higher rank.
- Only a decision like this one adds a sentinel to the recognised set.

## References

- RFC-0015, error classification.
- ADR-0028, a joined error classifies as its branch of highest rank.
- `errs/classify.go`: `Classify`.
- Go 1.27.1, `syscall.Errno.Is` in `syscall_unix.go` and
  `syscall_windows.go`, and `io/fs`.
- Go issue 45729, "net: deprecate Temporary error status",
  <https://github.com/golang/go/issues/45729>.
