// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package errs

//go:generate go tool kanon -type=Class -validate=valid

import "strconv"

// Class is a single value, so contradictory classifications are
// unrepresentable by construction.
//
// The order of the constants is part of the contract. [Unspecified]
// must remain the zero value, so that an error nobody has reasoned
// about is non-retryable by default. A new class goes at the end,
// never in the middle.
//
// # Encoding
//
// A Class encodes as its name, the string [Class.String] returns, through
// [Class.AppendText], [Class.MarshalText] and [Class.UnmarshalText].
// encoding/json and the JSON handler of log/slog use the text form, so
// a class in a log line or a message reads as "Transient", not as its
// number. The names are a persisted encoding and never change.
//
// A kanon record encodes a Class as a varint of its number, through
// [Class.ValidateKanon], so a class takes 2 bytes of the record. The
// numbers never change either, because a new class takes the number
// after the last one.
//
// # Allocation contract
//
// Class is a value type. [Class.String] returns a constant for every
// defined Class.
type Class uint8

const (
	// Unspecified is the reserved zero value: the error has no
	// classification. Callers MUST treat it as non-retryable. An
	// unclassified error is one nobody has reasoned about, and
	// retrying it is a guess.
	Unspecified Class = iota

	// Transient means the identical operation may succeed if
	// retried after a delay. Nothing about the caller's state needs
	// to change.
	Transient

	// Conflict means the state the operation was based on has
	// moved. Retrying the same operation fails the same way. The
	// caller re-reads, re-applies and retries, the
	// optimistic-concurrency loop that
	// [go.thesmos.sh/core/version.Versioned] documents.
	Conflict

	// NotFound means the addressed resource does not exist.
	NotFound

	// Invalid means the request itself is wrong. The same request
	// will never succeed.
	Invalid

	// Unsupported means the implementation cannot honour a method
	// its interface declares. It differs from Invalid: the request
	// is well-formed, and the implementation is narrower than the
	// contract. Producers SHOULD also satisfy
	// errors.Is(err, errors.ErrUnsupported).
	Unsupported

	// Denied means a refusal by policy, not a technical failure. An
	// automated retry cannot succeed. The remedy is escalation or a
	// policy change.
	Denied

	// Integrity means data failed verification. A caller never
	// retries it, because a retried read of corrupt data returns the
	// same corruption, and an automated recovery can spread it.
	Integrity
)

// String returns the class name, and "Class(N)" for a value outside
// the closed set, so a log line shows an unrecognised value as distinct
// from every class, and not as a bare number.
//
// # Allocation contract
//
// Zero alloc for every defined Class, whose names are constants. An
// out-of-range value allocates the formatted result.
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

// AppendText appends the name of c, the string [Class.String] returns,
// to b and returns the extended slice.
//
// Returns b unchanged and [ErrUnknownClass] for a value outside the
// eight classes, whose "Class(N)" rendering no decoder accepts.
//
// # Allocation contract
//
// Zero alloc when b has room for the name.
func (c Class) AppendText(b []byte) ([]byte, error) {
	if err := c.valid(); err != nil {
		return b, err
	}

	return append(b, c.String()...), nil
}

// MarshalText returns the name of c, the string [Class.String] returns.
//
// Returns [ErrUnknownClass] for a value outside the eight classes.
//
// # Allocation contract
//
// One allocation for the returned slice.
func (c Class) MarshalText() ([]byte, error) {
	return c.AppendText(nil)
}

// UnmarshalText sets c to the class whose name is text.
//
// Returns [ErrUnknownClass] for any other text, including a name in
// another letter case, and leaves c unchanged. A name that a later
// version of this package adds is an unknown class to this one, and
// guessing [Unspecified] for it would hide the difference.
//
// # Allocation contract
//
// Zero alloc.
func (c *Class) UnmarshalText(text []byte) error {
	for k := range Integrity + 1 {
		if string(text) == k.String() {
			*c = k

			return nil
		}
	}

	return ErrUnknownClass
}

// valid returns [ErrUnknownClass] for a value above [Integrity], and
// nil for each of the eight classes. [Class.AppendText] and
// [Class.ValidateKanon] call it, so the text form and the kanon form
// accept the same values.
func (c Class) valid() error {
	if c > Integrity {
		return ErrUnknownClass
	}

	return nil
}

// Classifier is implemented by errors that have a Class.
//
// An error type that knows its own class implements it. [WithClass]
// tags an error that does not.
type Classifier interface{ Class() Class }
