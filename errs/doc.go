// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package errs is the error-classification seam: a closed,
// core-owned taxonomy of what a caller should DO about an error.
//
// It is orthogonal to what went wrong, which package-local sentinel
// errors answer and errs never learns. It is orthogonal to what a
// remote caller should be told, which is transport policy.
//
// # The question this answers
//
// Every library must answer one question about a failure that its
// caller cannot answer for it: is this worth retrying? Without a
// shared vocabulary the answer gets encoded in prose, in sentinel
// names, or in string matching on error text — and none of those can
// be consumed by the code that most needs the answer. A retry loop, a
// circuit breaker, or a work queue is generic by construction and
// cannot know the packages whose errors it is handling.
//
// [Classify] gives that code an answer. [Retryable] is the shorthand
// for the only question most callers ask.
//
// # A closed enumeration, not sentinels
//
// [Class] is a single value, not a set. That is the load-bearing
// choice: errors.Join(ErrTransient, ErrPermanent) compiles, passes
// review, and means nothing, whereas one Class value cannot be two
// things. The set is fixed at eight and extending it is an RFC, so
// the taxonomy cannot accrete a vocabulary the way package sentinels
// do.
//
// # Not a status vocabulary
//
// Transport statuses answer what a caller should be TOLD, and answer
// what a caller should DO only by accident: unavailable, deadline
// exceeded, and resource exhausted are three statuses carrying one
// handling answer between them. core ships no transport and does not
// decide what maps to a 404. A consumer needing both axes carries
// both; they compose, because one describes handling and the other
// describes reporting.
//
// # Producers
//
// A producer classifies an error either by implementing [Classifier]
// on its own error type, or by wrapping with [WithClass]. Producers
// that do neither still classify usefully when they wrap one of the
// standard library sentinels [Classify] recognises.
//
// # Allocation contract
//
// [Class] is a value type; [Class.String] returns a constant.
// [Classify] and [Retryable] are zero-allocation. [WithClass]
// allocates one wrapper.
package errs
