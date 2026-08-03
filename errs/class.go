// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package errs

import "strconv"

// Class is a single value, not a set. Contradictory classifications
// are unrepresentable by construction.
//
// The constants are ordered and their positions are load-bearing:
// [Unspecified] must remain the zero value so that an error nobody
// has reasoned about is non-retryable by default. Insert new classes
// at the end, never in the middle.
//
// # Allocation contract
//
// Value type; pass by value. [Class.String] returns a constant for
// every defined Class.
type Class uint8

const (
	// Unspecified is the reserved zero value: the error carries no
	// classification. Callers MUST treat it as non-retryable. An
	// unclassified error is one nobody has reasoned about, and
	// retrying it is a guess.
	Unspecified Class = iota

	// Transient means the identical operation may succeed if
	// retried after a delay. Nothing about the caller's state needs
	// to change.
	Transient

	// Conflict means the state the operation was based on has
	// moved. Retrying identically fails identically; the caller
	// must re-read, re-apply, and retry — the optimistic-
	// concurrency loop documented on
	// [go.thesmos.sh/core/version.Versioned].
	Conflict

	// NotFound means the addressed resource does not exist.
	NotFound

	// Invalid means the request itself is wrong. The same request
	// will never succeed.
	Invalid

	// Unsupported means the implementation cannot honour a method
	// its interface declares. Distinct from Invalid: the request is
	// well-formed, the implementation is narrower than the
	// contract. Producers SHOULD also satisfy
	// errors.Is(err, errors.ErrUnsupported).
	Unsupported

	// Denied means refusal by policy rather than technical failure.
	// Automated retry cannot succeed; the remedy is escalation or a
	// policy change.
	Denied

	// Integrity means data failed verification. Never retry —
	// retrying a corrupt read yields the same corruption, and
	// automated recovery risks propagating it.
	Integrity
)

// String returns the class name. A value outside the closed set
// renders as "Class(N)", so an unrecognised value stays
// distinguishable in a log line rather than printing as a bare
// number.
//
// # Allocation contract
//
// Zero alloc for every defined Class — the returned strings are
// constants. An out-of-range value allocates the formatted result.
func (c Class) String() string {
	switch c {
	case Unspecified:
		return "Unspecified"
	case Transient:
		return "Transient"
	case Conflict:
		return "Conflict"
	case NotFound:
		return "NotFound"
	case Invalid:
		return "Invalid"
	case Unsupported:
		return "Unsupported"
	case Denied:
		return "Denied"
	case Integrity:
		return "Integrity"
	default:
		return "Class(" + strconv.Itoa(int(c)) + ")"
	}
}

// Classifier is implemented by errors carrying a Class.
//
// Implement it on an error type that already knows its own handling
// answer; use [WithClass] to tag an error that does not.
type Classifier interface{ Class() Class }
