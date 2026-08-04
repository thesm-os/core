// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed_test

import (
	"math"
	"slices"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/fixed"
)

// outOfDomain is math.MinInt64 reached the only way a caller can:
// an unchecked conversion. No constructor produces it, and the tests
// that use it are asserting on what the package does with a value it
// has documented as a bypass.
const outOfDomain = fixed.Fixed64(math.MinInt64)

func TestConstants(t *testing.T) {
	t.Parallel()

	t.Run("the zero value is the number zero", func(t *testing.T) {
		t.Parallel()

		var f fixed.Fixed64

		testkit.Equal(t, f, fixed.Zero, "the zero value must be Zero")
		testkit.True(t, f.IsZero(), "the zero value must report IsZero")
	})

	t.Run("One is the scale factor", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, fixed.One.Raw(), int64(1e8),
			"One must be 10^Scale raw units")
		testkit.Equal(t, fixed.One.Int(), int64(1), "One must be the number 1")
	})

	t.Run("Smallest is one raw unit", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, fixed.Smallest.Raw(), int64(1),
			"Smallest must be a single raw unit")
	})

	t.Run("the domain is symmetric about zero", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, fixed.Min, -fixed.Max,
			"Min must be the exact negation of Max")
		testkit.Equal(t, fixed.Max.Raw(), int64(math.MaxInt64),
			"Max must be math.MaxInt64 raw units")
		testkit.NotEqual(t, fixed.Min.Raw(), int64(math.MinInt64),
			"math.MinInt64 must be outside the domain")
	})

	t.Run("Size matches the encoded width", func(t *testing.T) {
		t.Parallel()

		b, err := fixed.One.MarshalBinary()

		testkit.NoError(t, err, "MarshalBinary must succeed")
		testkit.Len(t, b, fixed.Size, "the encoding must be Size bytes")
	})
}

func TestFromInt(t *testing.T) {
	t.Parallel()

	t.Run("scales a whole count", func(t *testing.T) {
		t.Parallel()

		got, err := fixed.FromInt(3)

		testkit.NoError(t, err, "FromInt(3) must succeed")
		testkit.Equal(t, got.Raw(), int64(3e8), "3 must be 3 * 10^Scale")
	})

	t.Run("accepts the bounds", func(t *testing.T) {
		t.Parallel()

		for _, v := range []int64{0, -1, 92233720368, -92233720368} {
			got, err := fixed.FromInt(v)

			testkit.NoError(t, err, "FromInt must accept an in-range count")
			testkit.Equal(t, got.Int(), v, "FromInt must round-trip through Int")
		}
	})

	t.Run("rejects a count beyond the range", func(t *testing.T) {
		t.Parallel()

		for _, v := range []int64{92233720369, -92233720369, math.MaxInt64, math.MinInt64} {
			_, err := fixed.FromInt(v)

			testkit.ErrorIs(t, err, fixed.ErrOverflow,
				"FromInt must reject a count that cannot be scaled")
		}
	})
}

func TestFromRaw(t *testing.T) {
	t.Parallel()

	t.Run("admits every in-domain raw value", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []int64{0, 1, -1, math.MaxInt64, -math.MaxInt64} {
			got, err := fixed.FromRaw(raw)

			testkit.NoError(t, err, "FromRaw must accept an in-domain value")
			testkit.Equal(t, got.Raw(), raw, "FromRaw must preserve the raw value")
		}
	})

	t.Run("rejects math.MinInt64", func(t *testing.T) {
		t.Parallel()

		got, err := fixed.FromRaw(math.MinInt64)

		testkit.ErrorIs(t, err, fixed.ErrRange,
			"FromRaw must reject the one out-of-domain int64")
		testkit.Equal(t, got, fixed.Zero, "a rejected value must return Zero")
	})
}

func TestInspection(t *testing.T) {
	t.Parallel()

	t.Run("Int truncates toward zero", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			in   fixed.Fixed64
			want int64
		}{
			"positive below the next unit": {199999999, 1},
			"negative below the next unit": {-199999999, -1},
			"exactly one":                  {fixed.One, 1},
			"below one":                    {99999999, 0},
			"zero":                         {fixed.Zero, 0},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tc.in.Int(), tc.want,
					"Int must truncate toward zero")
			})
		}
	})

	t.Run("Sign reports the three-way sign", func(t *testing.T) {
		t.Parallel()

		testkit.Equal(t, fixed.One.Sign(), 1, "a positive value must sign +1")
		testkit.Equal(t, fixed.Zero.Sign(), 0, "Zero must sign 0")
		testkit.Equal(t, (-fixed.One).Sign(), -1, "a negative value must sign -1")
	})

	t.Run("IsZero is exact", func(t *testing.T) {
		t.Parallel()

		testkit.True(t, fixed.Zero.IsZero(), "Zero must report IsZero")
		testkit.False(t, fixed.Smallest.IsZero(),
			"the smallest step must not report IsZero")
	})

	t.Run("Compare reflects ordering", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			a, b fixed.Fixed64
			want int
		}{
			"less":              {fixed.One, 2 * fixed.One, -1},
			"greater":           {2 * fixed.One, fixed.One, 1},
			"equal":             {fixed.One, fixed.One, 0},
			"negative vs zero":  {fixed.Min, fixed.Zero, -1},
			"bounds":            {fixed.Min, fixed.Max, -1},
			"zero vs. smallest": {fixed.Zero, fixed.Smallest, -1},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tc.a.Compare(tc.b), tc.want,
					"Compare must reflect ordering")
			})
		}
	})

	t.Run("sorts with slices.Sort and no comparison function", func(t *testing.T) {
		t.Parallel()

		// The claim in the type doc: the underlying int64 satisfies
		// cmp.Ordered, so this compiles without a cmp func.
		got := []fixed.Fixed64{fixed.Max, fixed.Min, fixed.Zero, fixed.One}
		slices.Sort(got)

		testkit.Equal(t, got,
			[]fixed.Fixed64{fixed.Min, fixed.Zero, fixed.One, fixed.Max},
			"slices.Sort must order Fixed64 numerically")
	})

	t.Run("is usable as a map key", func(t *testing.T) {
		t.Parallel()

		m := map[fixed.Fixed64]string{fixed.One: "one"}

		testkit.Equal(t, m[fixed.One], "one", "an equal value must find the key")
	})
}

func TestNegAndAbs(t *testing.T) {
	t.Parallel()

	t.Run("Neg is total across the domain", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			in, want fixed.Fixed64
		}{
			"positive": {fixed.One, -fixed.One},
			"negative": {-fixed.One, fixed.One},
			"zero":     {fixed.Zero, fixed.Zero},
			"max":      {fixed.Max, fixed.Min},
			"min":      {fixed.Min, fixed.Max},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tc.in.Neg(), tc.want, "Neg must negate")
			})
		}
	})

	t.Run("Abs is total across the domain", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			in, want fixed.Fixed64
		}{
			"positive": {fixed.One, fixed.One},
			"negative": {-fixed.One, fixed.One},
			"zero":     {fixed.Zero, fixed.Zero},
			"min":      {fixed.Min, fixed.Max},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tc.in.Abs(), tc.want, "Abs must take the magnitude")
			})
		}
	})

	t.Run("the documented sharp edge on the excluded value", func(t *testing.T) {
		t.Parallel()

		// Neg documents that it returns math.MinInt64 unchanged, which
		// is wrong and undetected. Asserting it keeps the doc honest:
		// if the behaviour ever changes, the doc must change with it.
		testkit.Equal(t, outOfDomain.Neg(), outOfDomain,
			"Neg on the bypass value must return it unchanged")
		testkit.Equal(t, outOfDomain.Abs(), outOfDomain,
			"Abs on the bypass value must return it unchanged")
	})
}
