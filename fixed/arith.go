// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed

import "math/bits"

// Add returns f + g.
//
// Exact: the result is the true sum whenever it is in range.
// Returns [ErrOverflow] otherwise, with no partial value.
//
// # Ordering
//
// Integer addition is associative, so a sum is the same value in any
// order — which is the guarantee float64 cannot give. The ERROR is
// not order-independent: a sequence whose total is representable can
// still overflow on the way there, so Max.Add(Max).Add(Min) fails
// where Max.Add(Min).Add(Max) succeeds. A caller summing a batch that
// may transit the bounds should order it accordingly.
func (f Fixed64) Add(g Fixed64) (Fixed64, error) {
	sum := f + g

	// Two conditions, and neither implies the other. The XOR test
	// catches a genuine int64 wrap: it fires when both operands share
	// a sign that the result does not. The equality test catches a
	// sum that did NOT wrap but landed on the one int64 this package
	// excludes — Min.Add(-Smallest) reaches it exactly.
	if (f^sum)&(g^sum) < 0 || sum == outOfDomain {
		return Zero, ErrOverflow
	}

	return sum, nil
}

// Sub returns f - g.
//
// Exact: the result is the true difference whenever it is in range.
// Returns [ErrOverflow] otherwise, with no partial value.
func (f Fixed64) Sub(g Fixed64) (Fixed64, error) {
	diff := f - g

	// As in [Fixed64.Add]: the XOR test catches a wrap, which needs
	// the operands to differ in sign and the result to differ from
	// the receiver. The equality test catches Min.Sub(Smallest),
	// which lands on the excluded value without wrapping.
	if (f^g)&(f^diff) < 0 || diff == outOfDomain {
		return Zero, ErrOverflow
	}

	return diff, nil
}

// Mul returns f × g, rounded toward zero.
//
// Not exact, and cannot be: eight places is not closed under
// multiplication, so [Smallest].Mul([Smallest]) is [Zero]. What Mul
// guarantees is that the rounding happens once, at the end, on a full
// 128-bit intermediate — computing at 64 bits and dividing afterwards
// would overflow for values a caller would consider ordinary.
//
// Returns [ErrOverflow] when the result is outside [Min, Max].
func (f Fixed64) Mul(g Fixed64) (Fixed64, error) {
	return f.mul(g, false)
}

// MulAway returns f × g, rounded away from zero.
//
// Identical to [Fixed64.Mul] except in the direction of the single
// final rounding. For callers who must not round down — a fee, a
// margin, a reserve — the choice is visible here in the method name
// rather than deferred to a runtime mode that every call site could
// set differently.
func (f Fixed64) MulAway(g Fixed64) (Fixed64, error) {
	return f.mul(g, true)
}

// Div returns f ÷ g, rounded toward zero.
//
// Returns [ErrDivZero] when g is [Zero], and [ErrOverflow] when the
// result is outside [Min, Max]. As with [Fixed64.Mul], the rounding
// is single and final over a 128-bit intermediate.
func (f Fixed64) Div(g Fixed64) (Fixed64, error) {
	return f.div(g, false)
}

// DivAway returns f ÷ g, rounded away from zero. See [Fixed64.Div]
// and [Fixed64.MulAway].
func (f Fixed64) DivAway(g Fixed64) (Fixed64, error) {
	return f.div(g, true)
}

// mul computes |f| × |g| ÷ 10^Scale in 128 bits and reapplies the
// sign.
//
// [bits.Mul64] and [bits.Div64] are unsigned, which is the reason
// this package excludes math.MinInt64: it is the one int64 whose
// magnitude is not an int64, and excluding it removes the branch
// rather than testing for it on every multiply.
func (f Fixed64) mul(g Fixed64, away bool) (Fixed64, error) {
	hi, lo := bits.Mul64(magnitude(f), magnitude(g))

	// bits.Div64 panics when the quotient will not fit in 64 bits.
	// This package does not panic on ordinary input, so the condition
	// is tested rather than risked — and a quotient that large is an
	// overflow under any reading.
	if hi >= scaleFactorU {
		return Zero, ErrOverflow
	}

	q, r := bits.Div64(hi, lo, scaleFactorU)

	return compose(q, r, away, f.Sign() != g.Sign())
}

// div computes |f| × 10^Scale ÷ |g| in 128 bits and reapplies the
// sign. See [Fixed64.mul] for why the operands are unsigned.
func (f Fixed64) div(g Fixed64, away bool) (Fixed64, error) {
	if g == Zero {
		return Zero, ErrDivZero
	}

	b := magnitude(g)
	hi, lo := bits.Mul64(magnitude(f), scaleFactorU)

	// The same pre-check as [Fixed64.mul], against the divisor rather
	// than the scale factor. bits.Div64 panics unless hi < b.
	if hi >= b {
		return Zero, ErrOverflow
	}

	q, r := bits.Div64(hi, lo, b)

	return compose(q, r, away, f.Sign() != g.Sign())
}

// compose applies the rounding direction and the sign to an unsigned
// quotient and remainder, bounds-checking both stages.
//
// The bound is tested before the away-from-zero increment as well as
// after. Doing it in that order is what makes the increment safe: q
// is known to be at most math.MaxInt64 when it happens, so q+1 cannot
// wrap, and the second test is a real range check rather than a
// defence against arithmetic that already went wrong.
func compose(q, r uint64, away, neg bool) (Fixed64, error) {
	if q > maxRawU {
		return Zero, ErrOverflow
	}

	if away && r != 0 {
		q++
		if q > maxRawU {
			return Zero, ErrOverflow
		}
	}

	//nolint:gosec // G115: bounded by the q > maxRawU checks above,
	// so q is at most math.MaxInt64 and the domain is symmetric.
	if neg {
		return -Fixed64(q), nil
	}

	return Fixed64(q), nil //nolint:gosec // G115: as above.
}
