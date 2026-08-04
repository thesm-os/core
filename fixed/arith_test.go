// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/fixed"
)

func TestAdd(t *testing.T) {
	t.Parallel()

	t.Run("is exact in range", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			a, b, want fixed.Fixed64
		}{
			// The headline float64 failure, with the answer this
			// package exists to give: 0.1 + 0.2 is exactly 0.3.
			"one tenth plus two tenths": {10000000, 20000000, 30000000},
			"crosses zero":              {-fixed.One, fixed.One, fixed.Zero},
			"identity":                  {fixed.Max, fixed.Zero, fixed.Max},
			"reaches the upper bound":   {fixed.Max - 1, fixed.Smallest, fixed.Max},
			"reaches the lower bound":   {fixed.Min + 1, -fixed.Smallest, fixed.Min},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := tc.a.Add(tc.b)

				testkit.NoError(t, err, "Add must succeed in range")
				testkit.Equal(t, got, tc.want, "Add must be exact")
			})
		}
	})

	t.Run("is commutative", func(t *testing.T) {
		t.Parallel()

		ab, errAB := fixed.Fixed64(123456789).Add(987654321)
		ba, errBA := fixed.Fixed64(987654321).Add(123456789)

		testkit.NoError(t, errAB, "Add must succeed")
		testkit.NoError(t, errBA, "Add must succeed")
		testkit.Equal(t, ab, ba, "Add must be commutative")
	})

	t.Run("reports a wrapping overflow", func(t *testing.T) {
		t.Parallel()

		// Both operands positive, result would exceed Max: the XOR
		// test fires.
		got, err := fixed.Max.Add(fixed.Smallest)

		testkit.ErrorIs(t, err, fixed.ErrOverflow, "Add past Max must overflow")
		testkit.Equal(t, got, fixed.Zero, "an overflow must return Zero")
	})

	t.Run("reports a non-wrapping landing on the excluded value", func(t *testing.T) {
		t.Parallel()

		// Min + -Smallest is math.MinInt64: representable in int64, so
		// no wrap occurs and only the domain test catches it. This is
		// the case the second half of the condition exists for.
		_, err := fixed.Min.Add(-fixed.Smallest)

		testkit.ErrorIs(t, err, fixed.ErrOverflow,
			"a sum landing on the excluded value must overflow")
	})
}

func TestSub(t *testing.T) {
	t.Parallel()

	t.Run("is exact in range", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			a, b, want fixed.Fixed64
		}{
			"three tenths minus one tenth": {30000000, 10000000, 20000000},
			"to zero":                      {fixed.One, fixed.One, fixed.Zero},
			"identity":                     {fixed.Min, fixed.Zero, fixed.Min},
			"negative minus negative":      {-fixed.One, -fixed.One, fixed.Zero},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := tc.a.Sub(tc.b)

				testkit.NoError(t, err, "Sub must succeed in range")
				testkit.Equal(t, got, tc.want, "Sub must be exact")
			})
		}
	})

	t.Run("reports a wrapping overflow", func(t *testing.T) {
		t.Parallel()

		got, err := fixed.Max.Sub(fixed.Min)

		testkit.ErrorIs(t, err, fixed.ErrOverflow,
			"the full-width difference must overflow")
		testkit.Equal(t, got, fixed.Zero, "an overflow must return Zero")
	})

	t.Run("reports a non-wrapping landing on the excluded value", func(t *testing.T) {
		t.Parallel()

		_, err := fixed.Min.Sub(fixed.Smallest)

		testkit.ErrorIs(t, err, fixed.ErrOverflow,
			"a difference landing on the excluded value must overflow")
	})
}

func TestMul(t *testing.T) {
	t.Parallel()

	t.Run("rounds toward zero", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			a, b, want fixed.Fixed64
		}{
			"identity":         {12345 * fixed.One, fixed.One, 12345 * fixed.One},
			"by zero":          {fixed.Max, fixed.Zero, fixed.Zero},
			"zero receiver":    {fixed.Zero, fixed.Max, fixed.Zero},
			"halves":           {fixed.One / 2, fixed.One / 2, fixed.One / 4},
			"negative times":   {-2 * fixed.One, 3 * fixed.One, -6 * fixed.One},
			"negative squared": {-2 * fixed.One, -3 * fixed.One, 6 * fixed.One},
			// Eight places is not closed under multiplication. The true
			// product is 10^-16; toward zero, that is Zero.
			"underflows to zero":     {fixed.Smallest, fixed.Smallest, fixed.Zero},
			"truncates a remainder":  {fixed.Smallest, fixed.One / 2, fixed.Zero},
			"negative truncates too": {-fixed.Smallest, fixed.One / 2, fixed.Zero},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := tc.a.Mul(tc.b)

				testkit.NoError(t, err, "Mul must succeed in range")
				testkit.Equal(t, got, tc.want, "Mul must round toward zero")
			})
		}
	})

	t.Run("rejects a product too wide for the 128-bit divide", func(t *testing.T) {
		t.Parallel()

		// |a|*|b| >= 2^64 * 10^Scale, so the quotient cannot fit in 64
		// bits. bits.Div64 would panic; the pre-check returns instead.
		got, err := fixed.Max.Mul(fixed.Max)

		testkit.ErrorIs(t, err, fixed.ErrOverflow,
			"a product beyond the 128-bit divide must overflow")
		testkit.Equal(t, got, fixed.Zero, "an overflow must return Zero")
	})

	t.Run("rejects a representable quotient beyond the domain", func(t *testing.T) {
		t.Parallel()

		// The divide succeeds — the quotient fits in 64 bits — but the
		// result exceeds Max. This is the bound check inside compose,
		// not the pre-check above it.
		_, err := fixed.Max.Mul(fixed.One + fixed.One/2)

		testkit.ErrorIs(t, err, fixed.ErrOverflow,
			"a quotient beyond Max must overflow")
	})
}

func TestMulAway(t *testing.T) {
	t.Parallel()

	t.Run("rounds away from zero", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			a, b, want fixed.Fixed64
		}{
			"positive remainder rounds up":   {fixed.Smallest, fixed.One / 2, fixed.Smallest},
			"negative remainder rounds down": {-fixed.Smallest, fixed.One / 2, -fixed.Smallest},
			"underflow still reaches a step": {fixed.Smallest, fixed.Smallest, fixed.Smallest},
			"exact result is untouched":      {2 * fixed.One, 3 * fixed.One, 6 * fixed.One},
			"zero has no remainder":          {fixed.Zero, fixed.Max, fixed.Zero},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := tc.a.MulAway(tc.b)

				testkit.NoError(t, err, "MulAway must succeed in range")
				testkit.Equal(t, got, tc.want, "MulAway must round away from zero")
			})
		}
	})

	t.Run("reports an overflow caused only by the rounding step", func(t *testing.T) {
		t.Parallel()

		// Chosen so the truncated quotient is exactly Max with a
		// non-zero remainder: Mul succeeds, MulAway overflows on the
		// increment alone. This is the second bound check in compose.
		a, b := fixed.Fixed64(100000001), fixed.Fixed64(9223371944621056361)

		truncated, err := a.Mul(b)

		testkit.NoError(t, err, "the truncated product must be in range")
		testkit.Equal(t, truncated, fixed.Max,
			"the truncated product must land exactly on Max")

		_, err = a.MulAway(b)

		testkit.ErrorIs(t, err, fixed.ErrOverflow,
			"rounding away from Max must overflow")
	})
}

func TestDiv(t *testing.T) {
	t.Parallel()

	t.Run("rounds toward zero", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			a, b, want fixed.Fixed64
		}{
			"identity":              {12345 * fixed.One, fixed.One, 12345 * fixed.One},
			"halves":                {fixed.One, 2 * fixed.One, fixed.One / 2},
			"zero numerator":        {fixed.Zero, fixed.Max, fixed.Zero},
			"negative numerator":    {-6 * fixed.One, 3 * fixed.One, -2 * fixed.One},
			"negative denominator":  {6 * fixed.One, -3 * fixed.One, -2 * fixed.One},
			"both negative":         {-6 * fixed.One, -3 * fixed.One, 2 * fixed.One},
			"truncates a remainder": {fixed.One, 3 * fixed.One, 33333333},
			"negative truncates":    {-fixed.One, 3 * fixed.One, -33333333},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := tc.a.Div(tc.b)

				testkit.NoError(t, err, "Div must succeed in range")
				testkit.Equal(t, got, tc.want, "Div must round toward zero")
			})
		}
	})

	t.Run("rejects division by zero", func(t *testing.T) {
		t.Parallel()

		got, err := fixed.One.Div(fixed.Zero)

		testkit.ErrorIs(t, err, fixed.ErrDivZero,
			"dividing by Zero must report ErrDivZero")
		testkit.ErrorIsNot(t, err, fixed.ErrOverflow,
			"a zero divisor is not an overflow")
		testkit.Equal(t, got, fixed.Zero, "a rejected divide must return Zero")
	})

	t.Run("rejects a quotient too wide for the 128-bit divide", func(t *testing.T) {
		t.Parallel()

		// Dividing by the smallest step multiplies by 10^8 twice over;
		// the quotient cannot fit in 64 bits and bits.Div64 would
		// panic. The pre-check returns instead.
		_, err := fixed.Max.Div(fixed.Smallest)

		testkit.ErrorIs(t, err, fixed.ErrOverflow,
			"a quotient beyond the 128-bit divide must overflow")
	})

	t.Run("rejects a representable quotient beyond the domain", func(t *testing.T) {
		t.Parallel()

		// The divide succeeds but the result exceeds Max: the bound
		// check inside compose rather than the pre-check.
		_, err := fixed.Max.Div(fixed.One - fixed.Smallest)

		testkit.ErrorIs(t, err, fixed.ErrOverflow,
			"a quotient beyond Max must overflow")
	})
}

func TestDivAway(t *testing.T) {
	t.Parallel()

	t.Run("rounds away from zero", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			a, b, want fixed.Fixed64
		}{
			"positive remainder rounds up":   {fixed.One, 3 * fixed.One, 33333334},
			"negative remainder rounds down": {-fixed.One, 3 * fixed.One, -33333334},
			"exact result is untouched":      {6 * fixed.One, 3 * fixed.One, 2 * fixed.One},
			"zero numerator":                 {fixed.Zero, fixed.Max, fixed.Zero},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := tc.a.DivAway(tc.b)

				testkit.NoError(t, err, "DivAway must succeed in range")
				testkit.Equal(t, got, tc.want, "DivAway must round away from zero")
			})
		}
	})

	t.Run("rejects division by zero", func(t *testing.T) {
		t.Parallel()

		_, err := fixed.One.DivAway(fixed.Zero)

		testkit.ErrorIs(t, err, fixed.ErrDivZero,
			"dividing by Zero must report ErrDivZero")
	})

	t.Run("reports an overflow caused only by the rounding step", func(t *testing.T) {
		t.Parallel()

		// As in MulAway: the truncated quotient is exactly Max with a
		// non-zero remainder, so only the away-from-zero increment
		// crosses the bound.
		a, b := fixed.Fixed64(9223371944621055439), fixed.Fixed64(99999999)

		truncated, err := a.Div(b)

		testkit.NoError(t, err, "the truncated quotient must be in range")
		testkit.Equal(t, truncated, fixed.Max,
			"the truncated quotient must land exactly on Max")

		_, err = a.DivAway(b)

		testkit.ErrorIs(t, err, fixed.ErrOverflow,
			"rounding away from Max must overflow")
	})
}

func TestArithmeticProperties(t *testing.T) {
	t.Parallel()

	t.Run("Mul and Div invert for exact operands", func(t *testing.T) {
		t.Parallel()

		for _, v := range []fixed.Fixed64{fixed.One, 2 * fixed.One, 250 * fixed.One} {
			product, err := fixed.Fixed64(12345 * fixed.One).Mul(v)
			testkit.NoError(t, err, "Mul must succeed")

			back, err := product.Div(v)
			testkit.NoError(t, err, "Div must succeed")
			testkit.Equal(t, back, fixed.Fixed64(12345*fixed.One),
				"an exact product must divide back to its operand")
		}
	})

	t.Run("a checked sum is order-dependent", func(t *testing.T) {
		t.Parallel()

		// The drawback the Add doc states, asserted so it stays true:
		// the total is representable either way, but one order
		// overflows in the middle and the other does not.
		_, err := fixed.Max.Add(fixed.Max)
		testkit.ErrorIs(t, err, fixed.ErrOverflow,
			"the unlucky order must overflow before reaching the total")

		partial, err := fixed.Max.Add(fixed.Min)
		testkit.NoError(t, err, "the lucky order must not overflow")

		total, err := partial.Add(fixed.Max)
		testkit.NoError(t, err, "the lucky order must reach the total")
		testkit.Equal(t, total, fixed.Max, "the total must be Max")
	})
}
