// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package errs

import (
	"errors"
	"io/fs"

	"go.thesmos.sh/core/epoch"
	"go.thesmos.sh/core/version"
)

// joinRank is the rank of each class in a joined error, whose class is
// the class of highest rank among its branches. The ranks run from 1
// for [Transient], whose remedy is a retry, to 7 for [Integrity], whose
// remedy is furthest from one.
//
// [Unspecified] and every value outside the eight classes rank zero, so
// a branch without a class leaves the result to the other branches.
// The table has an entry for every Class value, so an index into it
// does not need a bounds check.
var joinRank = [256]uint8{
	Transient:   1,
	Conflict:    2,
	NotFound:    3,
	Unsupported: 4,
	Invalid:     5,
	Denied:      6,
	Integrity:   7,
}

// byJoinRank maps a rank in [joinRank] back to its class.
var byJoinRank = [...]Class{
	Unspecified, Transient, Conflict, NotFound, Unsupported, Invalid, Denied, Integrity,
}

// Classify reports what a caller should do about err.
//
// Classify walks err's chain, following Unwrap() error from each error
// to the next, and returns the [Class] of the first [Classifier] on it.
// A layer that wraps a classified error with [WithClass] reclassifies
// it.
//
// # Joined errors
//
// At an error whose Unwrap returns []error, such as one that
// errors.Join builds, Classify classifies each branch as it would
// classify that branch alone, and returns the class of highest rank:
//
//  1. [Integrity]
//  2. [Denied]
//  3. [Invalid]
//  4. [Unsupported]
//  5. [NotFound]
//  6. [Conflict]
//  7. [Transient]
//
// The order runs from the class whose remedy is furthest from a retry
// to the class whose remedy is a retry. The class of a join does not
// depend on the order of its branches, and [Retryable] reports true for
// a join only when every branch with a class is [Transient]. A branch
// whose class is [Unspecified] does not change the result, and a join
// without a classified branch is Unspecified.
//
// # Recognised sentinels
//
// When err's chain ends without a Classifier or a join, Classify
// recognises a small closed set of sentinels on it, so a producer that
// has never heard of this package still classifies usefully:
//
//   - [fs.ErrPermission] — [Denied]
//   - [fs.ErrInvalid], [fs.ErrClosed] — [Invalid]
//   - [epoch.ErrSize] — [Invalid]
//   - [errors.ErrUnsupported] — [Unsupported]
//   - [fs.ErrNotExist] — [NotFound]
//   - [fs.ErrExist] — [Conflict]
//   - [version.ErrMismatch], [version.ErrExists] — [Conflict]
//   - [epoch.ErrFenced] — [Conflict]
//   - [version.ErrOutcomeUnknown] — [Transient]
//
// A syscall.Errno matches the fs sentinels through its Is method, so
// EACCES and EPERM classify as [Denied], EEXIST and ENOTEMPTY as
// [Conflict], and ENOENT as [NotFound]. An error that matches more than
// one sentinel takes the class that comes first in the list, which is
// the class of higher rank.
//
// The core sentinels are recognised here rather than wrapped at
// their producers because the producers are plain sentinels by
// design: they must classify correctly even when returned by an
// adapter that has never imported this package. The set is closed
// and grows only by deliberate decision, exactly like [Class]
// itself.
//
// Everything else is [Unspecified], and context.Canceled and
// context.DeadlineExceeded are deliberately left to that default
// rather than special-cased. A cancelled or expired context means
// the CALLER's deadline elapsed, not that the dependency failed;
// retrying inside a context that is already done cannot succeed, so
// non-retryable is already the right answer. Classifying them
// [Transient] would produce a loop that spins until something else
// notices. For the same reason Classify does not read the Timeout and
// Temporary methods of an error: context.DeadlineExceeded reports true
// for both.
//
// An explicit [Classifier] takes precedence over a recognised sentinel,
// even one nearer the start of the chain, because a producer that
// classified its own error has reasoned about it, and this package has
// not.
//
// Classify(nil) is [Unspecified].
//
// # Allocation contract
//
// Zero alloc.
func Classify(err error) Class {
	if c, ok := classOf(err); ok {
		return c
	}

	switch {
	case errors.Is(err, fs.ErrPermission):
		return Denied
	case errors.Is(err, fs.ErrInvalid), errors.Is(err, fs.ErrClosed), errors.Is(err, epoch.ErrSize):
		return Invalid
	case errors.Is(err, errors.ErrUnsupported):
		return Unsupported
	case errors.Is(err, fs.ErrNotExist):
		return NotFound
	case errors.Is(err, fs.ErrExist),
		errors.Is(err, version.ErrMismatch),
		errors.Is(err, version.ErrExists),
		errors.Is(err, epoch.ErrFenced):
		return Conflict
	case errors.Is(err, version.ErrOutcomeUnknown):
		return Transient
	default:
		return Unspecified
	}
}

// classOf walks err's chain and returns the [Class] of the first
// [Classifier] on it, or the class of the first join on it. It reports
// false when the chain ends without either, at a nil error or at an error
// without an Unwrap method.
//
// This is errors.As specialised to one interface. The standard
// library version takes a pointer to the target interface, which
// escapes into a non-inlinable call and costs one allocation on every
// classification — unacceptable on a seam whose whole purpose is to
// be called from generic retry and breaker code. A direct type
// assertion compiles to an itab lookup and allocates nothing.
//
// Both wrapping shapes are handled: Unwrap() error for a single
// cause, and Unwrap() []error for errors.Join trees, whose branches
// [joined] classifies.
func classOf(err error) (Class, bool) {
	for {
		if c, ok := err.(Classifier); ok {
			return c.Class(), true
		}

		// errorlint flags type switches on error and points at
		// errors.As. That is the function this replaces: errors.As
		// takes a pointer to the target interface, which escapes and
		// costs an allocation per call. The assertions below are on
		// the Unwrap contract itself, which is what any traversal —
		// including errors.As — must switch on somewhere.
		switch x := err.(type) { //nolint:errorlint // errors.As allocates, and the switch is on the Unwrap contract
		case interface{ Unwrap() error }:
			err = x.Unwrap()
		case interface{ Unwrap() []error }:
			return joined(x.Unwrap()), true
		default:
			return Unspecified, false
		}
	}
}

// joined returns the class of a join whose branches are branches: the
// class of highest rank in [joinRank] among the classes that [Classify]
// gives each branch alone.
func joined(branches []error) Class {
	var top uint8
	for _, b := range branches {
		top = max(top, joinRank[Classify(b)])
	}

	return byJoinRank[top]
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
// [Classifier] and is errors.Is-comparable to err, so wrapping does
// not break a caller matching on the underlying sentinel.
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
// result is comparable, and it has Unwrap so errors.Is and errors.As
// traverse through it to the original error.
type classified struct {
	err   error
	class Class
}

// Error returns the wrapped error's message unchanged and without the
// class, so log lines and assertions on error text do not contain a
// second copy of the taxonomy that could drift from [Class.String].
func (e classified) Error() string { return e.err.Error() }

// Unwrap returns the wrapped error so errors.Is and errors.As
// traverse through the tag to whatever the producer returned.
// Without it, tagging an error would break every caller matching on
// the underlying sentinel.
func (e classified) Unwrap() error { return e.err }

// Class satisfies [Classifier], which is what [Classify] finds when
// it walks the tree.
func (e classified) Class() Class { return e.class }
