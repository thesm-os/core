---
adr: 0079
title: A Content Type Has No Space at Either End
status: Accepted
date: 2026-11-07
supersedes: ADR-0042
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0079: A Content Type Has No Space at Either End

## Status

Accepted

Supersedes the bytes of a content type in
[ADR-0042](0042-a-content-type-is-bounded.md). The bound of 255 bytes and
the rest of ADR-0042 remain in force.

## Context

`blob.ValidContentType` bounds a content type to what every store of
`blob.Store` can keep. It accepts up to 255 bytes, each from space (0x20)
to tilde (0x7E), at any position.

A store over HTTP cannot keep a space at either end of a content type:

- RFC 9110 states that a field value does not include leading or trailing
  whitespace, and requires a parser to exclude such whitespace before it
  evaluates the value.
- S3, the XML API of Cloud Storage and Azure Blob Storage take the content
  type of an object in the header `Content-Type`.
- A server of `net/http` receives the header value `" text/plain"` as
  `"text/plain"`.

A store over an S3 gateway, versitygw v1.10.0 through aws-sdk-go-v2,
returned the content type of every printable character without its
leading space. It failed the round trip of `coretest/blobtest` on that
case alone.

## Decision

We will refuse a content type whose first or last byte is a space,
because no store that takes the content type as an HTTP header keeps that
space.

- `ValidContentType` reports false for such a content type. `Put` and
  `SignCreate` then classify it as `errs.Invalid`, as they classify every
  content type that `ValidContentType` refuses.
- A space between two other bytes remains valid, such as the space of
  `text/plain; charset=utf-8`.
- `coretest/blobtest` refuses `" text/plain"` and `"text/plain "`, and
  stores the printable characters with the space between two others. The
  planted defect of the round trip stores the content type in lowercase.

## Alternatives Considered

### A store that trims the content type

Each store over HTTP trims the content type before it sends it.

Rejected. `Get` would then return another content type than the one that
`Put` received, and no store normalises a content type.

### An excuse of the round trip for a store over HTTP

The rule stays, and a store over HTTP excuses the check of the printable
characters.

Rejected. The contract would accept a content type that every store over
HTTP loses, and each of those stores would need the same excuse.

### No space at all

`ValidContentType` refuses every space.

Rejected. A media type with a parameter has a space after its semicolon
by convention, and every store keeps a space between two other bytes.

## Consequences

**Positive:**

- A store that takes the content type as a header can keep each content
  type that `ValidContentType` accepts, because HTTP keeps a space between
  two other bytes.
- A caller learns at `Put` that a store cannot keep its content type,
  instead of reading another value back.

**Negative:**

- A caller that puts a content type with a space at either end receives
  `errs.Invalid`, also from a store that would keep the space.
- `Get`, `Stat` and `List` still return a content type that a writer
  stored before this rule, or outside `blob.Store`, and that
  `ValidContentType` now refuses.

**Neutral:**

- The bound of 255 bytes and the range of bytes from space to tilde do
  not change.

## References

- ADR-0042, the bound of a content type.
- Issue 125, <https://github.com/thesm-os/core/issues/125>.
- RFC 9110, "HTTP Semantics", section 5.5, field values,
  <https://www.rfc-editor.org/rfc/rfc9110#section-5.5>.
