// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package uuidtext_test

import (
	"bytes"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/internal/uuidtext"
)

// values generates the bytes of UUID values, the zero value included.
var values = prop.Bytes(prop.MinSize(id.Size128), prop.MaxSize(id.Size128)).Map(func(b []byte) [id.Size128]byte {
	return [id.Size128]byte(b)
})

// nonZeroValues generates the bytes of UUID values other than the zero value.
var nonZeroValues = values.Filter(func(b [id.Size128]byte) bool { return b != [id.Size128]byte{} })

func TestBinary(t *testing.T) {
	t.Parallel()

	t.Run("AppendBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the 16 bytes of a value to dst", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendBinary must keep dst and append the bytes of the value", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(id.Size128)), "dst")
				b := c.Draw(nonZeroValues, "value")
				want := append(slices.Clone(dst), b[:]...)

				assert.Equal(c, uuidtext.AppendBinary(dst, b), want, "AppendBinary must extend dst with the bytes")
			})
		})

		t.Run("returns dst unchanged for the zero value", func(t *testing.T) {
			t.Parallel()
			got := uuidtext.AppendBinary([]byte("ids/"), [id.Size128]byte{})
			assert.Equal(t, got, []byte("ids/"), "the binary form of the zero value must be no bytes")
		})
	})

	t.Run("DecodeBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value whose binary form AppendBinary appends", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(b [id.Size128]byte) ([]byte, error) {
				return uuidtext.AppendBinary(nil, b), nil
			}, func(data []byte) ([id.Size128]byte, error) {
				return uuidtext.DecodeBinary(data, sentinels)
			}, "DecodeBinary must invert AppendBinary", prop.Using(values))
		})

		t.Run("returns the zero value for no bytes", func(t *testing.T) {
			t.Parallel()
			got, err := uuidtext.DecodeBinary(nil, sentinels)
			assert.NoError(t, err, "DecodeBinary must accept no bytes")
			assert.Equal(t, got, [id.Size128]byte{}, "no bytes must decode to the zero value")
		})

		t.Run("returns Errors.Bytes for a length other than 0 and 16", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := uuidtext.DecodeBinary(bytes.Repeat([]byte{0x5a}, n), sentinels)

				return err
			}, errBytes, "DecodeBinary must refuse data of another length",
				prop.Using(prop.Integer(1, 2*id.Size128+1).Filter(func(n int) bool { return n != id.Size128 })),
				prop.Example(1), prop.Example(id.Size128-1), prop.Example(id.Size128+1))
		})

		t.Run("returns Errors.Bytes for 16 zero bytes", func(t *testing.T) {
			t.Parallel()
			_, err := uuidtext.DecodeBinary(make([]byte, id.Size128), sentinels)
			assert.ErrorIs(t, err, errBytes, "16 zero bytes must not decode, because the zero value encodes as none")
		})

		t.Run("returns a value that a later write to data leaves unchanged", func(t *testing.T) {
			t.Parallel()
			data := bytes.Repeat([]byte{0x88}, id.Size128)
			got, err := uuidtext.DecodeBinary(data, sentinels)
			assert.NoError(t, err, "DecodeBinary must accept 16 bytes")
			data[0] = 0
			assert.Equal(t, got[0], byte(0x88), "a write to data must not change the value")
		})
	})
}

// TestBinaryAllocs checks the allocation contracts of AppendBinary and
// DecodeBinary. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestBinaryAllocs(t *testing.T) {
	value := exampleBytes
	data := exampleBytes[:]

	t.Run("AppendBinary", func(t *testing.T) {
		buf := make([]byte, 0, id.Size128)
		expect.MaxAllocs(t, func() { buf = uuidtext.AppendBinary(buf[:0], value) }, 0,
			"AppendBinary must not allocate into a buffer with room")
		assert.Equal(t, buf, data, "the test must measure the binary form of the value")
	})

	t.Run("DecodeBinary", func(t *testing.T) {
		var (
			got [id.Size128]byte
			err error
		)
		expect.MaxAllocs(t, func() { got, err = uuidtext.DecodeBinary(data, sentinels) }, 0,
			"DecodeBinary must not allocate")
		assert.NoError(t, err, "the test must measure a decode that succeeds")
		assert.Equal(t, got, value, "the test must measure the decode of the value")
	})
}

// BenchmarkBinary reports the cost of AppendBinary and DecodeBinary. It fails
// when one of them allocates more than TestBinaryAllocs allows.
func BenchmarkBinary(b *testing.B) {
	value := exampleBytes
	data := exampleBytes[:]

	b.Run("AppendBinary", func(b *testing.B) {
		buf := make([]byte, 0, id.Size128)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			buf = uuidtext.AppendBinary(buf[:0], value)
		}

		assert.Equal(b, buf, data, "the benchmark must measure the binary form of the value")
	})

	b.Run("DecodeBinary", func(b *testing.B) {
		var (
			got [id.Size128]byte
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, err = uuidtext.DecodeBinary(data, sentinels)
		}

		assert.NoError(b, err, "the benchmark must measure a decode that succeeds")
		assert.Equal(b, got, value, "the benchmark must measure the decode of the value")
	})
}
