// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package epoch_test

import (
	"encoding"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/epoch"
)

// The encoding interfaces this package promises to satisfy; a
// missing method is a build failure, which is where it belongs.
var (
	_ encoding.BinaryAppender    = epoch.Zero
	_ encoding.BinaryMarshaler   = epoch.Zero
	_ encoding.BinaryUnmarshaler = (*epoch.Epoch)(nil)
)

// refuseContract is the contract of refusesLength, which
// TestUnmarshalBinary and FuzzUnmarshalBinary check.
const refuseContract = "UnmarshalBinary must refuse every length but EpochSize"

func TestMarshalBinary(t *testing.T) {
	t.Parallel()

	t.Run("is eight bytes big-endian", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			in   epoch.Epoch
			want []byte
		}{
			// The zero Epoch has a wire form: a freshly-created
			// scope legitimately persists it as its watermark seed.
			"zero": {epoch.Zero, []byte{0, 0, 0, 0, 0, 0, 0, 0}},
			"one":  {1, []byte{0, 0, 0, 0, 0, 0, 0, 1}},
			"max": {
				^epoch.Epoch(0),
				[]byte{255, 255, 255, 255, 255, 255, 255, 255},
			},
			"mixed": {0x0102030405060708, []byte{1, 2, 3, 4, 5, 6, 7, 8}},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := tc.in.MarshalBinary()

				assert.NoError(t, err, "MarshalBinary must succeed")
				assert.Equal(t, got, tc.want,
					"the layout is a stable wire contract")
			})
		}
	})

	t.Run("AppendBinary appends the binary form to dst", func(t *testing.T) {
		t.Parallel()

		prop.ForAll(t, "AppendBinary must extend dst with the binary form of the epoch", func(c *prop.Case) {
			dst := c.Draw(prop.Bytes(prop.MaxSize(2*epoch.EpochSize)), "dst")
			e := c.Draw(prop.Of[epoch.Epoch](), "epoch")

			form, err := e.MarshalBinary()
			assert.NoError(c, err, "MarshalBinary must succeed")
			want := append(slices.Clip(dst), form...)

			got, err := e.AppendBinary(dst)
			assert.NoError(c, err, "AppendBinary must succeed")
			assert.Equal(c, got, want, "AppendBinary must keep dst and append the binary form")
		})
	})
}

func TestUnmarshalBinary(t *testing.T) {
	t.Parallel()

	t.Run("returns the epoch that MarshalBinary encodes", func(t *testing.T) {
		t.Parallel()

		prop.RoundTrip(t, epoch.Epoch.MarshalBinary, func(b []byte) (epoch.Epoch, error) {
			var e epoch.Epoch
			err := e.UnmarshalBinary(b)

			return e, err
		}, "UnmarshalBinary must undo MarshalBinary for every epoch")
	})

	t.Run("returns ErrSize for any length but EpochSize", func(t *testing.T) {
		t.Parallel()

		prop.ForAll(t, refuseContract, refusesLength)
	})
}

// FuzzUnmarshalBinary checks the contract of refusesLength on the inputs
// that a fuzzer finds.
func FuzzUnmarshalBinary(f *testing.F) {
	prop.Fuzz(f, refuseContract, refusesLength)
}

// TestBinaryAllocs checks the allocation contract of AppendBinary.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestBinaryAllocs(t *testing.T) {
	t.Run("AppendBinary", func(t *testing.T) {
		e := epoch.Epoch(123456789)
		dst := make([]byte, 0, epoch.EpochSize)

		var (
			got []byte
			err error
		)
		expect.MaxAllocs(t, func() { got, err = e.AppendBinary(dst[:0]) }, 0,
			"AppendBinary must not allocate when dst has room for the binary form")
		assert.NoError(t, err, "AppendBinary must succeed")
		assert.Length(t, got, epoch.EpochSize, "the test must measure the binary form")
	})
}

func BenchmarkBinary(b *testing.B) {
	b.Run("AppendBinary", func(b *testing.B) {
		e := epoch.Epoch(123456789)
		dst := make([]byte, 0, epoch.EpochSize)

		var (
			got []byte
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, err = e.AppendBinary(dst[:0])
		}

		assert.NoError(b, err, "AppendBinary must succeed")
		assert.Length(b, got, epoch.EpochSize, "the benchmark must measure the binary form")
	})
}

// refusesLength decodes an input of any length but EpochSize, up to twice
// EpochSize, into an epoch of 7. The decode must return ErrSize and leave
// the epoch unchanged.
func refusesLength(c *prop.Case) {
	data := c.Draw(prop.Bytes(prop.MaxSize(2*epoch.EpochSize)).Filter(func(b []byte) bool {
		return len(b) != epoch.EpochSize
	}), "data")

	got := epoch.Epoch(7)

	var err error
	assert.Pure(c, func() epoch.Epoch { return got }, func() { err = got.UnmarshalBinary(data) },
		"a refused decode must not modify the receiver")
	assert.ErrorIs(c, err, epoch.ErrSize, "a wrong-length input must be a decode error")
}
