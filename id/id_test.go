// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package id_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
)

// validBytes generates byte strings of the three lengths of an ID.
var validBytes = prop.SampledFrom(id.Size128, id.Size160, id.Size256).Bind(func(n int) prop.Generator[[]byte] {
	return prop.Bytes(prop.MinSize(n), prop.MaxSize(n))
})

// ids generates IDs of every size, Zero included.
var ids = prop.SampledFrom(0, id.Size128, id.Size160, id.Size256).Bind(func(n int) prop.Generator[id.ID] {
	return prop.Bytes(prop.MinSize(n), prop.MaxSize(n)).Map(func(b []byte) id.ID {
		var i id.ID
		_ = i.UnmarshalBinary(b) // a length of an ID never fails

		return i
	})
})

// widths are an ID of each size and a second ID of the same size, for the
// benchmarks of the comparisons.
var widths = []struct {
	name string
	a, b id.ID
}{
	{name: "128", a: id.New128(fill128(0x42)), b: id.New128(fill128(0x43))},
	{name: "160", a: id.New160(fill160(0x42)), b: id.New160(fill160(0x43))},
	{name: "256", a: id.New256(fill256(0x42)), b: id.New256(fill256(0x43))},
}

func TestID(t *testing.T) {
	t.Parallel()

	sizes := []struct {
		name string
		give int
		want int
	}{
		{name: "Size128 is 16 bytes", give: id.Size128, want: 16},
		{name: "Size160 is 20 bytes", give: id.Size160, want: 20},
		{name: "Size256 is 32 bytes", give: id.Size256, want: 32},
		{name: "MaxSize is Size256", give: id.MaxSize, want: id.Size256},
	}
	for _, tt := range sizes {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.give, tt.want, "the size constant must keep its value")
		})
	}

	constructors := []struct {
		name  string
		size  int
		build func(b []byte) id.ID
	}{
		{name: "New128", size: id.Size128, build: func(b []byte) id.ID { return id.New128([id.Size128]byte(b)) }},
		{name: "New160", size: id.Size160, build: func(b []byte) id.ID { return id.New160([id.Size160]byte(b)) }},
		{name: "New256", size: id.Size256, build: func(b []byte) id.ID { return id.New256([id.Size256]byte(b)) }},
	}
	for _, tt := range constructors {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("returns an ID of the size of b", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.build(make([]byte, tt.size)).Size(), tt.size, "the ID must have the size of b")
			})

			t.Run("returns an ID of the bytes of b", func(t *testing.T) {
				t.Parallel()
				prop.Equal(t, func(b []byte) []byte { return tt.build(b).Bytes() }, func(b []byte) []byte { return b },
					"the ID must keep the bytes of b",
					prop.Using(prop.Bytes(prop.MinSize(tt.size), prop.MaxSize(tt.size))))
			})
		})
	}

	t.Run("FromBytes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the ID of bytes of the length of an ID", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "FromBytes must return the ID of b for a length of an ID", func(c *prop.Case) {
				b := c.Draw(validBytes, "b")

				got, err := id.FromBytes(b)
				assert.NoError(c, err, "FromBytes must accept the length")
				assert.Equal(c, got.Size(), len(b), "the size must be the length of b")
				assert.Equal(c, got.Bytes(), b, "the bytes must be the bytes of b")
			})
		})

		invalid := prop.Integer(0, 2*id.MaxSize).Filter(func(n int) bool {
			return n != id.Size128 && n != id.Size160 && n != id.Size256
		})

		t.Run("returns ErrSize for a length that no ID has", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := id.FromBytes(make([]byte, n))

				return err
			}, id.ErrSize, "a length that no ID has must be refused",
				prop.Using(invalid), prop.Example(0), prop.Example(id.Size128-1), prop.Example(18),
				prop.Example(id.Size256+1))
		})

		t.Run("returns Zero for a length that no ID has", func(t *testing.T) {
			t.Parallel()
			prop.True(t, func(n int) bool {
				got, _ := id.FromBytes(make([]byte, n))

				return got.IsZero()
			}, "a refused length must return Zero", prop.Using(invalid), prop.Example(0))
		})

		t.Run("returns an error of class Invalid for a length that no ID has", func(t *testing.T) {
			t.Parallel()
			_, err := id.FromBytes(make([]byte, 18))
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrSize must classify as Invalid")
		})

		t.Run("returns an ID that a later write to b leaves unchanged", func(t *testing.T) {
			t.Parallel()
			b := make([]byte, id.Size128)
			got, err := id.FromBytes(b)
			assert.NoError(t, err, "FromBytes must accept a valid length")
			b[0] = 0xFF
			assert.Equal(t, got.Bytes()[0], byte(0), "a write to b must not change the ID")
		})
	})

	t.Run("Size", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0 for Zero", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, id.Zero.Size(), 0, "Zero must have no active bytes")
		})
	})

	t.Run("Bytes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a slice that a write leaves the ID unchanged", func(t *testing.T) {
			t.Parallel()
			i := id.New128(fill128(0x42))
			b := i.Bytes()
			b[0] = 0
			assert.Equal(t, i.Bytes()[0], byte(0x42), "a write through the slice must not change the ID")
		})

		t.Run("returns no bytes for Zero", func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, id.Zero.Bytes(), "Zero must have no active bytes")
		})
	})

	t.Run("IsZero", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give id.ID
			want bool
		}{
			{name: "reports true for the zero value", give: id.ID{}, want: true},
			{name: "reports true for Zero", give: id.Zero, want: true},
			{name: "reports false for an ID of Size128", give: id.New128([id.Size128]byte{1}), want: false},
			{name: "reports false for an ID of Size160", give: id.New160([id.Size160]byte{2}), want: false},
			{name: "reports false for an ID of Size256", give: id.New256([id.Size256]byte{3}), want: false},
			{
				name: "reports false for an ID of Size128 whose bytes are all zero",
				give: id.New128([id.Size128]byte{}),
				want: false,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.IsZero(), tt.want, "IsZero must report whether the ID is Zero")
			})
		}
	})

	t.Run("Equal", func(t *testing.T) {
		t.Parallel()

		a := id.New128(fill128(0x42))
		tests := []struct {
			name string
			give id.ID
			want bool
		}{
			{name: "reports true for an ID with the same size and bytes", give: id.New128(fill128(0x42)), want: true},
			{name: "reports false for an ID with other bytes", give: id.New128(fill128(0x43)), want: false},
			{name: "reports false for an ID of another size", give: id.New160(fill160(0x42)), want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, a.Equal(tt.give), tt.want, "Equal must report whether the IDs match")
			})
		}
	})

	t.Run("Compare", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			a, b id.ID
			want int
		}{
			{name: "returns 0 for identical IDs", a: id.New128(fill128(0x42)), b: id.New128(fill128(0x42)), want: 0},
			{name: "returns -1 for smaller bytes", a: id.New128(fill128(0x01)), b: id.New128(fill128(0x02)), want: -1},
			{name: "returns 1 for larger bytes", a: id.New128(fill128(0x02)), b: id.New128(fill128(0x01)), want: 1},
			{
				name: "returns -1 for a shorter ID with a matching prefix",
				a:    id.New128([id.Size128]byte{}),
				b:    id.New160([id.Size160]byte{}),
				want: -1,
			},
			{
				name: "returns 1 for a longer ID with a matching prefix",
				a:    id.New160([id.Size160]byte{}),
				b:    id.New128([id.Size128]byte{}),
				want: 1,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.a.Compare(tt.b), tt.want, "Compare must order the IDs by their bytes")
			})
		}

		t.Run("orders IDs as bytes.Compare orders their bytes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Compare must order the active prefixes lexicographically", func(c *prop.Case) {
				a := c.Draw(ids, "a")
				b := c.Draw(ids, "b")
				assert.Equal(c, a.Compare(b), bytes.Compare(a.Bytes(), b.Bytes()), "Compare must order by the bytes")
			})
		})
	})

	t.Run("String", func(t *testing.T) {
		t.Parallel()

		t.Run("returns id: for Zero", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, id.Zero.String(), "id:", "Zero must encode as the bare prefix")
		})

		t.Run("returns the hexadecimal of a 128-bit ID after id:", func(t *testing.T) {
			t.Parallel()
			var b [id.Size128]byte
			b[0], b[1], b[2] = 0x01, 0x23, 0x45
			b[13], b[14], b[15] = 0xab, 0xcd, 0xef
			assert.Equal(t, id.New128(b).String(), "id:012345"+"00000000000000000000"+"abcdef",
				"String must encode the bytes in hexadecimal after the prefix")
		})

		t.Run("returns id: followed by the hexadecimal of the bytes", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, id.ID.String, func(i id.ID) string { return "id:" + hex.EncodeToString(i.Bytes()) },
				"String must write the prefix and the hexadecimal bytes", prop.Using(ids))
		})
	})
}

// TestIDAllocs checks the allocation contracts of the constructors and the
// methods of an ID. MaxAllocs counts the allocations of the whole process,
// so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestIDAllocs(t *testing.T) {
	raw128, raw160, raw256 := fill128(0x42), fill160(0x42), fill256(0x42)
	a, other := id.New128(fill128(0x42)), id.New128(fill128(0x43))

	t.Run("New128", func(t *testing.T) {
		var got id.ID
		expect.MaxAllocs(t, func() { got = id.New128(raw128) }, 0, "New128 must not allocate")
		assert.Equal(t, got.Size(), id.Size128, "the test must measure an ID of Size128")
	})

	t.Run("New160", func(t *testing.T) {
		var got id.ID
		expect.MaxAllocs(t, func() { got = id.New160(raw160) }, 0, "New160 must not allocate")
		assert.Equal(t, got.Size(), id.Size160, "the test must measure an ID of Size160")
	})

	t.Run("New256", func(t *testing.T) {
		var got id.ID
		expect.MaxAllocs(t, func() { got = id.New256(raw256) }, 0, "New256 must not allocate")
		assert.Equal(t, got.Size(), id.Size256, "the test must measure an ID of Size256")
	})

	t.Run("FromBytes", func(t *testing.T) {
		src := raw256[:]

		var (
			got id.ID
			err error
		)
		expect.MaxAllocs(t, func() { got, err = id.FromBytes(src) }, 0, "FromBytes must not allocate")
		assert.NoError(t, err, "the test must measure a length that FromBytes accepts")
		assert.Equal(t, got.Size(), id.Size256, "the test must measure an ID of Size256")
	})

	t.Run("Size", func(t *testing.T) {
		var n int
		expect.MaxAllocs(t, func() { n = a.Size() }, 0, "Size must not allocate")
		assert.Equal(t, n, id.Size128, "the test must measure the size of the ID")
	})

	t.Run("Bytes", func(t *testing.T) {
		var b []byte
		expect.MaxAllocs(t, func() { b = a.Bytes() }, 1,
			"Bytes must allocate only the copy that its slice escapes with")
		assert.Length(t, b, id.Size128, "the test must measure the bytes of the ID")
	})

	t.Run("IsZero", func(t *testing.T) {
		zero := true
		expect.MaxAllocs(t, func() { zero = a.IsZero() }, 0, "IsZero must not allocate")
		assert.False(t, zero, "the test must measure an ID that is not Zero")
	})

	t.Run("Equal", func(t *testing.T) {
		equal := true
		expect.MaxAllocs(t, func() { equal = a.Equal(other) }, 0, "Equal must not allocate")
		assert.False(t, equal, "the test must measure two IDs that differ")
	})

	t.Run("Compare", func(t *testing.T) {
		var order int
		expect.MaxAllocs(t, func() { order = a.Compare(other) }, 0, "Compare must not allocate")
		assert.Equal(t, order, -1, "the test must measure two IDs that differ")
	})

	t.Run("String", func(t *testing.T) {
		var s string
		expect.MaxAllocs(t, func() { s = a.String() }, 1, "String must allocate only the result")
		assert.Length(t, s, len("id:")+2*id.Size128, "the test must measure the string of the ID")
	})
}

// BenchmarkID reports the cost of the comparisons for each width of an ID,
// of Bytes, FromBytes and String, and fails when one allocates more than
// TestIDAllocs allows.
func BenchmarkID(b *testing.B) {
	b.Run("Equal", func(b *testing.B) {
		for _, w := range widths {
			b.Run(w.name, func(b *testing.B) {
				equal := true

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					equal = w.a.Equal(w.b)
				}

				assert.False(b, equal, "the benchmark must measure two IDs that differ")
			})
		}
	})

	b.Run("Compare", func(b *testing.B) {
		for _, w := range widths {
			b.Run(w.name, func(b *testing.B) {
				var order int

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					order = w.a.Compare(w.b)
				}

				assert.Equal(b, order, -1, "the benchmark must measure two IDs that differ")
			})
		}
	})

	b.Run("IsZero", func(b *testing.B) {
		for _, w := range widths {
			b.Run(w.name, func(b *testing.B) {
				zero := true

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					zero = w.a.IsZero()
				}

				assert.False(b, zero, "the benchmark must measure an ID that is not Zero")
			})
		}
	})

	b.Run("Bytes", func(b *testing.B) {
		a := id.New128(fill128(0x42))
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got = a.Bytes()
		}

		assert.Length(b, got, id.Size128, "the benchmark must measure the bytes of the ID")
	})

	b.Run("FromBytes", func(b *testing.B) {
		raw := fill256(0x7f)
		src := raw[:]

		var (
			got id.ID
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, err = id.FromBytes(src)
		}

		assert.NoError(b, err, "the benchmark must measure a length that FromBytes accepts")
		assert.Equal(b, got.Size(), id.Size256, "the benchmark must measure an ID of Size256")
	})

	b.Run("String", func(b *testing.B) {
		a := id.New128(fill128(0x42))
		var s string

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			s = a.String()
		}

		assert.Length(b, s, len("id:")+2*id.Size128, "the benchmark must measure the string of the ID")
	})
}

// fill128 returns 16 bytes of b.
func fill128(b byte) [id.Size128]byte {
	var out [id.Size128]byte
	for i := range out {
		out[i] = b
	}

	return out
}

// fill160 returns 20 bytes of b.
func fill160(b byte) [id.Size160]byte {
	var out [id.Size160]byte
	for i := range out {
		out[i] = b
	}

	return out
}

// fill256 returns 32 bytes of b.
func fill256(b byte) [id.Size256]byte {
	var out [id.Size256]byte
	for i := range out {
		out[i] = b
	}

	return out
}
