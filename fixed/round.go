// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package fixed

// pow10 indexes 10^n for n in [0, Scale], the step of each place count.
// Its length comes from its elements, because an operator in an array
// length has no coverage counter and a mutation tool never runs a mutant
// of it.
var pow10 = [...]Fixed64{
	1, 10, 100, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8,
}

// Round returns f quantised to places decimal places, rounding toward
// zero.
//
// A caller that computes at [Scale] places and emits at two needs this
// operation. [Fixed64.Mul] and [Fixed64.Div] round the last raw unit, not
// to a chosen place. Round(2) on 12.34567890 is 12.34000000: the value
// keeps eight places, and the places below the second are zero.
//
// Returns [ErrRange] when places is outside [0, Scale]. Rounding toward
// zero never increases a magnitude, so Round cannot overflow.
func (f Fixed64) Round(places int) (Fixed64, error) {
	return f.round(places, false)
}

// RoundAway returns f quantised to places decimal places, rounding away
// from zero.
//
// Returns [ErrRange] when places is outside [0, Scale], and [ErrOverflow]
// when the step away from zero leaves the domain: [Max].RoundAway(0) is
// 92233720369, which is outside it.
func (f Fixed64) RoundAway(places int) (Fixed64, error) {
	return f.round(places, true)
}

// round quantises f to a step of 10^(Scale-places).
//
// Go's % truncates toward zero and takes the sign of the dividend, so f-r
// is the result toward zero for both signs, and r has the sign of f. The
// result away from zero is one step further in the direction of r, and
// none for an r of zero. [Fixed64.Add] takes the step, so it returns
// [ErrOverflow] when the step leaves the domain.
func (f Fixed64) round(places int, away bool) (Fixed64, error) {
	if places < 0 || places > Scale {
		return Zero, ErrRange
	}

	step := pow10[Scale-places]
	r := f % step
	q := f - r

	if !away {
		return q, nil
	}

	return q.Add(step * Fixed64(r.Sign()))
}
