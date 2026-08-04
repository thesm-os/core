// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/fixed"
)

func TestRound(t *testing.T) {
	t.Parallel()

	t.Run("quantises toward zero", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			in     fixed.Fixed64
			places int
			want   fixed.Fixed64
		}{
			"to minor units":     {1234567890, 2, 1234000000},
			"to whole units":     {1234567890, 0, 1200000000},
			"to basis points":    {1234567890, 4, 1234560000},
			"at full scale":      {1234567890, fixed.Scale, 1234567890},
			"already quantised":  {1200000000, 2, 1200000000},
			"negative to units":  {-1234567890, 0, -1200000000},
			"negative to minors": {-1234567890, 2, -1234000000},
			"zero":               {fixed.Zero, 0, fixed.Zero},
			"below the step":     {fixed.Smallest, 0, fixed.Zero},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := tc.in.Round(tc.places)

				testkit.NoError(t, err, "Round must succeed for valid places")
				testkit.Equal(t, got, tc.want, "Round must quantise toward zero")
			})
		}
	})

	t.Run("cannot overflow at the bounds", func(t *testing.T) {
		t.Parallel()

		// Toward zero never increases a magnitude, so even the extremes
		// are safe at every place count.
		for places := range fixed.Scale + 1 {
			_, err := fixed.Max.Round(places)
			testkit.NoError(t, err, "Round on Max must not overflow")

			_, err = fixed.Min.Round(places)
			testkit.NoError(t, err, "Round on Min must not overflow")
		}
	})

	t.Run("rejects a places count outside the scale", func(t *testing.T) {
		t.Parallel()

		for _, places := range []int{-1, fixed.Scale + 1, 100} {
			got, err := fixed.One.Round(places)

			testkit.ErrorIs(t, err, fixed.ErrRange,
				"Round must reject places outside [0, Scale]")
			testkit.Equal(t, got, fixed.Zero, "a rejected round must return Zero")
		}
	})
}

func TestRoundAway(t *testing.T) {
	t.Parallel()

	t.Run("quantises away from zero", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			in     fixed.Fixed64
			places int
			want   fixed.Fixed64
		}{
			"to minor units":     {1234567890, 2, 1235000000},
			"to whole units":     {1234567890, 0, 1300000000},
			"negative to units":  {-1234567890, 0, -1300000000},
			"negative to minors": {-1234567890, 2, -1235000000},
			"already quantised":  {1200000000, 2, 1200000000},
			"at full scale":      {1234567890, fixed.Scale, 1234567890},
			"zero":               {fixed.Zero, 0, fixed.Zero},
			"below the step":     {fixed.Smallest, 0, fixed.One},
			"negative below":     {-fixed.Smallest, 0, -fixed.One},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := tc.in.RoundAway(tc.places)

				testkit.NoError(t, err, "RoundAway must succeed in range")
				testkit.Equal(t, got, tc.want,
					"RoundAway must quantise away from zero")
			})
		}
	})

	t.Run("reports an overflow past the upper bound", func(t *testing.T) {
		t.Parallel()

		// Max is 92233720368.54775807; away from zero at zero places is
		// 92233720369, which is not representable.
		got, err := fixed.Max.RoundAway(0)

		testkit.ErrorIs(t, err, fixed.ErrOverflow,
			"rounding Max away from zero must overflow")
		testkit.Equal(t, got, fixed.Zero, "an overflow must return Zero")
	})

	t.Run("reports an overflow past the lower bound", func(t *testing.T) {
		t.Parallel()

		_, err := fixed.Min.RoundAway(0)

		testkit.ErrorIs(t, err, fixed.ErrOverflow,
			"rounding Min away from zero must overflow")
	})

	t.Run("rejects a places count outside the scale", func(t *testing.T) {
		t.Parallel()

		for _, places := range []int{-1, fixed.Scale + 1} {
			_, err := fixed.One.RoundAway(places)

			testkit.ErrorIs(t, err, fixed.ErrRange,
				"RoundAway must reject places outside [0, Scale]")
		}
	})
}

func TestRoundingRelationship(t *testing.T) {
	t.Parallel()

	t.Run("the two directions bracket the value", func(t *testing.T) {
		t.Parallel()

		// For any value and place count, toward-zero is no further from
		// zero than away-from-zero, and they differ by at most one step.
		in := fixed.Fixed64(1234567890)

		for places := range fixed.Scale + 1 {
			down, err := in.Round(places)
			testkit.NoError(t, err, "Round must succeed")

			up, err := in.RoundAway(places)
			testkit.NoError(t, err, "RoundAway must succeed")

			testkit.True(t, down <= in && in <= up,
				"the roundings must bracket the original value")
		}
	})
}
