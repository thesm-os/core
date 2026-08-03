// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package errs

import (
	"errors"
	"io/fs"
)

// Classify reports what a caller should do about err.
//
// It walks err's tree and returns the [Class] of the first
// [Classifier] it finds. Failing that, it recognises two standard
// library sentinels, so a producer that has never heard of this
// package still classifies usefully:
//
//   - [fs.ErrNotExist] — [NotFound]
//   - [errors.ErrUnsupported] — [Unsupported]
//
// Everything else is [Unspecified], and context.Canceled and
// context.DeadlineExceeded are deliberately left to that default
// rather than special-cased. A cancelled or expired context means
// the CALLER's deadline elapsed, not that the dependency failed;
// retrying inside a context that is already done cannot succeed, so
// non-retryable is already the right answer. Classifying them
// [Transient] would produce a loop that spins until something else
// notices.
//
// An explicit [Classifier] wins over a recognised sentinel, even one
// wrapped closer to the surface: a producer that classified its own
// error has reasoned about it, and this package has not.
//
// Classify(nil) is [Unspecified].
//
// # Allocation contract
//
// Zero alloc.
func Classify(err error) Class {
	if err == nil {
		return Unspecified
	}

	if c, ok := classOf(err); ok {
		return c
	}

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return NotFound
	case errors.Is(err, errors.ErrUnsupported):
		return Unsupported
	default:
		return Unspecified
	}
}

// classOf walks err's tree depth-first and returns the [Class] of the
// first [Classifier] it finds.
//
// This is errors.As specialised to one interface. The standard
// library version takes a pointer to the target interface, which
// escapes into a non-inlinable call and costs one allocation on every
// classification — unacceptable on a seam whose whole purpose is to
// be called from generic retry and breaker code. A direct type
// assertion compiles to an itab lookup and allocates nothing.
//
// Both wrapping shapes are handled: Unwrap() error for a single
// cause, and Unwrap() []error for errors.Join trees, which is why
// this walks a tree rather than a chain.
func classOf(err error) (Class, bool) {
	for err != nil {
		if c, ok := err.(Classifier); ok {
			return c.Class(), true
		}

		// errorlint flags type switches on error and points at
		// errors.As. That is the function this replaces: errors.As
		// takes a pointer to the target interface, which escapes and
		// costs an allocation per call. The assertions below are on
		// the Unwrap contract itself, which is what any traversal —
		// including errors.As — must switch on somewhere.
		switch x := err.(type) { //nolint:errorlint
		case interface{ Unwrap() error }:
			err = x.Unwrap()
		case interface{ Unwrap() []error }:
			for _, branch := range x.Unwrap() {
				if c, ok := classOf(branch); ok {
					return c, true
				}
			}

			return Unspecified, false
		default:
			return Unspecified, false
		}
	}

	return Unspecified, false
}

// Retryable reports whether [Classify] returns [Transient]. It is the
// only question most callers ask.
//
// Every other class is non-retryable, including [Unspecified]:
// retrying an error nobody has classified is a guess, and the safe
// guess is not to.
//
// # Allocation contract
//
// Zero alloc.
func Retryable(err error) bool {
	return Classify(err) == Transient
}

// WithClass returns err tagged with c. The result satisfies
// [Classifier] and stays errors.Is-comparable to err, so wrapping
// does not break a caller matching on the underlying sentinel.
//
// Use it to classify an error whose type you do not own. An error
// type you do own should implement [Classifier] directly and skip
// the wrapper.
//
// WithClass(nil, c) is nil — tagging the absence of an error would
// produce a non-nil error meaning success.
//
// # Allocation contract
//
// One allocation for the wrapper.
func WithClass(err error, c Class) error {
	if err == nil {
		return nil
	}

	return classified{err: err, class: c}
}

// classified is the [WithClass] wrapper. It is a value type so the
// result is comparable, and carries Unwrap so errors.Is and errors.As
// traverse through it to the original error.
type classified struct {
	err   error
	class Class
}

// Error returns the wrapped error's message unchanged. The class is
// deliberately absent from the text: it is a machine-readable
// handling answer, and rendering it would put a second, drifting
// copy of the taxonomy into log lines and error-message assertions.
func (e classified) Error() string { return e.err.Error() }

// Unwrap returns the wrapped error so errors.Is and errors.As
// traverse through the tag to whatever the producer returned.
// Without it, tagging an error would break every caller matching on
// the underlying sentinel.
func (e classified) Unwrap() error { return e.err }

// Class satisfies [Classifier], which is what [Classify] finds when
// it walks the tree.
func (e classified) Class() Class { return e.class }
