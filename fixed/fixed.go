// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed

import (
	"cmp"
	"math"
)

// Fixed64 is a decimal at [Scale] places, stored as one int64.
//
// The zero value is the number zero and every operation on it works.
// That is deliberate and is the reason a money type does not belong
// here: an amount with no currency is not a monetary zero, so its
// zero value must be invalid. That distinction belongs to the type
// carrying the currency, not to the arithmetic underneath it. Fixed64
// is a number, and numbers have a zero.
//
// # Comparability
//
// Fixed64's underlying type is [int64], so it is comparable with the
// standard operators, usable as a map key, and sortable with
// [slices.Sort] directly — it satisfies [cmp.Ordered], so no
// comparison function is needed. [Fixed64.Compare] exists for call
// sites wanting a three-way result.
//
// # Domain
//
// Valid values run [Min, Max]. math.MinInt64 is excluded; see
// [Fixed64.Neg].
//
// # Allocation contract
//
// Value type; pass by value. Every method except [Fixed64.String],
// [Fixed64.MarshalText] and [Fixed64.MarshalBinary] is zero-alloc.
type Fixed64 int64

const (
	// Scale is the number of decimal places. One logical unit is
	// 100,000,000 raw units, which is the value of [One].
	//
	// Eight is chosen to be exact for every use a foundation type is
	// asked to cover — minor currency units (2), basis points (4),
	// interest and FX rates (6), per-unit pricing (8) — while leaving
	// a range of ±92.2 billion. Nine places would mirror
	// [time.Duration]'s nanoseconds and cut the range to ±9.2 billion
	// for a decimal none of those needs.
	//
	// Scale is a constant, not a type parameter. Go cannot
	// parameterise on a value, so a per-scale type would mean one
	// type per scale and two of them could not be added without a
	// conversion — which is exactly the bug this package prevents.
	Scale = 8

	// Size is the width in bytes of the binary encoding.
	Size = 8

	// Zero is the number zero and the zero value of [Fixed64].
	Zero Fixed64 = 0

	// One is the number one: 100,000,000 raw units. It is also the
	// scale factor, so a raw value divided by One is a whole count.
	One Fixed64 = 1e8

	// Smallest is the smallest representable step, 10⁻⁸.
	//
	// Named for what it is rather than "Epsilon": machine epsilon is
	// a relative bound on a floating-point representation, and this
	// is an absolute one on a fixed-point representation.
	Smallest Fixed64 = 1

	// Max is the largest representable value, 92233720368.54775807.
	Max Fixed64 = math.MaxInt64

	// Min is the smallest representable value, -92233720368.54775807.
	//
	// Min is -[math.MaxInt64], not math.MinInt64, which makes the
	// domain symmetric about zero. See [Fixed64.Neg].
	Min Fixed64 = -math.MaxInt64
)

const (
	// scaleFactor is 10^Scale as a raw count.
	scaleFactor = int64(One)

	// scaleFactorU is scaleFactor for the unsigned 128-bit paths.
	scaleFactorU = uint64(One)

	// maxRawU is the largest in-domain magnitude, unsigned.
	maxRawU = uint64(math.MaxInt64)

	// maxWholeUnits is the largest integer [FromInt] accepts.
	maxWholeUnits = int64(Max) / scaleFactor

	// outOfDomain is math.MinInt64: representable in the underlying
	// int64 and excluded from the domain. No constructor and no
	// decode path produces it.
	outOfDomain Fixed64 = math.MinInt64

	// maxTextLen is len("-92233720368.54775807").
	maxTextLen = 21
)

// FromInt returns v as a [Fixed64] with a zero fractional part.
//
// Returns [ErrOverflow] when |v| exceeds 92,233,720,368, the largest
// whole count representable at [Scale] places.
func FromInt(v int64) (Fixed64, error) {
	if v > maxWholeUnits || v < -maxWholeUnits {
		return Zero, ErrOverflow
	}

	return Fixed64(v) * One, nil
}

// FromRaw returns a [Fixed64] from a raw unit count — the inverse of
// [Fixed64.Raw]. Use it on decode paths that carry the raw int64
// rather than the text or binary form.
//
// Returns [ErrRange] for math.MinInt64, the one int64 outside the
// domain. Every other int64 is a valid Fixed64, so this is one
// comparison rather than a range scan.
func FromRaw(raw int64) (Fixed64, error) {
	if raw == int64(outOfDomain) {
		return Zero, ErrRange
	}

	return Fixed64(raw), nil
}

// Raw returns f as a count of 10⁻⁸ units — the underlying int64.
//
// Pair with [FromRaw]. Raw is the escape hatch for storage layers
// that carry an integer column; callers doing arithmetic should stay
// in Fixed64, where the scale and the overflow check travel with the
// value.
func (f Fixed64) Raw() int64 {
	return int64(f)
}

// Int returns the whole part of f, truncated toward zero.
//
// Truncation is not rounding: Int on 1.99999999 is 1, and on
// -1.99999999 is -1. A caller wanting a rounded whole number calls
// [Fixed64.Round] or [Fixed64.RoundAway] with places 0 first.
func (f Fixed64) Int() int64 {
	return int64(f) / scaleFactor
}

// IsZero reports whether f is [Zero].
func (f Fixed64) IsZero() bool {
	return f == Zero
}

// Sign returns -1 if f is negative, 0 if f is [Zero], +1 if positive.
func (f Fixed64) Sign() int {
	return cmp.Compare(f, Zero)
}

// Compare returns -1 if f is less than g, +1 if greater, 0 if equal.
//
// Provided for [slices.SortFunc] and similar plumbing that wants a
// three-way result. Ordinary comparison uses the operators directly:
// f < g is valid Go and means what it says.
func (f Fixed64) Compare(g Fixed64) int {
	return cmp.Compare(f, g)
}

// Neg returns -f. It cannot fail.
//
// This is the payoff for excluding math.MinInt64 from the domain.
// With the full int64 range, math.MinInt64 has no positive
// counterpart, so Neg would need an error return that is unreachable
// for every realistic input — the shape callers learn to ignore, and
// the ones who ignore it are wrong exactly once. Excluding the value
// deletes the case instead of handling it, and takes the same branch
// out of [Fixed64.Mul] and [Fixed64.Div], which reapply a sign to an
// unsigned magnitude.
//
// The sharp edge: Fixed64 is a defined int64, so
// Fixed64(math.MinInt64) compiles. No constructor and no decode path
// produces it, and on such a value Neg and [Fixed64.Abs] return it
// unchanged, which is wrong and undetected. An unchecked conversion
// is not a route into this type; it is a bypass of one.
func (f Fixed64) Neg() Fixed64 {
	return -f
}

// Abs returns the magnitude of f. It cannot fail; see [Fixed64.Neg]
// for why, and for the one value on which it is wrong.
func (f Fixed64) Abs() Fixed64 {
	if f < Zero {
		return -f
	}

	return f
}

// magnitude returns |f| as a uint64, for the 128-bit paths.
//
// Correct for every in-domain value, and incidentally correct for
// outOfDomain: negating math.MinInt64 wraps back to itself, and the
// unsigned reinterpretation of that bit pattern is 2⁶³, which is its
// true magnitude.
func magnitude(f Fixed64) uint64 {
	if f < Zero {
		return uint64(-f)
	}

	return uint64(f)
}
