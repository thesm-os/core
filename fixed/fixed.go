// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed

import (
	"cmp"
	"math"
)

// Fixed64 is a decimal at [Scale] places, stored as one int64.
//
// The zero value is the number zero, and every operation accepts it.
// A money type does not belong in this package. An amount without a
// currency is not zero money, so a money type needs an invalid zero
// value, and it stores its currency beside a Fixed64.
//
// # Comparability
//
// The underlying type of Fixed64 is [int64], so Fixed64 satisfies
// [cmp.Ordered]. The standard operators compare two values, a Fixed64
// can be a map key, and [slices.Sort] sorts a slice of them without a
// comparison function. [Fixed64.Compare] returns a three-way result
// for call sites that need one.
//
// # Domain
//
// Valid values range from [Min] to [Max]. The domain excludes
// math.MinInt64, as [Fixed64.Neg] explains.
//
// # Allocation contract
//
// Fixed64 is a value type. Every method except [Fixed64.String],
// [Fixed64.MarshalText] and [Fixed64.MarshalBinary] is zero-alloc.
type Fixed64 int64

const (
	// Scale is the number of decimal places. One logical unit is
	// 100,000,000 raw units, the value of [One].
	//
	// Eight places represent minor currency units (2), basis points
	// (4), interest and exchange rates (6) and per-unit prices (8)
	// exactly, and leave a range of ±92.2 billion. Nine places, the
	// nanoseconds of [time.Duration], would cut the range to ±9.2
	// billion for a precision that none of these uses needs.
	//
	// Scale is a constant, not a type parameter. Go cannot parameterise
	// a type on a value, so a scale per type would need one type per
	// scale, and adding values of two such types would need a
	// conversion, the error that this package prevents.
	Scale = 8

	// Size is the length in bytes of the binary form of a [Fixed64].
	Size = 8

	// Zero is the number zero and the zero value of [Fixed64].
	Zero Fixed64 = 0

	// One is the number one: 100,000,000 raw units. It is also the
	// scale factor, so a raw value divided by One is a whole count.
	One Fixed64 = 1e8

	// Smallest is 10⁻⁸, the smallest representable step: an absolute
	// bound of a fixed-point representation, where machine epsilon is a
	// relative bound of a floating-point one. The constant is not named
	// Epsilon for that reason.
	Smallest Fixed64 = 1

	// Max is the largest representable value, 92233720368.54775807.
	Max Fixed64 = math.MaxInt64

	// Min is the smallest representable value, -92233720368.54775807.
	//
	// Min is -[math.MaxInt64], not math.MinInt64, so the domain is
	// symmetric about zero, as [Fixed64.Neg] explains. Min is a
	// literal: an operator in a const declaration has no coverage
	// counter, so a mutation tool does not run its mutants.
	Min Fixed64 = -9_223_372_036_854_775_807
)

const (
	// scaleFactor is 10^Scale as a raw count.
	scaleFactor = int64(One)

	// scaleFactorU is scaleFactor for the unsigned 128-bit paths.
	scaleFactorU = uint64(One)

	// maxRawU is the largest in-domain magnitude, unsigned.
	maxRawU = uint64(math.MaxInt64)

	// maxWholeUnits is the largest integer [FromInt] accepts: [Max]
	// divided by [One], truncated. It is a literal for the reason [Min]
	// is.
	maxWholeUnits int64 = 92_233_720_368

	// outOfDomain is math.MinInt64, which the underlying int64 can
	// represent and the domain excludes. No constructor and no decode
	// path returns it.
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

// FromRaw returns the [Fixed64] of raw, a count of 10⁻⁸ units. It is
// the inverse of [Fixed64.Raw], for a decode path that reads the raw
// int64 instead of the text or binary form.
//
// Returns [ErrRange] for math.MinInt64, the one int64 outside the
// domain. Every other int64 is a valid Fixed64, so the check is one
// comparison.
func FromRaw(raw int64) (Fixed64, error) {
	if err := Fixed64(raw).valid(); err != nil {
		return Zero, err
	}

	return Fixed64(raw), nil
}

// valid returns [ErrRange] for math.MinInt64, the one int64 outside
// the domain, and nil for every other value. It is the one domain
// check of the package: [FromRaw], [Fixed64.AppendBinary],
// [Fixed64.AppendText] and [Fixed64.ValidateKanon] call it, so every
// form of a Fixed64 accepts the same values.
func (f Fixed64) valid() error {
	if f == outOfDomain {
		return ErrRange
	}

	return nil
}

// Raw returns f as a count of 10⁻⁸ units, its underlying int64.
//
// [FromRaw] is its inverse, and a storage layer with an integer column
// uses the pair. Arithmetic belongs in Fixed64, whose methods apply the
// scale and check for overflow.
func (f Fixed64) Raw() int64 {
	return int64(f)
}

// Int returns the whole part of f, truncated toward zero: 1 for
// 1.99999999, and -1 for -1.99999999.
//
// For a rounded whole number, a caller calls [Fixed64.Round] or
// [Fixed64.RoundAway] with places 0 first.
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

// Compare returns -1 if f is less than g, +1 if greater, 0 if equal,
// for [slices.SortFunc] and other callers of a three-way comparison.
// The operators compare two values directly: f < g is valid Go.
func (f Fixed64) Compare(g Fixed64) int {
	return cmp.Compare(f, g)
}

// Neg returns -f and cannot fail: the domain excludes math.MinInt64.
//
// A Neg over the full int64 range would need an error for
// math.MinInt64, which has no positive counterpart. No realistic input
// returns that error. Excluding the value also removes that branch from
// [Fixed64.Mul] and [Fixed64.Div], which apply a sign to an unsigned
// magnitude.
//
// Because Fixed64 is a defined int64, the conversion
// Fixed64(math.MinInt64) compiles. No constructor and no decode path
// returns that value. For it, Neg and [Fixed64.Abs] return the value
// unchanged, which is wrong, and nothing detects it. Such a conversion
// bypasses every check of this package.
func (f Fixed64) Neg() Fixed64 {
	return -f
}

// Abs returns the magnitude of f. It cannot fail. [Fixed64.Neg]
// explains why, and names the one value for which Abs is wrong.
func (f Fixed64) Abs() Fixed64 {
	if f < Zero {
		return -f
	}

	return f
}

// magnitude returns |f| as a uint64, for the 128-bit paths.
//
// It is correct for every value of the domain, and for outOfDomain:
// negating math.MinInt64 returns math.MinInt64, whose bit pattern as a
// uint64 is 2⁶³, its magnitude.
func magnitude(f Fixed64) uint64 {
	if f < Zero {
		return uint64(-f)
	}

	return uint64(f)
}
