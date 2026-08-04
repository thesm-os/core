// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed

//go:generate testkit sentinel -o errors.gen_test.go

import "errors"

// Sentinel errors returned by construction, arithmetic, and decoding.
//
// Every one classifies as Invalid under
// [go.thesmos.sh/core/errs.Classify]: each reports that the input
// itself is wrong, so the identical call will never succeed and
// retrying it is a guess. They are distinct sentinels rather than one
// because the caller's response differs — a malformed string is a
// boundary-validation failure, an overflow is a modelling failure,
// and a division by zero is a logic failure.
var (
	// ErrOverflow reports that an arithmetic result is outside
	// [Min, Max]. Returned by [FromInt], the four arithmetic
	// methods, and [Fixed64.RoundAway].
	//
	// The operation is not wrapped, not saturated, and not partially
	// applied: no value is returned alongside it. Saturation would
	// produce a plausible number with no signal, and [Max] is a value
	// somebody will store.
	ErrOverflow = errors.New("fixed: result out of range")

	// ErrDivZero reports division by [Zero], from [Fixed64.Div] or
	// [Fixed64.DivAway].
	//
	// Distinct from [ErrOverflow] because the causes do not overlap:
	// an overflow means the model chose too small a type, a division
	// by zero means a denominator was never checked.
	ErrDivZero = errors.New("fixed: division by zero")

	// ErrRange reports a value outside the representable domain that
	// did not arise from arithmetic: a decoded or parsed magnitude
	// beyond [Max], the excluded math.MinInt64, or a places argument
	// to [Fixed64.Round] outside [0, Scale].
	//
	// Separate from [ErrOverflow] so a caller can tell "the input you
	// handed me is unrepresentable" from "the sum you asked for is".
	ErrRange = errors.New("fixed: value out of range")

	// ErrSyntax reports that [Parse] was handed something outside its
	// grammar — an empty string, a stray sign or exponent, an
	// embedded space, a missing digit either side of the point.
	//
	// The grammar is deliberately narrow; see [Parse].
	ErrSyntax = errors.New("fixed: malformed decimal")

	// ErrPrecision reports that [Parse] was handed a significant
	// digit beyond the eighth decimal place.
	//
	// Truncating it silently is the representation error this package
	// exists to prevent, reintroduced at the boundary: a caller who
	// wrote a ninth significant digit meant it. Trailing zeroes are
	// not significant and are accepted, so "1.000000000" parses and
	// "0.000000001" does not.
	ErrPrecision = errors.New("fixed: more than 8 decimal places")

	// ErrSize reports that [Fixed64.UnmarshalBinary] was handed
	// other than [Size] bytes.
	//
	// A short read is a decode error, never a panic and never a
	// partially-filled value.
	ErrSize = errors.New("fixed: encoded value must be 8 bytes")
)
