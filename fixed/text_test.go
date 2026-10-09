// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package fixed_test

import (
	"encoding"
	"encoding/json"
	"math/big"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/fixed"
)

// The text interfaces that Fixed64 implements. A missing method fails the
// build.
var (
	_ encoding.TextAppender    = fixed.Zero
	_ encoding.TextMarshaler   = fixed.Zero
	_ encoding.TextUnmarshaler = (*fixed.Fixed64)(nil)
)

// parseContract is the contract of parsesExactly, which TestText and
// FuzzParse check.
const parseContract = "Parse must return Zero with an error, or the exact value of the text"

// texts generates texts of the grammar of Parse, with up to 13 whole
// digits and 12 fraction digits, and strings of any runes.
var texts = prop.OneOf(
	prop.StringMatching(`-?[0-9]{1,13}(\.[0-9]{1,12})?`),
	prop.String(),
)

// payload is a JSON document with a field of Fixed64.
type payload struct {
	Amount fixed.Fixed64 `json:"amount"`
}

func TestText(t *testing.T) {
	t.Parallel()

	t.Run("Parse", func(t *testing.T) {
		t.Parallel()

		valid := []struct {
			name string
			give string
			want fixed.Fixed64
		}{
			{name: "returns One for 1", give: "1", want: fixed.One},
			{name: "returns Zero for 0", give: "0", want: fixed.Zero},
			{name: "returns Zero for -0", give: "-0", want: fixed.Zero},
			{name: "returns Zero for -0.00000000", give: "-0.00000000", want: fixed.Zero},
			{name: "returns One for 1.00000000", give: "1.00000000", want: fixed.One},
			{name: "returns Smallest for 0.00000001", give: "0.00000001", want: fixed.Smallest},
			{name: "returns minus One for -1.00000000", give: "-1.00000000", want: -fixed.One},
			{name: "returns 1.5 for a short fraction", give: "1.5", want: fixed.One + fixed.One/2},
			{name: "returns Max for its text", give: "92233720368.54775807", want: fixed.Max},
			{name: "returns Min for its text", give: "-92233720368.54775807", want: fixed.Min},
			{name: "returns 7 for 007", give: "007", want: 7 * fixed.One},
			{name: "returns the largest whole count", give: "92233720368", want: 9223372036800000000},
			{name: "returns the largest negative whole count", give: "-92233720368", want: -9223372036800000000},
			{name: "returns One for an insignificant ninth place", give: "1.000000000", want: fixed.One},
			{name: "returns One for six insignificant places", give: "1.00000000000000", want: fixed.One},
			{name: "returns 12.3456789 for its text", give: "12.34567890", want: 1234567890},
			{name: "returns 0.1 for one tenth", give: "0.1", want: 10000000},
		}
		for _, tt := range valid {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := fixed.Parse(tt.give)
				assert.NoError(t, err, "Parse must accept the grammar")
				assert.Equal(t, got, tt.want, "Parse must return the value of the text")
			})
		}

		syntax := []struct {
			name string
			give string
		}{
			{name: "returns ErrSyntax for an empty string", give: ""},
			{name: "returns ErrSyntax for a lone minus sign", give: "-"},
			{name: "returns ErrSyntax for a leading plus sign", give: "+1"},
			{name: "returns ErrSyntax for a missing whole part", give: ".5"},
			{name: "returns ErrSyntax for a missing fraction", give: "1."},
			{name: "returns ErrSyntax for two points", give: "1.2.3"},
			{name: "returns ErrSyntax for an exponent", give: "1e3"},
			{name: "returns ErrSyntax for an exponent after a fraction", give: "1.5e3"},
			{name: "returns ErrSyntax for an underscore", give: "1_000"},
			{name: "returns ErrSyntax for a leading space", give: " 1"},
			{name: "returns ErrSyntax for a trailing space", give: "1 "},
			{name: "returns ErrSyntax for letters", give: "abc"},
			{name: "returns ErrSyntax for a hexadecimal prefix", give: "0x10"},
			{name: "returns ErrSyntax for a minus sign between digits", give: "1-2"},
			{name: "returns ErrSyntax for a trailing minus sign", give: "1-"},
			{name: "returns ErrSyntax for two minus signs", give: "--1"},
			{name: "returns ErrSyntax for a comma between digit groups", give: "1,000"},
			{name: "returns ErrSyntax for Inf", give: "Inf"},
			{name: "returns ErrSyntax for NaN", give: "NaN"},
		}
		for _, tt := range syntax {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := fixed.Parse(tt.give)
				expect.ErrorIs(t, err, fixed.ErrSyntax, "Parse must refuse a text outside the grammar")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrSyntax must classify as Invalid")
				expect.Equal(t, got, fixed.Zero, "a refused text must return Zero")
			})
		}

		precision := []struct {
			name string
			give string
		}{
			{name: "returns ErrPrecision for a digit in the ninth place", give: "0.000000001"},
			{name: "returns ErrPrecision for a digit far past the scale", give: "1.00000000000001"},
			{name: "returns ErrPrecision for a digit before trailing zeroes", give: "1.0000000010"},
		}
		for _, tt := range precision {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := fixed.Parse(tt.give)
				expect.ErrorIs(t, err, fixed.ErrPrecision, "Parse must not truncate a significant digit")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrPrecision must classify as Invalid")
				expect.Equal(t, got, fixed.Zero, "a refused text must return Zero")
			})
		}

		outOfRange := []struct {
			name string
			give string
		}{
			{name: "returns ErrRange for one raw unit past Max", give: "92233720368.54775808"},
			{name: "returns ErrRange for one raw unit past Min", give: "-92233720368.54775808"},
			{name: "returns ErrRange for a whole part above the largest count", give: "92233720369"},
			{name: "returns ErrRange for a magnitude far past Max", give: "99999999999999999999999999999999"},
			{name: "returns ErrRange for a magnitude far past Min", give: "-99999999999999999999999999999999"},
			// One digit past the largest whole count, the scaled value
			// wraps a uint64 and falls under the bound of the domain, so a
			// parser that checks the count one digit late returns
			// 3.52241920 for the first text.
			{name: "returns ErrRange for a whole part that wraps a uint64 under the bound", give: "922337203689"},
			{name: "returns ErrRange for a negative whole part that wraps a uint64", give: "-922337203689"},
			{name: "returns ErrRange for a whole part that wraps a uint64 to a large value", give: "922337203680"},
		}
		for _, tt := range outOfRange {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := fixed.Parse(tt.give)
				expect.ErrorIs(t, err, fixed.ErrRange, "Parse must refuse a magnitude past the domain")
				expect.Equal(t, got, fixed.Zero, "a refused text must return Zero")
			})
		}

		t.Run("returns the exact value of a text that it accepts", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, parseContract, parsesExactly)
		})

		t.Run("returns the value of the text that String renders", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(f fixed.Fixed64) (string, error) {
				return f.String(), nil
			}, fixed.Parse, "Parse must undo String for every value of the domain", prop.Using(values))
		})
	})

	t.Run("String", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give fixed.Fixed64
			want string
		}{
			{name: "renders One at eight places", give: fixed.One, want: "1.00000000"},
			{name: "renders Zero at eight places", give: fixed.Zero, want: "0.00000000"},
			{name: "renders Smallest in the eighth place", give: fixed.Smallest, want: "0.00000001"},
			{name: "renders minus One with a minus sign", give: -fixed.One, want: "-1.00000000"},
			{name: "renders minus Smallest with a minus sign", give: -fixed.Smallest, want: "-0.00000001"},
			{name: "renders Max", give: fixed.Max, want: "92233720368.54775807"},
			{name: "renders Min", give: fixed.Min, want: "-92233720368.54775807"},
			{name: "renders the trailing zero of 12.3456789", give: 1234567890, want: "12.34567890"},
			{name: "pads the fraction of 1.00000001", give: 100000001, want: "1.00000001"},
			{name: "renders a whole count with eight zeroes", give: 5 * fixed.One, want: "5.00000000"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.String(), tt.want, "String must render every place")
			})
		}

		t.Run("renders math.MinInt64", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, outOfDomain.String(), "-92233720368.54775808",
				"String must render the bypass value for a diagnostic")
		})
	})

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the text of String after dst", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendText must extend dst with the text of String", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(32)), "dst")
				f := c.Draw(values, "f")
				want := append(slices.Clip(dst), f.String()...)

				got, err := f.AppendText(dst)
				assert.NoError(c, err, "AppendText must accept every value of the domain")
				assert.Equal(c, got, want, "AppendText must keep dst and append the text")
			})
		})

		t.Run("returns dst unchanged with ErrRange for math.MinInt64", func(t *testing.T) {
			t.Parallel()
			got, err := outOfDomain.AppendText([]byte("value="))
			expect.ErrorIs(t, err, fixed.ErrRange, "AppendText must refuse the bypass value")
			expect.Equal(t, string(got), "value=", "a refused append must leave dst unchanged")
		})
	})

	t.Run("MarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the text of String", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "MarshalText must return the text of String", func(c *prop.Case) {
				f := c.Draw(values, "f")
				got, err := f.MarshalText()
				assert.NoError(c, err, "MarshalText must accept every value of the domain")
				assert.Equal(c, string(got), f.String(), "MarshalText must return the text of String")
			})
		})

		t.Run("returns ErrRange for math.MinInt64", func(t *testing.T) {
			t.Parallel()
			_, err := outOfDomain.MarshalText()
			assert.ErrorIs(t, err, fixed.ErrRange, "MarshalText must not emit a value outside the domain")
		})

		t.Run("makes json.Marshal write a JSON string", func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(fixed.Fixed64(1234567890))
			assert.NoError(t, err, "json.Marshal must succeed")
			assert.Equal(t, string(got), `"12.34567890"`, "a Fixed64 must encode as a JSON string")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("decodes the text of MarshalText", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, fixed.Fixed64.MarshalText, func(text []byte) (fixed.Fixed64, error) {
				var f fixed.Fixed64
				err := f.UnmarshalText(text)

				return f, err
			}, "UnmarshalText must undo MarshalText for every value of the domain", prop.Using(values))
		})

		tests := []struct {
			name string
			give string
			want error
		}{
			{name: "returns ErrSyntax for a text outside the grammar", give: "nope", want: fixed.ErrSyntax},
			{
				name: "returns ErrPrecision for a digit in the ninth place",
				give: "0.000000001",
				want: fixed.ErrPrecision,
			},
			{
				name: "returns ErrRange for a whole part above the largest count",
				give: "92233720369",
				want: fixed.ErrRange,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got := fixed.One

				var err error
				assert.Pure(t, func() fixed.Fixed64 { return got }, func() { err = got.UnmarshalText([]byte(tt.give)) },
					"a refused text must leave the receiver unchanged")
				assert.ErrorIs(t, err, tt.want, "UnmarshalText must return the error of Parse")
			})
		}

		t.Run("decodes a JSON field that json.Marshal writes", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(f fixed.Fixed64) ([]byte, error) {
				return json.Marshal(payload{Amount: f})
			}, func(document []byte) (fixed.Fixed64, error) {
				var p payload
				err := json.Unmarshal(document, &p)

				return p.Amount, err //nolint:wrapcheck // the decoder's own error is the failure
			}, "json.Unmarshal must undo json.Marshal of a field", prop.Using(values))
		})

		t.Run("returns ErrPrecision through json.Unmarshal", func(t *testing.T) {
			t.Parallel()
			var got fixed.Fixed64
			err := json.Unmarshal([]byte(`"0.000000001"`), &got)
			assert.ErrorIs(t, err, fixed.ErrPrecision, "json.Unmarshal must return the error of Parse")
		})
	})
}

// FuzzParse checks the contract of parsesExactly on the inputs that a
// fuzzer finds.
func FuzzParse(f *testing.F) {
	prop.Fuzz(f, parseContract, parsesExactly)
}

// TestTextAllocs checks the allocation contract of the text methods.
// String and MarshalText allocate their result. MaxAllocs counts the
// allocations of the whole process, so the test does not run in parallel.
func TestTextAllocs(t *testing.T) {
	f := fixed.Fixed64(-1234567890)
	text := []byte("-12.34567890")
	dst := make([]byte, 0, 32)

	t.Run("Parse", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { got, _ = fixed.Parse("-12.34567890") }, 0, "Parse must not allocate")
		assert.Equal(t, got, f, "the test must measure the value of the text")
	})

	t.Run("String", func(t *testing.T) {
		var got string
		expect.MaxAllocs(t, func() { got = f.String() }, 1, "String must allocate its result alone")
		assert.Equal(t, got, string(text), "the test must measure the text of the value")
	})

	t.Run("AppendText", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got, _ = f.AppendText(dst[:0]) }, 0,
			"AppendText must not allocate when dst has room for the text")
		assert.Equal(t, got, text, "the test must measure the text of the value")
	})

	t.Run("MarshalText", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got, _ = f.MarshalText() }, 1, "MarshalText must allocate its result alone")
		assert.Equal(t, got, text, "the test must measure the text of the value")
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { _ = got.UnmarshalText(text) }, 0, "UnmarshalText must not allocate")
		assert.Equal(t, got, f, "the test must measure the value of the text")
	})
}

// BenchmarkText reports the cost of the text methods, and fails when one
// allocates more than its contract states.
func BenchmarkText(b *testing.B) {
	f := fixed.Fixed64(-1234567890)
	text := []byte("-12.34567890")
	dst := make([]byte, 0, 32)

	b.Run("Parse", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = fixed.Parse("-12.34567890")
		}

		assert.Equal(b, got, f, "the benchmark must measure the value of the text")
	})

	b.Run("String", func(b *testing.B) {
		var got string

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got = f.String()
		}

		assert.Equal(b, got, string(text), "the benchmark must measure the text of the value")
	})

	b.Run("AppendText", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = f.AppendText(dst[:0])
		}

		assert.Equal(b, got, text, "the benchmark must measure the text of the value")
	})

	b.Run("MarshalText", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = f.MarshalText()
		}

		assert.Equal(b, got, text, "the benchmark must measure the text of the value")
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_ = got.UnmarshalText(text)
		}

		assert.Equal(b, got, f, "the benchmark must measure the value of the text")
	})
}

// parsesExactly parses a text that the case draws. A refused text returns
// Zero, and an accepted text returns the value whose rational number at
// Scale places is the decimal of the text.
func parsesExactly(c *prop.Case) {
	s := c.Draw(texts, "text")

	got, err := fixed.Parse(s)
	if err != nil {
		assert.Equal(c, got, fixed.Zero, "a refused text must return Zero")

		return
	}

	want, ok := new(big.Rat).SetString(s)
	assert.True(c, ok, "an accepted text must be a decimal")
	assert.Equal(c, new(big.Rat).SetFrac(big.NewInt(got.Raw()), scale).RatString(), want.RatString(),
		"Parse must return the exact value of the text")
}
