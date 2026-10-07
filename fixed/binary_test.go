// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed_test

import (
	"bytes"
	"encoding"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/fixed"
)

// The binary interfaces that Fixed64 implements. A missing method fails
// the build.
var (
	_ encoding.BinaryAppender    = fixed.Zero
	_ encoding.BinaryMarshaler   = fixed.Zero
	_ encoding.BinaryUnmarshaler = (*fixed.Fixed64)(nil)
)

// decodeContract is the contract of decodesCanonically, which TestBinary
// and FuzzUnmarshalBinary check.
const decodeContract = "UnmarshalBinary must leave the receiver unchanged, or decode the value whose binary form is the input"

// binaryForms generates inputs of exactly Size octets, and inputs of any
// length up to twice Size.
var binaryForms = prop.OneOf(
	prop.Bytes(prop.MinSize(fixed.Size), prop.MaxSize(fixed.Size)),
	prop.Bytes(prop.MaxSize(2*fixed.Size)),
)

func TestBinary(t *testing.T) {
	t.Parallel()

	t.Run("AppendBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the binary form after dst", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendBinary must extend dst with the binary form of the value", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(2*fixed.Size)), "dst")
				f := c.Draw(values, "f")

				form, err := f.MarshalBinary()
				assert.NoError(c, err, "MarshalBinary must accept every value of the domain")
				want := append(slices.Clip(dst), form...)

				got, err := f.AppendBinary(dst)
				assert.NoError(c, err, "AppendBinary must accept every value of the domain")
				assert.Equal(c, got, want, "AppendBinary must keep dst and append the binary form")
			})
		})

		t.Run("returns dst unchanged with ErrRange for math.MinInt64", func(t *testing.T) {
			t.Parallel()
			got, err := outOfDomain.AppendBinary([]byte{0xaa})
			expect.ErrorIs(t, err, fixed.ErrRange, "AppendBinary must refuse the bypass value")
			expect.Equal(t, got, []byte{0xaa}, "a refused append must leave dst unchanged")
		})
	})

	t.Run("MarshalBinary", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give fixed.Fixed64
			want []byte
		}{
			{name: "encodes Zero as eight zero octets", give: fixed.Zero, want: []byte{0, 0, 0, 0, 0, 0, 0, 0}},
			{
				name: "encodes Smallest with a last octet of 1",
				give: fixed.Smallest,
				want: []byte{0, 0, 0, 0, 0, 0, 0, 1},
			},
			{
				name: "encodes minus Smallest as eight octets of 0xff",
				give: -fixed.Smallest, want: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			},
			{name: "encodes One as 0x05f5e100", give: fixed.One, want: []byte{0, 0, 0, 0, 0x05, 0xf5, 0xe1, 0x00}},
			{
				name: "encodes Max with a first octet of 0x7f",
				give: fixed.Max, want: []byte{0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			},
			{
				name: "encodes Min with a first octet of 0x80",
				give: fixed.Min, want: []byte{0x80, 0, 0, 0, 0, 0, 0, 1},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tt.give.MarshalBinary()
				assert.NoError(t, err, "MarshalBinary must accept every value of the domain")
				assert.Equal(t, got, tt.want, "the layout must be big-endian two's complement")
			})
		}

		t.Run("returns ErrRange for math.MinInt64", func(t *testing.T) {
			t.Parallel()
			_, err := outOfDomain.MarshalBinary()
			assert.ErrorIs(t, err, fixed.ErrRange, "no value outside the domain may reach the wire")
		})

		t.Run("encodes a negative value to sort above a positive one", func(t *testing.T) {
			t.Parallel()
			negative, err := (-fixed.One).MarshalBinary()
			assert.NoError(t, err, "MarshalBinary must accept minus One")
			positive, err := fixed.One.MarshalBinary()
			assert.NoError(t, err, "MarshalBinary must accept One")
			assert.Equal(t, bytes.Compare(negative, positive), 1,
				"the binary form of minus One must sort above that of One")
		})
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value that MarshalBinary encodes", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, fixed.Fixed64.MarshalBinary, func(form []byte) (fixed.Fixed64, error) {
				var f fixed.Fixed64
				err := f.UnmarshalBinary(form)

				return f, err
			}, "UnmarshalBinary must undo MarshalBinary for every value of the domain", prop.Using(values))
		})

		t.Run("returns ErrSize for any length but Size", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalBinary must refuse every length but Size", func(c *prop.Case) {
				data := c.Draw(prop.Bytes(prop.MaxSize(2*fixed.Size)).Filter(func(b []byte) bool {
					return len(b) != fixed.Size
				}), "data")

				got := fixed.One

				var err error
				assert.Pure(c, func() fixed.Fixed64 { return got }, func() { err = got.UnmarshalBinary(data) },
					"a refused input must leave the receiver unchanged")
				assert.ErrorIs(c, err, fixed.ErrSize, "an input of another length must return ErrSize")
			})
		})

		t.Run("returns ErrSize that classifies as Invalid", func(t *testing.T) {
			t.Parallel()
			var got fixed.Fixed64
			err := got.UnmarshalBinary(nil)
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrSize must classify as Invalid")
		})

		t.Run("returns ErrRange for the binary form of math.MinInt64", func(t *testing.T) {
			t.Parallel()
			got := fixed.One

			var err error
			assert.Pure(t, func() fixed.Fixed64 { return got }, func() {
				err = got.UnmarshalBinary([]byte{0x80, 0, 0, 0, 0, 0, 0, 0})
			}, "a refused input must leave the receiver unchanged")
			assert.ErrorIs(t, err, fixed.ErrRange, "the binary form of the bypass value must return ErrRange")
		})

		t.Run("decodes an input to the value whose binary form it is", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, decodeContract, decodesCanonically)
		})
	})
}

// FuzzUnmarshalBinary checks the contract of decodesCanonically on the
// inputs that a fuzzer finds.
func FuzzUnmarshalBinary(f *testing.F) {
	prop.Fuzz(f, decodeContract, decodesCanonically)
}

// TestBinaryAllocs checks the allocation contract of the binary methods.
// MarshalBinary allocates its result. MaxAllocs counts the allocations of
// the whole process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestBinaryAllocs(t *testing.T) {
	f := fixed.Fixed64(-1234567890)
	form := []byte{0xff, 0xff, 0xff, 0xff, 0xb6, 0x69, 0xfd, 0x2e}
	dst := make([]byte, 0, fixed.Size)

	t.Run("AppendBinary", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got, _ = f.AppendBinary(dst[:0]) }, 0,
			"AppendBinary must not allocate when dst has room for the binary form")
		assert.Equal(t, got, form, "the test must measure the binary form")
	})

	t.Run("MarshalBinary", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got, _ = f.MarshalBinary() }, 1, "MarshalBinary must allocate its result alone")
		assert.Equal(t, got, form, "the test must measure the binary form")
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		var got fixed.Fixed64
		expect.MaxAllocs(t, func() { _ = got.UnmarshalBinary(form) }, 0, "UnmarshalBinary must not allocate")
		assert.Equal(t, got, f, "the test must measure the value of the binary form")
	})
}

// BenchmarkBinary reports the cost of the binary methods, and fails when
// one allocates more than its contract states.
func BenchmarkBinary(b *testing.B) {
	f := fixed.Fixed64(-1234567890)
	form := []byte{0xff, 0xff, 0xff, 0xff, 0xb6, 0x69, 0xfd, 0x2e}
	dst := make([]byte, 0, fixed.Size)

	b.Run("AppendBinary", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = f.AppendBinary(dst[:0])
		}

		assert.Equal(b, got, form, "the benchmark must measure the binary form")
	})

	b.Run("MarshalBinary", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = f.MarshalBinary()
		}

		assert.Equal(b, got, form, "the benchmark must measure the binary form")
	})

	b.Run("UnmarshalBinary", func(b *testing.B) {
		var got fixed.Fixed64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_ = got.UnmarshalBinary(form)
		}

		assert.Equal(b, got, f, "the benchmark must measure the value of the binary form")
	})
}

// decodesCanonically decodes an input that the case draws into a receiver
// of One. A refused input leaves the receiver at One, and a decoded value
// encodes to the input.
func decodesCanonically(c *prop.Case) {
	data := c.Draw(binaryForms, "data")

	got := fixed.One
	if err := got.UnmarshalBinary(data); err != nil {
		assert.Equal(c, got, fixed.One, "a refused input must leave the receiver unchanged")

		return
	}

	form, err := got.MarshalBinary()
	assert.NoError(c, err, "a decoded value must be in the domain")
	assert.Equal(c, form, data, "a decoded value must encode to the input")
}
