// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed

// pow10 indexes 10^n for n in [0, Scale]. Used to derive the
// quantisation step from a place count.
var pow10 = [Scale + 1]Fixed64{
	1, 10, 100, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8,
}

// Round returns f quantised to places decimal places, rounding toward
// zero.
//
// This is the operation a caller who computed at [Scale] places and
// must emit at two actually needs — the directional pair on
// [Fixed64.Mul] and [Fixed64.Div] rounds the last raw unit, not to a
// chosen place. Round(2) on 12.34567890 is 12.34000000: the value
// still carries eight places, and the ones below the second are zero.
//
// Returns [ErrRange] when places is outside [0, Scale]. Rounding
// toward zero never increases a magnitude, so Round cannot overflow.
func (f Fixed64) Round(places int) (Fixed64, error) {
	return f.round(places, false)
}

// RoundAway returns f quantised to places decimal places, rounding
// away from zero.
//
// Returns [ErrRange] when places is outside [0, Scale], and
// [ErrOverflow] when rounding up crosses the domain bound —
// [Max].RoundAway(0) is 92233720369, which is not representable.
func (f Fixed64) RoundAway(places int) (Fixed64, error) {
	return f.round(places, true)
}

// round quantises f to a step of 10^(Scale-places).
//
// Go's % truncates toward zero and takes the sign of the dividend, so
// f-r is already the toward-zero result for both signs and the
// away-from-zero case is one step further out in the direction f
// already points.
func (f Fixed64) round(places int, away bool) (Fixed64, error) {
	if places < 0 || places > Scale {
		return Zero, ErrRange
	}

	step := pow10[Scale-places]
	r := f % step
	q := f - r

	if !away || r == Zero {
		return q, nil
	}

	// Bound before stepping rather than after: q is a multiple of
	// step at this point, so testing the headroom is exact and there
	// is no wrapped value to detect afterwards.
	if f > Zero {
		if q > Max-step {
			return Zero, ErrOverflow
		}

		return q + step, nil
	}

	if q < Min+step {
		return Zero, ErrOverflow
	}

	return q - step, nil
}
