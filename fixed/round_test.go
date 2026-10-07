// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed_test

import (
	"math"
	"math/big"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/fixed"
)

// The place counts that Round and RoundAway accept, and the two ranges of
// place counts outside [0, Scale] that they refuse.
var (
	places         = prop.Integer(0, fixed.Scale)
	negativePlaces = prop.Integer(math.MinInt, -1)
	excessPlaces   = prop.Integer(fixed.Scale+1, math.MaxInt)
)

func TestRound(t *testing.T) {
	t.Parallel()

	t.Run("Round", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			give   fixed.Fixed64
			places int
			want   fixed.Fixed64
		}{
			{name: "returns 12.34 for 12.3456789 at two places", give: 1234567890, places: 2, want: 1234000000},
			{name: "returns 12 for 12.3456789 at no places", give: 1234567890, places: 0, want: 1200000000},
			{name: "returns 12.3456 for 12.3456789 at four places", give: 1234567890, places: 4, want: 1234560000},
			{name: "returns the receiver at Scale places", give: 1234567890, places: fixed.Scale, want: 1234567890},
			{name: "returns a quantised receiver unchanged", give: 1200000000, places: 2, want: 1200000000},
			{name: "returns -12 for -12.3456789 at no places", give: -1234567890, places: 0, want: -1200000000},
			{name: "returns -12.34 for -12.3456789 at two places", give: -1234567890, places: 2, want: -1234000000},
			{name: "returns Zero for Zero", give: fixed.Zero, places: 0, want: fixed.Zero},
			{name: "returns Zero for Smallest at no places", give: fixed.Smallest, places: 0, want: fixed.Zero},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tt.give.Round(tt.places)
				assert.NoError(t, err, "Round must succeed for a place count in the scale")
				assert.Equal(t, got, tt.want, "Round must round toward zero")
			})
		}

		t.Run("returns the value rounded toward zero to the places", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Round must round the value toward zero to the step of the places", func(c *prop.Case) {
				f, n := c.Draw(values, "f"), c.Draw(places, "places")
				got, err := f.Round(n)
				assertExact(c, got, err, rounded(f, n, false))
			})
		})

		t.Run("returns no error for Max", func(t *testing.T) {
			t.Parallel()
			prop.NoError(t, func(n int) error {
				_, err := fixed.Max.Round(n)

				return err
			}, "rounding toward zero must never increase a magnitude", prop.Using(places))
		})

		t.Run("returns no error for Min", func(t *testing.T) {
			t.Parallel()
			prop.NoError(t, func(n int) error {
				_, err := fixed.Min.Round(n)

				return err
			}, "rounding toward zero must never increase a magnitude", prop.Using(places))
		})

		t.Run("returns ErrRange for a negative place count", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := fixed.One.Round(n)

				return err
			}, fixed.ErrRange, "Round must refuse a place count below 0", prop.Using(negativePlaces))
		})

		t.Run("returns ErrRange for a place count above Scale", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := fixed.One.Round(n)

				return err
			}, fixed.ErrRange, "Round must refuse a place count above Scale", prop.Using(excessPlaces))
		})

		t.Run("returns Zero with ErrRange", func(t *testing.T) {
			t.Parallel()
			got, err := fixed.One.Round(fixed.Scale + 1)
			expect.ErrorIs(t, err, fixed.ErrRange, "Round must refuse a place count above Scale")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrRange must classify as Invalid")
			expect.Equal(t, got, fixed.Zero, "a refused round must return Zero")
		})
	})

	t.Run("RoundAway", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			give   fixed.Fixed64
			places int
			want   fixed.Fixed64
		}{
			{name: "returns 12.35 for 12.3456789 at two places", give: 1234567890, places: 2, want: 1235000000},
			{name: "returns 13 for 12.3456789 at no places", give: 1234567890, places: 0, want: 1300000000},
			{name: "returns -13 for -12.3456789 at no places", give: -1234567890, places: 0, want: -1300000000},
			{name: "returns -12.35 for -12.3456789 at two places", give: -1234567890, places: 2, want: -1235000000},
			{name: "returns a quantised receiver unchanged", give: 1200000000, places: 2, want: 1200000000},
			{name: "returns the receiver at Scale places", give: 1234567890, places: fixed.Scale, want: 1234567890},
			{name: "returns Zero for Zero", give: fixed.Zero, places: 0, want: fixed.Zero},
			{name: "returns One for Smallest at no places", give: fixed.Smallest, places: 0, want: fixed.One},
			{
				name:   "returns minus One for minus Smallest at no places",
				give:   -fixed.Smallest,
				places: 0,
				want:   -fixed.One,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tt.give.RoundAway(tt.places)
				assert.NoError(t, err, "RoundAway must succeed in the domain")
				assert.Equal(t, got, tt.want, "RoundAway must round away from zero")
			})
		}

		t.Run("returns the value rounded away from zero or ErrOverflow", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "RoundAway must round the value away from zero or return ErrOverflow", func(c *prop.Case) {
				f, n := c.Draw(values, "f"), c.Draw(places, "places")
				got, err := f.RoundAway(n)
				assertExact(c, got, err, rounded(f, n, true))
			})
		})

		t.Run("returns ErrOverflow for Max at no places", func(t *testing.T) {
			t.Parallel()
			got, err := fixed.Max.RoundAway(0)
			expect.ErrorIs(t, err, fixed.ErrOverflow, "92233720369 must be outside the domain")
			expect.Equal(t, got, fixed.Zero, "an overflow must return Zero")
		})

		t.Run("returns ErrOverflow for Min at no places", func(t *testing.T) {
			t.Parallel()
			_, err := fixed.Min.RoundAway(0)
			assert.ErrorIs(t, err, fixed.ErrOverflow, "-92233720369 must be outside the domain")
		})

		t.Run("returns ErrRange for a negative place count", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := fixed.One.RoundAway(n)

				return err
			}, fixed.ErrRange, "RoundAway must refuse a place count below 0", prop.Using(negativePlaces))
		})

		t.Run("returns ErrRange for a place count above Scale", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := fixed.One.RoundAway(n)

				return err
			}, fixed.ErrRange, "RoundAway must refuse a place count above Scale", prop.Using(excessPlaces))
		})
	})
}

// TestRoundAllocs checks the allocation contract of Round and RoundAway.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestRoundAllocs(t *testing.T) {
	f := fixed.Fixed64(-1234567890)

	t.Run("Round", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { got, _ = f.Round(2) }, 0, "Round must not allocate")
		assert.Equal(t, got, fixed.Fixed64(-1234000000), "the test must measure a rounded value")
	})

	t.Run("RoundAway", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { got, _ = f.RoundAway(2) }, 0, "RoundAway must not allocate")
		assert.Equal(t, got, fixed.Fixed64(-1235000000), "the test must measure a rounded value")
	})
}

// BenchmarkRound reports the cost of Round and RoundAway, and fails when
// one allocates.
func BenchmarkRound(b *testing.B) {
	f := fixed.Fixed64(-1234567890)

	b.Run("Round", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = f.Round(2)
		}

		assert.Equal(b, got, fixed.Fixed64(-1234000000), "the benchmark must measure a rounded value")
	})

	b.Run("RoundAway", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = f.RoundAway(2)
		}

		assert.Equal(b, got, fixed.Fixed64(-1235000000), "the benchmark must measure a rounded value")
	})
}

// rounded returns the raw value of f rounded to n places: toward zero, or
// away from zero when away is true.
func rounded(f fixed.Fixed64, n int, away bool) *big.Int {
	step := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(fixed.Scale-n)), nil)

	return new(big.Int).Mul(quotient(big.NewInt(f.Raw()), step, away), step)
}
