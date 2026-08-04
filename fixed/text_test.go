// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed_test

import (
	"encoding/json"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/fixed"
)

func TestString(t *testing.T) {
	t.Parallel()

	t.Run("renders every place", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			in   fixed.Fixed64
			want string
		}{
			"one":                 {fixed.One, "1.00000000"},
			"zero":                {fixed.Zero, "0.00000000"},
			"smallest":            {fixed.Smallest, "0.00000001"},
			"negative":            {-fixed.One, "-1.00000000"},
			"negative smallest":   {-fixed.Smallest, "-0.00000001"},
			"max":                 {fixed.Max, "92233720368.54775807"},
			"min":                 {fixed.Min, "-92233720368.54775807"},
			"mixed":               {1234567890, "12.34567890"},
			"padded fraction":     {100000001, "1.00000001"},
			"whole with no cents": {5 * fixed.One, "5.00000000"},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tc.in.String(), tc.want,
					"String must render all Scale places")
			})
		}
	})

	t.Run("is total on the excluded value", func(t *testing.T) {
		t.Parallel()

		// String is a diagnostic and must not fail when a value is
		// unusual — that is exactly when it is being read.
		testkit.Equal(t, outOfDomain.String(), "-92233720368.54775808",
			"String must render the bypass value rather than refuse it")
	})
}

func TestParse(t *testing.T) {
	t.Parallel()

	t.Run("accepts the grammar", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			in   string
			want fixed.Fixed64
		}{
			"whole number":           {"1", fixed.One},
			"zero":                   {"0", fixed.Zero},
			"negative zero":          {"-0", fixed.Zero},
			"negative zero fraction": {"-0.00000000", fixed.Zero},
			"full scale":             {"1.00000000", fixed.One},
			"smallest":               {"0.00000001", fixed.Smallest},
			"negative":               {"-1.00000000", -fixed.One},
			"short fraction":         {"1.5", fixed.One + fixed.One/2},
			"max":                    {"92233720368.54775807", fixed.Max},
			"min":                    {"-92233720368.54775807", fixed.Min},
			"leading zeroes":         {"007", 7 * fixed.One},
			// Exactly the largest whole count: the accumulator reaches
			// its bound without exceeding it, so the guard must be > and
			// not >=.
			"largest whole count":          {"92233720368", 9223372036800000000},
			"largest whole count negative": {"-92233720368", -9223372036800000000},
			"insignificant ninth":          {"1.000000000", fixed.One},
			"many insignificant":           {"1.00000000000000", fixed.One},
			"mixed":                        {"12.34567890", 1234567890},
			"fraction only in tenths":      {"0.1", 10000000},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := fixed.Parse(tc.in)

				testkit.NoError(t, err, "Parse must accept the grammar")
				testkit.Equal(t, got, tc.want, "Parse must produce the value")
			})
		}
	})

	t.Run("rejects everything outside the grammar", func(t *testing.T) {
		t.Parallel()

		cases := map[string]string{
			"empty":               "",
			"lone sign":           "-",
			"leading plus":        "+1",
			"no integer part":     ".5",
			"no fraction part":    "1.",
			"two points":          "1.2.3",
			"exponent":            "1e3",
			"exponent with point": "1.5e3",
			"underscore":          "1_000",
			"leading space":       " 1",
			"trailing space":      "1 ",
			"letters":             "abc",
			"hex":                 "0x10",
			"internal sign":       "1-2",
			"trailing sign":       "1-",
			"double negative":     "--1",
			"comma grouping":      "1,000",
			"infinity":            "Inf",
			"not a number":        "NaN",
		}
		for name, in := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := fixed.Parse(in)

				testkit.ErrorIs(t, err, fixed.ErrSyntax,
					"Parse must reject input outside the grammar")
				testkit.Equal(t, got, fixed.Zero,
					"a rejected parse must return Zero")
			})
		}
	})

	t.Run("rejects a significant digit past the scale", func(t *testing.T) {
		t.Parallel()

		cases := map[string]string{
			"ninth place":             "0.000000001",
			"far past the scale":      "1.00000000000001",
			"significant then zeroes": "1.0000000010",
		}
		for name, in := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := fixed.Parse(in)

				testkit.ErrorIs(t, err, fixed.ErrPrecision,
					"Parse must refuse to truncate a significant digit")
				testkit.ErrorIsNot(t, err, fixed.ErrSyntax,
					"a precision loss is not a syntax error")
				testkit.Equal(t, got, fixed.Zero,
					"a rejected parse must return Zero")
			})
		}
	})

	t.Run("rejects a magnitude beyond the domain", func(t *testing.T) {
		t.Parallel()

		cases := map[string]string{
			"one past max":         "92233720368.54775808",
			"one past min":         "-92233720368.54775808",
			"whole part too large": "92233720369",
			"far beyond":           "99999999999999999999999999999999",
			"negative far beyond":  "-99999999999999999999999999999999",
			// One digit past the largest whole count. Scaling this by
			// 10^Scale wraps a uint64 and lands back under the domain
			// bound, so a parser that bounds the accumulator one digit
			// too late returns 3.52241920 here instead of an error.
			"wraps back under the bound":           "922337203689",
			"wraps back under the bound, negative": "-922337203689",
			"wraps to a large value":               "922337203680",
		}
		for name, in := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := fixed.Parse(in)

				testkit.ErrorIs(t, err, fixed.ErrRange,
					"Parse must reject a magnitude beyond the domain")
				testkit.Equal(t, got, fixed.Zero,
					"a rejected parse must return Zero")
			})
		}
	})

	t.Run("never produces the excluded value", func(t *testing.T) {
		t.Parallel()

		// The domain is symmetric, so the most negative parseable
		// string is Min and math.MinInt64 is unreachable from text.
		got, err := fixed.Parse("-92233720368.54775807")

		testkit.NoError(t, err, "Min must be parseable")
		testkit.Equal(t, got, fixed.Min, "the most negative input must be Min")
		testkit.NotEqual(t, got, outOfDomain,
			"Parse must never produce the excluded value")
	})
}

func TestTextRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("Parse inverts String for every shape", func(t *testing.T) {
		t.Parallel()

		values := []fixed.Fixed64{
			fixed.Zero, fixed.One, fixed.Smallest, -fixed.Smallest,
			-fixed.One, fixed.Max, fixed.Min, 1234567890, -1234567890,
			99999999, -99999999, 100000000, 7,
		}
		for _, want := range values {
			got, err := fixed.Parse(want.String())

			testkit.NoError(t, err, "a rendered value must parse back")
			testkit.Equal(t, got, want, "the round trip must be exact")
		}
	})
}

func TestMarshalText(t *testing.T) {
	t.Parallel()

	t.Run("matches String", func(t *testing.T) {
		t.Parallel()

		b, err := fixed.Fixed64(1234567890).MarshalText()

		testkit.NoError(t, err, "MarshalText must succeed")
		testkit.Equal(t, string(b), "12.34567890",
			"MarshalText must match String")
	})

	t.Run("refuses the excluded value", func(t *testing.T) {
		t.Parallel()

		_, err := outOfDomain.MarshalText()

		testkit.ErrorIs(t, err, fixed.ErrRange,
			"MarshalText must not emit an out-of-domain value")
	})
}

func TestAppendText(t *testing.T) {
	t.Parallel()

	t.Run("appends to an existing buffer", func(t *testing.T) {
		t.Parallel()

		got, err := fixed.One.AppendText([]byte("value="))

		testkit.NoError(t, err, "AppendText must succeed")
		testkit.Equal(t, string(got), "value=1.00000000",
			"AppendText must append rather than replace")
	})

	t.Run("leaves dst untouched when it refuses", func(t *testing.T) {
		t.Parallel()

		got, err := outOfDomain.AppendText([]byte("value="))

		testkit.ErrorIs(t, err, fixed.ErrRange,
			"AppendText must refuse the excluded value")
		testkit.Equal(t, string(got), "value=",
			"a refused append must not modify dst")
	})
}

func TestUnmarshalText(t *testing.T) {
	t.Parallel()

	t.Run("accepts what Parse accepts", func(t *testing.T) {
		t.Parallel()

		var got fixed.Fixed64

		err := got.UnmarshalText([]byte("12.34567890"))

		testkit.NoError(t, err, "UnmarshalText must accept valid text")
		testkit.Equal(t, got, fixed.Fixed64(1234567890),
			"UnmarshalText must decode the value")
	})

	t.Run("returns Parse's errors and leaves the receiver alone", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			in   string
			want error
		}{
			"syntax":    {"nope", fixed.ErrSyntax},
			"precision": {"0.000000001", fixed.ErrPrecision},
			"range":     {"92233720369", fixed.ErrRange},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got := fixed.One

				err := got.UnmarshalText([]byte(tc.in))

				testkit.ErrorIs(t, err, tc.want,
					"UnmarshalText must return Parse's error")
				testkit.Equal(t, got, fixed.One,
					"a rejected decode must not modify the receiver")
			})
		}
	})
}

func TestJSON(t *testing.T) {
	t.Parallel()

	t.Run("encodes as a string, not a number", func(t *testing.T) {
		t.Parallel()

		// The consequence of implementing TextMarshaler, and the point
		// of it: a JSON number would decode to float64 by default.
		b, err := json.Marshal(fixed.Fixed64(1234567890))

		testkit.NoError(t, err, "json.Marshal must succeed")
		testkit.Equal(t, string(b), `"12.34567890"`,
			"a Fixed64 must encode as a JSON string")
	})

	t.Run("round-trips through a struct field", func(t *testing.T) {
		t.Parallel()

		type payload struct {
			Amount fixed.Fixed64 `json:"amount"`
		}

		b, err := json.Marshal(payload{Amount: fixed.Max})
		testkit.NoError(t, err, "json.Marshal must succeed")
		testkit.True(t, strings.Contains(string(b), `"92233720368.54775807"`),
			"the field must carry the exact text form")

		var got payload

		testkit.NoError(t, json.Unmarshal(b, &got), "json.Unmarshal must succeed")
		testkit.Equal(t, got.Amount, fixed.Max,
			"the value must survive a JSON round trip exactly")
	})

	t.Run("rejects a malformed value on decode", func(t *testing.T) {
		t.Parallel()

		var got fixed.Fixed64

		err := json.Unmarshal([]byte(`"0.000000001"`), &got)

		testkit.ErrorIs(t, err, fixed.ErrPrecision,
			"a JSON decode must surface the precision error")
	})
}
