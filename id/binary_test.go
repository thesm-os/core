// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package id_test

import (
	"bytes"
	"encoding"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.thesmos.sh/kanon/kanontest"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
)

// The encoding interfaces ID satisfies. A missing method is a build
// failure.
var (
	_ encoding.BinaryAppender    = id.Zero
	_ encoding.BinaryMarshaler   = id.Zero
	_ encoding.BinaryUnmarshaler = (*id.ID)(nil)
)

// shapes are an ID of every size, with the bytes the encoding must
// contain. The bytes are the recorded form of the frozen layout.
var shapes = []struct {
	name string
	id   id.ID
	want []byte
}{
	{name: "zero", id: id.Zero, want: []byte{}},
	{name: "Size128", id: id.New128(fill128(0x11)), want: bytes.Repeat([]byte{0x11}, id.Size128)},
	{name: "Size160", id: id.New160(fill160(0x22)), want: bytes.Repeat([]byte{0x22}, id.Size160)},
	{name: "Size256", id: id.New256(fill256(0x33)), want: bytes.Repeat([]byte{0x33}, id.Size256)},
}

func TestBinary(t *testing.T) {
	t.Parallel()

	t.Run("MarshalBinary", func(t *testing.T) {
		t.Parallel()

		for _, tt := range shapes {
			t.Run("returns the bytes of an ID of "+tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tt.id.MarshalBinary()
				assert.NoError(t, err, "MarshalBinary must succeed")
				assert.Equal(t, got, tt.want, "the encoding must be the bytes of the identifier")
			})
		}
	})

	t.Run("AppendBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the bytes of the ID to dst", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendBinary must keep dst and append the bytes of the ID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(id.MaxSize)), "dst")
				i := c.Draw(ids, "id")
				want := append(slices.Clone(dst), i.Bytes()...)

				got, err := i.AppendBinary(dst)
				assert.NoError(c, err, "AppendBinary must succeed")
				assert.Equal(c, got, want, "AppendBinary must extend dst with the bytes of the ID")
			})
		})
	})

	t.Run("AppendKanon", func(t *testing.T) {
		t.Parallel()

		t.Run("appends what AppendBinary appends", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendKanon must append the binary form of the ID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(id.MaxSize)), "dst")
				i := c.Draw(ids, "id")

				want, err := i.AppendBinary(slices.Clone(dst))
				assert.NoError(c, err, "AppendBinary must succeed")
				assert.Equal(c, i.AppendKanon(dst), want, "AppendKanon must equal AppendBinary")
			})
		})
	})

	t.Run("SizeKanon", func(t *testing.T) {
		t.Parallel()

		for _, tt := range shapes {
			t.Run("returns the length of the encoding of an ID of "+tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.id.SizeKanon(), len(tt.want), "SizeKanon must equal the length of the encoding")
			})
		}
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the ID that AppendBinary encodes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalBinary must decode the encoding of any ID over any ID", func(c *prop.Case) {
				prior := c.Draw(ids, "prior")
				want := c.Draw(ids, "id")

				got := prior
				assert.NoError(c, got.UnmarshalBinary(want.Bytes()), "UnmarshalBinary must accept the encoding")
				assert.Equal(c, got, want, "the decoded ID must equal the encoded one")
			})
		})

		t.Run("returns Zero for nil", func(t *testing.T) {
			t.Parallel()
			got := id.New128(fill128(0x66))
			assert.NoError(t, got.UnmarshalBinary(nil), "UnmarshalBinary must accept nil")
			assert.True(t, got.IsZero(), "nil must decode to the zero ID")
		})

		t.Run("returns the result of FromBytes for every nonempty length", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalBinary must agree with FromBytes on nonempty data", func(c *prop.Case) {
				data := c.Draw(prop.Bytes(prop.MinSize(1), prop.MaxSize(id.Size256+1)), "data")

				want, wantErr := id.FromBytes(data)
				var got id.ID
				err := got.UnmarshalBinary(data)
				assert.ErrorIs(c, err, wantErr, "UnmarshalBinary must return the error of FromBytes")
				assert.Equal(c, got, want, "UnmarshalBinary must decode the ID of FromBytes")
			})
		})

		t.Run("returns ErrSize for a length that no encoding has", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalBinary must refuse a length that no ID encodes to", func(c *prop.Case) {
				n := c.Draw(prop.Integer(1, 2*id.MaxSize).Filter(func(n int) bool {
					return n != id.Size128 && n != id.Size160 && n != id.Size256
				}), "length")

				got := id.New128(fill128(0x77))

				var err error
				assert.Pure(c, func() id.ID { return got }, func() { err = got.UnmarshalBinary(make([]byte, n)) },
					"a refused decode must leave the ID unchanged")
				assert.ErrorIs(c, err, id.ErrSize, "a wrong length must be a decode error")
			})
		})

		t.Run("returns an error of class Invalid for a length that no encoding has", func(t *testing.T) {
			t.Parallel()
			var got id.ID
			assert.Equal(t, errs.Classify(got.UnmarshalBinary(make([]byte, 18))), errs.Invalid,
				"ErrSize must classify as Invalid")
		})

		t.Run("returns an ID that a later write to data leaves unchanged", func(t *testing.T) {
			t.Parallel()
			data := bytes.Repeat([]byte{0x88}, id.Size128)
			var got id.ID
			assert.NoError(t, got.UnmarshalBinary(data), "UnmarshalBinary must accept a valid length")
			data[0] = 0
			assert.Equal(t, got.Bytes()[0], byte(0x88), "a write to data must not change the ID")
		})
	})
}

// TestExactKanon checks the two guarantees that ExactKanon declares with
// kanon's conformance suite. An ID without the methods of kanon.Exact does
// not compile here.
func TestExactKanon(t *testing.T) {
	t.Parallel()
	kanontest.RunExact[id.ID](t)
}

// TestBinaryAllocs checks the allocation contracts of the binary methods.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestBinaryAllocs(t *testing.T) {
	u := id.New256(fill256(0x7f))
	data := bytes.Repeat([]byte{0x7f}, id.Size256)

	t.Run("AppendBinary", func(t *testing.T) {
		buf := make([]byte, 0, id.MaxSize)

		var err error
		expect.MaxAllocs(t, func() { buf, err = u.AppendBinary(buf[:0]) }, 0,
			"AppendBinary must not allocate when dst has room for the ID")
		assert.NoError(t, err, "AppendBinary must succeed")
		assert.Length(t, buf, id.Size256, "the test must measure the encoding of the ID")
	})

	t.Run("AppendKanon", func(t *testing.T) {
		buf := make([]byte, 0, id.MaxSize)
		expect.MaxAllocs(t, func() { buf = u.AppendKanon(buf[:0]) }, 0,
			"AppendKanon must not allocate when dst has room for the ID")
		assert.Length(t, buf, id.Size256, "the test must measure the encoding of the ID")
	})

	t.Run("MarshalBinary", func(t *testing.T) {
		t.Run("allocates only the slice that it returns", func(t *testing.T) {
			var got []byte
			expect.MaxAllocs(t, func() { got, _ = u.MarshalBinary() }, 1,
				"MarshalBinary must allocate only the returned slice")
			assert.Length(t, got, id.Size256, "the test must measure the encoding of the ID")
		})

		t.Run("allocates nothing for Zero", func(t *testing.T) {
			got := []byte{0}
			expect.MaxAllocs(t, func() { got, _ = id.Zero.MarshalBinary() }, 0,
				"MarshalBinary must not allocate for Zero")
			assert.Empty(t, got, "the test must measure the encoding of Zero")
		})
	})

	t.Run("SizeKanon", func(t *testing.T) {
		var n int
		expect.MaxAllocs(t, func() { n = u.SizeKanon() }, 0, "SizeKanon must not allocate")
		assert.Equal(t, n, id.Size256, "the test must measure the size of the ID")
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		var (
			got id.ID
			err error
		)
		expect.MaxAllocs(t, func() { err = got.UnmarshalBinary(data) }, 0, "UnmarshalBinary must not allocate")
		assert.NoError(t, err, "the test must measure a decode that succeeds")
		assert.Equal(t, got, u, "the test must measure the decode of the ID")
	})
}

// BenchmarkBinary reports the cost of the binary methods of a 32-byte ID,
// and fails when one allocates more than TestBinaryAllocs allows.
func BenchmarkBinary(b *testing.B) {
	u := id.New256(fill256(0x7f))

	b.Run("AppendBinary", func(b *testing.B) {
		buf := make([]byte, 0, id.MaxSize)
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			buf, err = u.AppendBinary(buf[:0])
		}

		assert.NoError(b, err, "AppendBinary must succeed")
		assert.Length(b, buf, id.Size256, "the benchmark must measure the encoding of the ID")
	})

	b.Run("AppendKanon", func(b *testing.B) {
		buf := make([]byte, 0, id.MaxSize)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			buf = u.AppendKanon(buf[:0])
		}

		assert.Length(b, buf, id.Size256, "the benchmark must measure the encoding of the ID")
	})

	b.Run("MarshalBinary", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = u.MarshalBinary()
		}

		assert.Length(b, got, id.Size256, "the benchmark must measure the encoding of the ID")
	})

	b.Run("UnmarshalBinary", func(b *testing.B) {
		data := bytes.Repeat([]byte{0x7f}, id.Size256)

		var (
			got id.ID
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = got.UnmarshalBinary(data)
		}

		assert.NoError(b, err, "the benchmark must measure a decode that succeeds")
		assert.Equal(b, got, u, "the benchmark must measure the decode of the ID")
	})
}
