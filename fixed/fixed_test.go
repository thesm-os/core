// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package fixed_test

import (
	"math"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/fixed"
)

// outOfDomain is math.MinInt64, which only an unchecked conversion
// produces. No constructor returns it, and the tests that use it check
// what the package does with a value that it documents as a bypass.
const outOfDomain = fixed.Fixed64(math.MinInt64)

// maxWholeUnits is the largest whole count that FromInt accepts:
// 92,233,720,368, the whole part of Max.
const maxWholeUnits = 92_233_720_368

// values generates the values of the domain. The first generator draws
// from the whole domain. The second draws n from 0 to 63 and then a value
// of a magnitude below 2^n, so small magnitudes occur as often as large
// ones.
var values = prop.OneOf(
	prop.Integer(fixed.Min, fixed.Max),
	prop.Integer(0, 63).Bind(func(n int) prop.Generator[fixed.Fixed64] {
		bound := fixed.Max >> (63 - n)

		return prop.Integer(-bound, bound)
	}),
)

func TestFixed64(t *testing.T) {
	t.Parallel()

	t.Run("has Zero as its zero value", func(t *testing.T) {
		t.Parallel()
		var f fixed.Fixed64
		assert.Equal(t, f, fixed.Zero, "the zero value must be Zero")
	})

	t.Run("has One at 10^Scale raw units", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, fixed.One.Raw(), int64(1e8), "One must be 10^Scale raw units")
	})

	t.Run("has Smallest at one raw unit", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, fixed.Smallest.Raw(), int64(1), "Smallest must be one raw unit")
	})

	t.Run("has Max at math.MaxInt64 raw units", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, fixed.Max.Raw(), int64(math.MaxInt64), "Max must be math.MaxInt64 raw units")
	})

	t.Run("has Min at the negation of Max", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, fixed.Min, -fixed.Max, "the domain must be symmetric about zero")
	})

	t.Run("sorts with slices.Sort", func(t *testing.T) {
		t.Parallel()
		got := []fixed.Fixed64{fixed.Max, fixed.Min, fixed.Zero, fixed.One}
		slices.Sort(got)
		assert.Equal(t, got, []fixed.Fixed64{fixed.Min, fixed.Zero, fixed.One, fixed.Max},
			"slices.Sort must order values numerically without a comparison function")
	})

	t.Run("finds an equal value as a map key", func(t *testing.T) {
		t.Parallel()
		m := map[fixed.Fixed64]string{fixed.One: "one"}
		assert.Equal(t, m[fixed.One], "one", "an equal value must find the key")
	})

	t.Run("FromInt", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the whole count at Scale places", func(t *testing.T) {
			t.Parallel()
			got, err := fixed.FromInt(3)
			assert.NoError(t, err, "FromInt must accept a count in the range")
			assert.Equal(t, got.Raw(), int64(3e8), "3 must be 3 times 10^Scale raw units")
		})

		t.Run("returns the value whose Int is the count", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, fixed.FromInt, func(f fixed.Fixed64) (int64, error) {
				return f.Int(), nil
			}, "Int must return the count that FromInt scales",
				prop.Using(prop.Integer[int64](-maxWholeUnits, maxWholeUnits)),
				prop.Example(int64(0)), prop.Example(int64(-1)),
				prop.Example(int64(maxWholeUnits)), prop.Example(int64(-maxWholeUnits)))
		})

		t.Run("returns ErrOverflow for a count above the range", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(v int64) error {
				_, err := fixed.FromInt(v)

				return err
			}, fixed.ErrOverflow, "FromInt must refuse a count that it cannot scale",
				prop.Using(prop.Integer[int64](maxWholeUnits+1, math.MaxInt64)))
		})

		t.Run("returns ErrOverflow for a count below the range", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(v int64) error {
				_, err := fixed.FromInt(v)

				return err
			}, fixed.ErrOverflow, "FromInt must refuse a count that it cannot scale",
				prop.Using(prop.Integer[int64](math.MinInt64, -maxWholeUnits-1)))
		})

		t.Run("returns Zero with the error", func(t *testing.T) {
			t.Parallel()
			got, err := fixed.FromInt(maxWholeUnits + 1)
			expect.ErrorIs(t, err, fixed.ErrOverflow, "FromInt must refuse the count")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrOverflow must classify as Invalid")
			expect.Equal(t, got, fixed.Zero, "a refused count must return Zero")
		})
	})

	t.Run("FromRaw", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value whose Raw is the count", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, fixed.FromRaw, func(f fixed.Fixed64) (int64, error) {
				return f.Raw(), nil
			}, "Raw must return the count of FromRaw for every count of the domain",
				prop.Using(prop.Integer[int64](-math.MaxInt64, math.MaxInt64)))
		})

		t.Run("returns ErrRange for math.MinInt64", func(t *testing.T) {
			t.Parallel()
			got, err := fixed.FromRaw(math.MinInt64)
			expect.ErrorIs(t, err, fixed.ErrRange, "FromRaw must refuse the one int64 outside the domain")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrRange must classify as Invalid")
			expect.Equal(t, got, fixed.Zero, "a refused count must return Zero")
		})
	})

	t.Run("Int", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give fixed.Fixed64
			want int64
		}{
			{name: "returns 1 for 1.99999999", give: 199999999, want: 1},
			{name: "returns -1 for -1.99999999", give: -199999999, want: -1},
			{name: "returns 1 for One", give: fixed.One, want: 1},
			{name: "returns 0 for 0.99999999", give: 99999999, want: 0},
			{name: "returns 0 for Zero", give: fixed.Zero, want: 0},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.Int(), tt.want, "Int must truncate toward zero")
			})
		}
	})

	t.Run("IsZero", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for Zero", func(t *testing.T) {
			t.Parallel()
			assert.True(t, fixed.Zero.IsZero(), "Zero must report IsZero")
		})

		t.Run("reports false for Smallest", func(t *testing.T) {
			t.Parallel()
			assert.False(t, fixed.Smallest.IsZero(), "the smallest step must not report IsZero")
		})
	})

	t.Run("Sign", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give fixed.Fixed64
			want int
		}{
			{name: "returns 1 for a positive value", give: fixed.One, want: 1},
			{name: "returns 0 for Zero", give: fixed.Zero, want: 0},
			{name: "returns -1 for a negative value", give: -fixed.One, want: -1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.Sign(), tt.want, "Sign must return the three-way sign")
			})
		}
	})

	t.Run("Compare", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			f, g fixed.Fixed64
			want int
		}{
			{name: "returns -1 for a smaller receiver", f: fixed.One, g: 2 * fixed.One, want: -1},
			{name: "returns 1 for a larger receiver", f: 2 * fixed.One, g: fixed.One, want: 1},
			{name: "returns 0 for an equal argument", f: fixed.One, g: fixed.One, want: 0},
			{name: "returns -1 for Min against Zero", f: fixed.Min, g: fixed.Zero, want: -1},
			{name: "returns -1 for Min against Max", f: fixed.Min, g: fixed.Max, want: -1},
			{name: "returns -1 for Zero against Smallest", f: fixed.Zero, g: fixed.Smallest, want: -1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.f.Compare(tt.g), tt.want, "Compare must return the order of the values")
			})
		}
	})

	t.Run("Neg", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give fixed.Fixed64
			want fixed.Fixed64
		}{
			{name: "returns minus One for One", give: fixed.One, want: -fixed.One},
			{name: "returns One for minus One", give: -fixed.One, want: fixed.One},
			{name: "returns Zero for Zero", give: fixed.Zero, want: fixed.Zero},
			{name: "returns Min for Max", give: fixed.Max, want: fixed.Min},
			{name: "returns Max for Min", give: fixed.Min, want: fixed.Max},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.Neg(), tt.want, "Neg must negate the value")
			})
		}

		t.Run("returns the value whose sum with the receiver is Zero", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Add must return Zero for a value and its negation", func(c *prop.Case) {
				f := c.Draw(values, "f")
				sum, err := f.Add(f.Neg())
				assert.NoError(c, err, "the sum of a value and its negation must not overflow")
				assert.Equal(c, sum, fixed.Zero, "the sum of a value and its negation must be Zero")
			})
		})

		t.Run("returns math.MinInt64 unchanged", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, outOfDomain.Neg(), outOfDomain, "Neg must return the bypass value unchanged")
		})
	})

	t.Run("Abs", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give fixed.Fixed64
			want fixed.Fixed64
		}{
			{name: "returns One for One", give: fixed.One, want: fixed.One},
			{name: "returns One for minus One", give: -fixed.One, want: fixed.One},
			{name: "returns Zero for Zero", give: fixed.Zero, want: fixed.Zero},
			{name: "returns Max for Min", give: fixed.Min, want: fixed.Max},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.Abs(), tt.want, "Abs must return the magnitude")
			})
		}

		t.Run("returns the magnitude of the negation", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, fixed.Fixed64.Abs, func(f fixed.Fixed64) fixed.Fixed64 { return f.Neg().Abs() },
				"a value and its negation must have one magnitude", prop.Using(values))
		})

		t.Run("returns math.MinInt64 unchanged", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, outOfDomain.Abs(), outOfDomain, "Abs must return the bypass value unchanged")
		})
	})
}

// TestFixed64Allocs checks the allocation contract of the constructors and
// the inspection methods. MaxAllocs counts the allocations of the whole
// process, so the test does not run in parallel.
func TestFixed64Allocs(t *testing.T) {
	f := fixed.Fixed64(-1234567890)

	t.Run("FromInt", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { got, _ = fixed.FromInt(-12) }, 0, "FromInt must not allocate")
		assert.Equal(t, got, -12*fixed.One, "the test must measure a scaled count")
	})

	t.Run("FromRaw", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { got, _ = fixed.FromRaw(-1234567890) }, 0, "FromRaw must not allocate")
		assert.Equal(t, got, f, "the test must measure a count of the domain")
	})

	t.Run("Raw", func(t *testing.T) {
		var got int64
		expect.MaxAllocs(t, func() { got = f.Raw() }, 0, "Raw must not allocate")
		assert.Equal(t, got, int64(-1234567890), "the test must measure the raw count")
	})

	t.Run("Int", func(t *testing.T) {
		var got int64
		expect.MaxAllocs(t, func() { got = f.Int() }, 0, "Int must not allocate")
		assert.Equal(t, got, int64(-12), "the test must measure the whole part")
	})

	t.Run("IsZero", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = f.IsZero() }, 0, "IsZero must not allocate")
		assert.False(t, got, "the test must measure a value other than Zero")
	})

	t.Run("Sign", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = f.Sign() }, 0, "Sign must not allocate")
		assert.Equal(t, got, -1, "the test must measure a negative value")
	})

	t.Run("Compare", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = f.Compare(fixed.One) }, 0, "Compare must not allocate")
		assert.Equal(t, got, -1, "the test must measure a smaller receiver")
	})

	t.Run("Neg", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { got = f.Neg() }, 0, "Neg must not allocate")
		assert.Equal(t, got, -f, "the test must measure the negation")
	})

	t.Run("Abs", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { got = f.Abs() }, 0, "Abs must not allocate")
		assert.Equal(t, got, -f, "the test must measure the magnitude")
	})
}

// BenchmarkFixed64 reports the cost of the constructors and the
// inspection methods, and fails when one allocates.
func BenchmarkFixed64(b *testing.B) {
	f := fixed.Fixed64(-1234567890)

	b.Run("FromInt", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = fixed.FromInt(-12)
		}

		assert.Equal(b, got, -12*fixed.One, "the benchmark must measure a scaled count")
	})

	b.Run("FromRaw", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = fixed.FromRaw(-1234567890)
		}

		assert.Equal(b, got, f, "the benchmark must measure a count of the domain")
	})

	b.Run("Raw", func(b *testing.B) {
		var got int64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = f.Raw()
		}

		assert.Equal(b, got, int64(-1234567890), "the benchmark must measure the raw count")
	})

	b.Run("Int", func(b *testing.B) {
		var got int64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = f.Int()
		}

		assert.Equal(b, got, int64(-12), "the benchmark must measure the whole part")
	})

	b.Run("IsZero", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = f.IsZero()
		}

		assert.False(b, got, "the benchmark must measure a value other than Zero")
	})

	b.Run("Sign", func(b *testing.B) {
		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = f.Sign()
		}

		assert.Equal(b, got, -1, "the benchmark must measure a negative value")
	})

	b.Run("Compare", func(b *testing.B) {
		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = f.Compare(fixed.One)
		}

		assert.Equal(b, got, -1, "the benchmark must measure a smaller receiver")
	})

	b.Run("Neg", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = f.Neg()
		}

		assert.Equal(b, got, -f, "the benchmark must measure the negation")
	})

	b.Run("Abs", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = f.Abs()
		}

		assert.Equal(b, got, -f, "the benchmark must measure the magnitude")
	})
}
