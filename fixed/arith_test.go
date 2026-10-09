// Copyright ThesmOS B.V. 2026
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

// scale is 10^Scale, the raw count of One, as an exact integer.
var scale = big.NewInt(fixed.One.Raw())

func TestArith(t *testing.T) {
	t.Parallel()

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			f, g, want fixed.Fixed64
		}{
			{name: "returns 0.3 for 0.1 plus 0.2", f: 10000000, g: 20000000, want: 30000000},
			{name: "returns Zero for minus One plus One", f: -fixed.One, g: fixed.One, want: fixed.Zero},
			{name: "returns the receiver for Zero", f: fixed.Max, g: fixed.Zero, want: fixed.Max},
			{name: "returns Max for a sum at the upper bound", f: fixed.Max - 1, g: fixed.Smallest, want: fixed.Max},
			{name: "returns Min for a sum at the lower bound", f: fixed.Min + 1, g: -fixed.Smallest, want: fixed.Min},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tt.f.Add(tt.g)
				assert.NoError(t, err, "Add must succeed in the domain")
				assert.Equal(t, got, tt.want, "Add must return the exact sum")
			})
		}

		t.Run("returns the exact sum or ErrOverflow", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Add must return the exact sum or ErrOverflow", func(c *prop.Case) {
				f, g := c.Draw(values, "f"), c.Draw(values, "g")
				got, err := f.Add(g)
				assertExact(c, got, err, new(big.Int).Add(big.NewInt(f.Raw()), big.NewInt(g.Raw())))
			})
		})

		t.Run("returns ErrOverflow for a sum past Max", func(t *testing.T) {
			t.Parallel()
			got, err := fixed.Max.Add(fixed.Smallest)
			expect.ErrorIs(t, err, fixed.ErrOverflow, "Add past Max must overflow")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrOverflow must classify as Invalid")
			expect.Equal(t, got, fixed.Zero, "an overflow must return Zero")
		})

		t.Run("returns ErrOverflow for a sum at math.MinInt64", func(t *testing.T) {
			t.Parallel()
			_, err := fixed.Min.Add(-fixed.Smallest)
			assert.ErrorIs(t, err, fixed.ErrOverflow, "a sum at the excluded value must overflow without a wrap")
		})

		t.Run("returns ErrOverflow for one order of a sum that fits in another", func(t *testing.T) {
			t.Parallel()
			_, err := fixed.Max.Add(fixed.Max)
			assert.ErrorIs(t, err, fixed.ErrOverflow, "Max plus Max must overflow before Min joins the sum")

			partial, err := fixed.Max.Add(fixed.Min)
			assert.NoError(t, err, "Max plus Min must not overflow")
			total, err := partial.Add(fixed.Max)
			assert.NoError(t, err, "the other order must reach the total")
			assert.Equal(t, total, fixed.Max, "the total must be Max")
		})
	})

	t.Run("Sub", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			f, g, want fixed.Fixed64
		}{
			{name: "returns 0.2 for 0.3 minus 0.1", f: 30000000, g: 10000000, want: 20000000},
			{name: "returns Zero for One minus One", f: fixed.One, g: fixed.One, want: fixed.Zero},
			{name: "returns the receiver for Zero", f: fixed.Min, g: fixed.Zero, want: fixed.Min},
			{name: "returns Zero for a negative value minus itself", f: -fixed.One, g: -fixed.One, want: fixed.Zero},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tt.f.Sub(tt.g)
				assert.NoError(t, err, "Sub must succeed in the domain")
				assert.Equal(t, got, tt.want, "Sub must return the exact difference")
			})
		}

		t.Run("returns the exact difference or ErrOverflow", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Sub must return the exact difference or ErrOverflow", func(c *prop.Case) {
				f, g := c.Draw(values, "f"), c.Draw(values, "g")
				got, err := f.Sub(g)
				assertExact(c, got, err, new(big.Int).Sub(big.NewInt(f.Raw()), big.NewInt(g.Raw())))
			})
		})

		t.Run("returns ErrOverflow for Max minus Min", func(t *testing.T) {
			t.Parallel()
			got, err := fixed.Max.Sub(fixed.Min)
			expect.ErrorIs(t, err, fixed.ErrOverflow, "the difference of the bounds must overflow")
			expect.Equal(t, got, fixed.Zero, "an overflow must return Zero")
		})

		t.Run("returns ErrOverflow for a difference at math.MinInt64", func(t *testing.T) {
			t.Parallel()
			_, err := fixed.Min.Sub(fixed.Smallest)
			assert.ErrorIs(t, err, fixed.ErrOverflow, "a difference at the excluded value must overflow without a wrap")
		})
	})

	t.Run("Mul", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			f, g, want fixed.Fixed64
		}{
			{name: "returns the receiver for One", f: 12345 * fixed.One, g: fixed.One, want: 12345 * fixed.One},
			{name: "returns Zero for a factor of Zero", f: fixed.Max, g: fixed.Zero, want: fixed.Zero},
			{name: "returns Zero for a receiver of Zero", f: fixed.Zero, g: fixed.Max, want: fixed.Zero},
			{
				name: "returns a quarter for a half times a half",
				f:    fixed.One / 2,
				g:    fixed.One / 2,
				want: fixed.One / 4,
			},
			{
				name: "returns a negative product for one negative factor",
				f:    -2 * fixed.One,
				g:    3 * fixed.One,
				want: -6 * fixed.One,
			},
			{
				name: "returns a positive product for two negative factors",
				f:    -2 * fixed.One,
				g:    -3 * fixed.One,
				want: 6 * fixed.One,
			},
			{name: "returns Zero for Smallest times Smallest", f: fixed.Smallest, g: fixed.Smallest, want: fixed.Zero},
			{name: "returns Zero for Smallest times a half", f: fixed.Smallest, g: fixed.One / 2, want: fixed.Zero},
			{
				name: "returns Zero for minus Smallest times a half",
				f:    -fixed.Smallest,
				g:    fixed.One / 2,
				want: fixed.Zero,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tt.f.Mul(tt.g)
				assert.NoError(t, err, "Mul must succeed in the domain")
				assert.Equal(t, got, tt.want, "Mul must round the product toward zero")
			})
		}

		t.Run("returns the product rounded toward zero or ErrOverflow", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Mul must round the product toward zero or return ErrOverflow", func(c *prop.Case) {
				f, g := c.Draw(values, "f"), c.Draw(values, "g")
				got, err := f.Mul(g)
				product := new(big.Int).Mul(big.NewInt(f.Raw()), big.NewInt(g.Raw()))
				assertExact(c, got, err, quotient(product, scale, false))
			})
		})

		t.Run("returns ErrOverflow for a product too wide for the 128-bit division", func(t *testing.T) {
			t.Parallel()
			got, err := fixed.Max.Mul(fixed.Max)
			expect.ErrorIs(t, err, fixed.ErrOverflow, "a product past the 128-bit division must overflow")
			expect.Equal(t, got, fixed.Zero, "an overflow must return Zero")
		})

		t.Run("returns ErrOverflow for a quotient above Max", func(t *testing.T) {
			t.Parallel()
			_, err := fixed.Max.Mul(fixed.One + fixed.One/2)
			assert.ErrorIs(t, err, fixed.ErrOverflow, "a quotient of 64 bits above Max must overflow")
		})

		t.Run("returns ErrOverflow at the bound of bits.Div64", func(t *testing.T) {
			t.Parallel()
			// 2^32 times 10^Scale times 2^32 is 10^Scale times 2^64, so the
			// high word of the product equals the divisor, at which
			// bits.Div64 panics.
			_, err := fixed.Fixed64(1 << 32).Mul(fixed.Fixed64(1e8 << 32))
			assert.ErrorIs(t, err, fixed.ErrOverflow, "the bound of bits.Div64 must be an error")
		})
	})

	t.Run("MulAway", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			f, g, want fixed.Fixed64
		}{
			{
				name: "returns Smallest for Smallest times a half",
				f:    fixed.Smallest,
				g:    fixed.One / 2,
				want: fixed.Smallest,
			},
			{
				name: "returns minus Smallest for minus Smallest times a half",
				f:    -fixed.Smallest,
				g:    fixed.One / 2,
				want: -fixed.Smallest,
			},
			{
				name: "returns Smallest for Smallest times Smallest",
				f:    fixed.Smallest,
				g:    fixed.Smallest,
				want: fixed.Smallest,
			},
			{name: "returns an exact product unchanged", f: 2 * fixed.One, g: 3 * fixed.One, want: 6 * fixed.One},
			{name: "returns Zero for a receiver of Zero", f: fixed.Zero, g: fixed.Max, want: fixed.Zero},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tt.f.MulAway(tt.g)
				assert.NoError(t, err, "MulAway must succeed in the domain")
				assert.Equal(t, got, tt.want, "MulAway must round the product away from zero")
			})
		}

		t.Run("returns the product rounded away from zero or ErrOverflow", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "MulAway must round the product away from zero or return ErrOverflow", func(c *prop.Case) {
				f, g := c.Draw(values, "f"), c.Draw(values, "g")
				got, err := f.MulAway(g)
				product := new(big.Int).Mul(big.NewInt(f.Raw()), big.NewInt(g.Raw()))
				assertExact(c, got, err, quotient(product, scale, true))
			})
		})

		t.Run("returns ErrOverflow when the rounding step passes Max", func(t *testing.T) {
			t.Parallel()
			f, g := fixed.Fixed64(100000001), fixed.Fixed64(9223371944621056361)

			truncated, err := f.Mul(g)
			assert.NoError(t, err, "the truncated product must be in the domain")
			assert.Equal(t, truncated, fixed.Max, "the truncated product must be Max")

			_, err = f.MulAway(g)
			assert.ErrorIs(t, err, fixed.ErrOverflow, "the step away from Max must overflow")
		})
	})

	t.Run("Div", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			f, g, want fixed.Fixed64
		}{
			{name: "returns the receiver for One", f: 12345 * fixed.One, g: fixed.One, want: 12345 * fixed.One},
			{name: "returns a half for One divided by two", f: fixed.One, g: 2 * fixed.One, want: fixed.One / 2},
			{name: "returns Zero for a receiver of Zero", f: fixed.Zero, g: fixed.Max, want: fixed.Zero},
			{
				name: "returns a negative quotient for a negative receiver",
				f:    -6 * fixed.One,
				g:    3 * fixed.One,
				want: -2 * fixed.One,
			},
			{
				name: "returns a negative quotient for a negative divisor",
				f:    6 * fixed.One,
				g:    -3 * fixed.One,
				want: -2 * fixed.One,
			},
			{
				name: "returns a positive quotient for two negative operands",
				f:    -6 * fixed.One,
				g:    -3 * fixed.One,
				want: 2 * fixed.One,
			},
			{name: "returns 0.33333333 for One divided by three", f: fixed.One, g: 3 * fixed.One, want: 33333333},
			{
				name: "returns -0.33333333 for minus One divided by three",
				f:    -fixed.One,
				g:    3 * fixed.One,
				want: -33333333,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tt.f.Div(tt.g)
				assert.NoError(t, err, "Div must succeed in the domain")
				assert.Equal(t, got, tt.want, "Div must round the quotient toward zero")
			})
		}

		t.Run("returns the quotient rounded toward zero or an error", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Div must round the quotient toward zero or return an error", func(c *prop.Case) {
				f, g := c.Draw(values, "f"), c.Draw(values, "g")
				got, err := f.Div(g)
				if g == fixed.Zero {
					assert.ErrorIs(c, err, fixed.ErrDivZero, "a divisor of Zero must return ErrDivZero")

					return
				}

				numerator := new(big.Int).Mul(big.NewInt(f.Raw()), scale)
				assertExact(c, got, err, quotient(numerator, big.NewInt(g.Raw()), false))
			})
		})

		t.Run("returns the operand of an exact product", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Div must undo a Mul by a whole count", func(c *prop.Case) {
				f := c.Draw(prop.Integer(fixed.Min/1000, fixed.Max/1000), "f")
				g := c.Draw(prop.Integer[fixed.Fixed64](1, 1000), "g") * fixed.One

				product, err := f.Mul(g)
				assert.NoError(c, err, "the product must be in the domain")
				back, err := product.Div(g)
				assert.NoError(c, err, "the quotient must be in the domain")
				assert.Equal(c, back, f, "an exact product must divide back to its operand")
			})
		})

		t.Run("returns ErrDivZero for a divisor of Zero", func(t *testing.T) {
			t.Parallel()
			got, err := fixed.One.Div(fixed.Zero)
			expect.ErrorIs(t, err, fixed.ErrDivZero, "a divisor of Zero must return ErrDivZero")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrDivZero must classify as Invalid")
			expect.Equal(t, got, fixed.Zero, "a refused division must return Zero")
		})

		t.Run("returns ErrOverflow for a quotient too wide for the 128-bit division", func(t *testing.T) {
			t.Parallel()
			_, err := fixed.Max.Div(fixed.Smallest)
			assert.ErrorIs(t, err, fixed.ErrOverflow, "a quotient past the 128-bit division must overflow")
		})

		t.Run("returns ErrOverflow for a quotient above Max", func(t *testing.T) {
			t.Parallel()
			_, err := fixed.Max.Div(fixed.One - fixed.Smallest)
			assert.ErrorIs(t, err, fixed.ErrOverflow, "a quotient of 64 bits above Max must overflow")
		})

		t.Run("returns ErrOverflow at the bound of bits.Div64", func(t *testing.T) {
			t.Parallel()
			// 184467440738 times 10^Scale has a high word of 1, which equals
			// the divisor, at which bits.Div64 panics.
			_, err := fixed.Fixed64(184467440738).Div(fixed.Smallest)
			assert.ErrorIs(t, err, fixed.ErrOverflow, "the bound of bits.Div64 must be an error")
		})
	})

	t.Run("DivAway", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			f, g, want fixed.Fixed64
		}{
			{name: "returns 0.33333334 for One divided by three", f: fixed.One, g: 3 * fixed.One, want: 33333334},
			{
				name: "returns -0.33333334 for minus One divided by three",
				f:    -fixed.One,
				g:    3 * fixed.One,
				want: -33333334,
			},
			{name: "returns an exact quotient unchanged", f: 6 * fixed.One, g: 3 * fixed.One, want: 2 * fixed.One},
			{name: "returns Zero for a receiver of Zero", f: fixed.Zero, g: fixed.Max, want: fixed.Zero},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tt.f.DivAway(tt.g)
				assert.NoError(t, err, "DivAway must succeed in the domain")
				assert.Equal(t, got, tt.want, "DivAway must round the quotient away from zero")
			})
		}

		t.Run("returns the quotient rounded away from zero or an error", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "DivAway must round the quotient away from zero or return an error", func(c *prop.Case) {
				f, g := c.Draw(values, "f"), c.Draw(values, "g")
				got, err := f.DivAway(g)
				if g == fixed.Zero {
					assert.ErrorIs(c, err, fixed.ErrDivZero, "a divisor of Zero must return ErrDivZero")

					return
				}

				numerator := new(big.Int).Mul(big.NewInt(f.Raw()), scale)
				assertExact(c, got, err, quotient(numerator, big.NewInt(g.Raw()), true))
			})
		})

		t.Run("returns ErrDivZero for a divisor of Zero", func(t *testing.T) {
			t.Parallel()
			_, err := fixed.One.DivAway(fixed.Zero)
			assert.ErrorIs(t, err, fixed.ErrDivZero, "a divisor of Zero must return ErrDivZero")
		})

		t.Run("returns ErrOverflow when the rounding step passes Max", func(t *testing.T) {
			t.Parallel()
			f, g := fixed.Fixed64(9223371944621055439), fixed.Fixed64(99999999)

			truncated, err := f.Div(g)
			assert.NoError(t, err, "the truncated quotient must be in the domain")
			assert.Equal(t, truncated, fixed.Max, "the truncated quotient must be Max")

			_, err = f.DivAway(g)
			assert.ErrorIs(t, err, fixed.ErrOverflow, "the step away from Max must overflow")
		})

		t.Run("returns Max when the rounding step ends at Max", func(t *testing.T) {
			t.Parallel()
			f, g := fixed.Fixed64(9223371944621055438), fixed.Fixed64(99999999)

			truncated, err := f.Div(g)
			assert.NoError(t, err, "the truncated quotient must be in the domain")
			assert.Equal(t, truncated, fixed.Max-1, "the truncated quotient must be one step below Max")

			got, err := f.DivAway(g)
			assert.NoError(t, err, "a step that ends at Max must succeed")
			assert.Equal(t, got, fixed.Max, "the step must end at Max")
		})
	})
}

// TestArithAllocs checks the allocation contract of the arithmetic.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestArithAllocs(t *testing.T) {
	f, g := fixed.Fixed64(1234567890), fixed.Fixed64(-987654321)

	t.Run("Add", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { got, _ = f.Add(g) }, 0, "Add must not allocate")
		assert.Equal(t, got, fixed.Fixed64(246913569), "the test must measure the sum")
	})

	t.Run("Sub", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { got, _ = f.Sub(g) }, 0, "Sub must not allocate")
		assert.Equal(t, got, fixed.Fixed64(2222222211), "the test must measure the difference")
	})

	t.Run("Mul", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { got, _ = f.Mul(g) }, 0, "Mul must not allocate")
		assert.Equal(t, got, fixed.Fixed64(-12193263111), "the test must measure the product")
	})

	t.Run("MulAway", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { got, _ = f.MulAway(g) }, 0, "MulAway must not allocate")
		assert.Equal(t, got, fixed.Fixed64(-12193263112), "the test must measure the product")
	})

	t.Run("Div", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { got, _ = f.Div(g) }, 0, "Div must not allocate")
		assert.Equal(t, got, fixed.Fixed64(-124999998), "the test must measure the quotient")
	})

	t.Run("DivAway", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { got, _ = f.DivAway(g) }, 0, "DivAway must not allocate")
		assert.Equal(t, got, fixed.Fixed64(-124999999), "the test must measure the quotient")
	})
}

// BenchmarkArith reports the cost of the arithmetic, and fails when an
// operation allocates.
func BenchmarkArith(b *testing.B) {
	f, g := fixed.Fixed64(1234567890), fixed.Fixed64(-987654321)

	b.Run("Add", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = f.Add(g)
		}

		assert.Equal(b, got, fixed.Fixed64(246913569), "the benchmark must measure the sum")
	})

	b.Run("Sub", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = f.Sub(g)
		}

		assert.Equal(b, got, fixed.Fixed64(2222222211), "the benchmark must measure the difference")
	})

	b.Run("Mul", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = f.Mul(g)
		}

		assert.Equal(b, got, fixed.Fixed64(-12193263111), "the benchmark must measure the product")
	})

	b.Run("MulAway", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = f.MulAway(g)
		}

		assert.Equal(b, got, fixed.Fixed64(-12193263112), "the benchmark must measure the product")
	})

	b.Run("Div", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = f.Div(g)
		}

		assert.Equal(b, got, fixed.Fixed64(-124999998), "the benchmark must measure the quotient")
	})

	b.Run("DivAway", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = f.DivAway(g)
		}

		assert.Equal(b, got, fixed.Fixed64(-124999999), "the benchmark must measure the quotient")
	})
}

// assertExact checks the result of an operation against want, its exact
// raw value. The operation returns want when want is in the domain, and
// Zero with ErrOverflow otherwise.
func assertExact(c *prop.Case, got fixed.Fixed64, err error, want *big.Int) {
	if !want.IsInt64() || want.Int64() == math.MinInt64 {
		assert.ErrorIs(c, err, fixed.ErrOverflow, "a result outside the domain must return ErrOverflow")
		assert.Equal(c, got, fixed.Zero, "an overflow must return Zero")

		return
	}

	assert.NoError(c, err, "a result in the domain must succeed")
	assert.Equal(c, got, fixed.Fixed64(want.Int64()), "the result must be the exact value")
}

// quotient returns num divided by den, rounded toward zero, or away from
// zero when away is true and the division leaves a remainder.
func quotient(num, den *big.Int, away bool) *big.Int {
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	if away && r.Sign() != 0 {
		q.Add(q, big.NewInt(int64(num.Sign()*den.Sign())))
	}

	return q
}
