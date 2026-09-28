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
// shared vocabulary the answer ends up in documentation, in sentinel
// names, or in string matching on error text, and the code that most
// needs the answer can read none of them. A retry loop, a circuit
// breaker, or a work queue is generic by construction and cannot know
// the packages whose errors it is handling.
//
// [Classify] gives that code an answer. [Retryable] is the shorthand
// for the only question most callers ask.
//
// # A closed enumeration, not sentinels
//
// [Class] is a single value. errors.Join(ErrTransient, ErrPermanent)
// compiles, passes review and means nothing, whereas one Class value
// cannot be two things. The set is fixed at eight and extending it
// takes an RFC, so the taxonomy cannot accrete a vocabulary the way
// package sentinels do.
//
// # Not a status vocabulary
//
// Transport statuses answer what a caller should be TOLD, and answer
// what a caller should DO only by accident: unavailable, deadline
// exceeded, and resource exhausted are three statuses that share one
// handling answer. core has no transport and does not decide what maps
// to a 404. A consumer that needs both axes keeps both, and they
// compose, because one describes handling and the other describes
// reporting.
//
// # Producers
//
// A producer classifies an error either by implementing [Classifier]
// on its own error type, or by wrapping with [WithClass]. Producers
// that do neither still classify usefully when they wrap one of the
// standard library sentinels [Classify] recognises.
//
// A producer that knows when a retry can succeed, such as a transport
// that read a server's Retry-After header, attaches the delay with
// [WithRetryAfter] or a RetryAfter method on its own error type, and
// [RetryAfter] reads it. [Retryable] reports whether a caller retries,
// and the delay sets when.
//
// # Joined errors
//
// An error joined from more than one failure classifies as the failure
// whose remedy is furthest from a retry, whatever order the failures
// are in, so a retry loop retries a join only when every classified
// failure in it is [Transient]. [Classify] states the order.
//
// # Allocation contract
//
// [Class] is a value type, and [Class.String] returns a constant.
// [Classify], [Retryable] and [RetryAfter] are zero-allocation.
// [WithClass] and [WithRetryAfter] allocate one wrapper.
package errs
