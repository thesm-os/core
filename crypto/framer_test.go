// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
)

// framerBoundaryContract is the contract of framerBoundaries.
const framerBoundaryContract = "Bytes must change the frame when the boundary between two fields moves"

// framerDomain is the Domain of the frames whose tag the test does not
// pin.
var framerDomain = crypto.Domain{Name: "entry", Version: 1}

// framerPayload is the variable field of the allocation tests and the
// benchmarks.
var framerPayload = []byte("some variable width payload")

func TestFramer(t *testing.T) {
	t.Parallel()

	t.Run("NewFramer", func(t *testing.T) {
		t.Parallel()

		t.Run("writes the length of the name then the name then the version", func(t *testing.T) {
			t.Parallel()
			f := crypto.NewFramer(nil, crypto.Domain{Name: "ab", Version: 0x0102})
			assert.Equal(t, f.Frame(), []byte{
				0, 0, 0, 0, 0, 0, 0, 2, // uint64 len("ab")
				'a', 'b',
				0x01, 0x02, // uint16 version
			}, "the tag must match the documented layout")
		})

		t.Run("appends the tag to dst", func(t *testing.T) {
			t.Parallel()
			f := crypto.NewFramer([]byte{0xAA}, crypto.Domain{})
			assert.Equal(t, f.Frame(), []byte{0xAA, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
				"NewFramer must keep the bytes of dst")
		})

		tests := []struct {
			name string
			give crypto.Domain
			want crypto.Domain
		}{
			{
				name: "writes another tag for another name",
				give: crypto.Domain{Name: "entry", Version: 1},
				want: crypto.Domain{Name: "receipt", Version: 1},
			},
			{
				name: "writes another tag for a name that extends another name",
				give: crypto.Domain{Name: "audit-entry", Version: 1},
				want: crypto.Domain{Name: "audit-entry-v2-extended", Version: 1},
			},
			{
				name: "writes another tag for another version",
				give: crypto.Domain{Name: "entry", Version: 1},
				want: crypto.Domain{Name: "entry", Version: 2},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				a := crypto.NewFramer(nil, tt.give)
				b := crypto.NewFramer(nil, tt.want)
				assert.NotEqual(t, a.Frame(), b.Frame(), "two domains must give two tags")
			})
		}
	})

	t.Run("Fixed", func(t *testing.T) {
		t.Parallel()

		t.Run("appends p without a length", func(t *testing.T) {
			t.Parallel()
			f := crypto.NewFramer(nil, crypto.Domain{})
			before := len(f.Frame())
			f.Fixed([]byte("xyz"))
			assert.Equal(t, f.Frame()[before:], []byte("xyz"), "Fixed must append p verbatim")
		})
	})

	t.Run("Bytes", func(t *testing.T) {
		t.Parallel()

		t.Run("writes the length of p before p", func(t *testing.T) {
			t.Parallel()
			f := crypto.NewFramer(nil, crypto.Domain{})
			before := len(f.Frame())
			f.Bytes([]byte("xyz"))
			assert.Equal(t, f.Frame()[before:], []byte{0, 0, 0, 0, 0, 0, 0, 3, 'x', 'y', 'z'},
				"Bytes must prefix p with its length")
		})

		t.Run("writes a zero length for an empty p", func(t *testing.T) {
			t.Parallel()
			f := crypto.NewFramer(nil, crypto.Domain{})
			before := len(f.Frame())
			f.Bytes(nil)
			assert.Equal(t, f.Frame()[before:], []byte{0, 0, 0, 0, 0, 0, 0, 0},
				"an empty field must leave its length in the frame")
		})

		t.Run("writes another frame when the boundary between two fields moves", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, framerBoundaryContract, framerBoundaries)
		})
	})

	t.Run("String", func(t *testing.T) {
		t.Parallel()

		t.Run("writes the bytes that Bytes writes for the same content", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(s string) []byte {
				f := crypto.NewFramer(nil, framerDomain)
				f.String(s)

				return f.Frame()
			}, func(s string) []byte {
				f := crypto.NewFramer(nil, framerDomain)
				f.Bytes([]byte(s))

				return f.Frame()
			}, "String must frame s as Bytes frames its bytes",
				prop.Using(prop.String(prop.MaxSize(64))), prop.Example(""), prop.Example("hello"))
		})
	})

	t.Run("Uint64", func(t *testing.T) {
		t.Parallel()

		t.Run("writes v as 8 big-endian bytes", func(t *testing.T) {
			t.Parallel()
			f := crypto.NewFramer(nil, crypto.Domain{})
			before := len(f.Frame())
			f.Uint64(0x0102030405060708)
			assert.Equal(t, f.Frame()[before:], []byte{1, 2, 3, 4, 5, 6, 7, 8},
				"Uint64 must write v big-endian without a length")
		})
	})

	t.Run("Uint32", func(t *testing.T) {
		t.Parallel()

		t.Run("writes v as 4 big-endian bytes", func(t *testing.T) {
			t.Parallel()
			f := crypto.NewFramer(nil, crypto.Domain{})
			before := len(f.Frame())
			f.Uint32(0x01020304)
			assert.Equal(t, f.Frame()[before:], []byte{1, 2, 3, 4},
				"Uint32 must write v big-endian without a length")
		})
	})

	t.Run("Frame", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a slice that aliases dst", func(t *testing.T) {
			t.Parallel()
			buf := make([]byte, 0, 64)
			f := crypto.NewFramer(buf, framerDomain)
			assert.Equal(t, &f.Frame()[0], &buf[:1][0], "Frame must return the array of dst", assert.ByIdentity())
		})
	})
}

// TestFramerAllocs checks the allocation contract of each method in a
// buffer with capacity, and the growth of a buffer that NewFramer must
// enlarge. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestFramerAllocs(t *testing.T) {
	buf := make([]byte, 0, 256)

	t.Run("NewFramer", func(t *testing.T) {
		tests := []struct {
			name  string
			frame func() []byte
			want  uint64
		}{
			{
				name:  "does not allocate in a buffer with capacity",
				frame: func() []byte { f := crypto.NewFramer(buf[:0], framerDomain); return f.Frame() },
				want:  0,
			},
			{
				name:  "allocates once in a buffer that make sizes",
				frame: func() []byte { f := crypto.NewFramer(make([]byte, 0, 128), framerDomain); return f.Frame() },
				want:  1,
			},
			{
				name:  "allocates twice in a nil buffer",
				frame: func() []byte { f := crypto.NewFramer(nil, framerDomain); return f.Frame() },
				want:  2,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var got []byte
				expect.MaxAllocs(t, func() { got = tt.frame() }, tt.want, "NewFramer must keep its ceiling")
				assert.NotEmpty(t, got, "the test must measure a frame")
			})
		}
	})

	tests := []struct {
		name  string
		frame func() []byte
	}{
		{
			name:  "Fixed",
			frame: func() []byte { f := crypto.NewFramer(buf[:0], framerDomain); f.Fixed(framerPayload); return f.Frame() },
		},
		{
			name:  "Bytes",
			frame: func() []byte { f := crypto.NewFramer(buf[:0], framerDomain); f.Bytes(framerPayload); return f.Frame() },
		},
		{
			name:  "String",
			frame: func() []byte { f := crypto.NewFramer(buf[:0], framerDomain); f.String("payload"); return f.Frame() },
		},
		{
			name:  "Uint64",
			frame: func() []byte { f := crypto.NewFramer(buf[:0], framerDomain); f.Uint64(42); return f.Frame() },
		},
		{
			name:  "Uint32",
			frame: func() []byte { f := crypto.NewFramer(buf[:0], framerDomain); f.Uint32(42); return f.Frame() },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []byte
			expect.MaxAllocs(t, func() { got = tt.frame() }, 0, "a field must not allocate in a buffer with capacity")
			assert.NotEmpty(t, got, "the test must measure a frame")
		})
	}
}

// BenchmarkFramer reports the cost of NewFramer in a reused, a sized and a
// nil buffer, and of each field in a reused buffer, and fails when one of
// them allocates more than TestFramerAllocs allows.
func BenchmarkFramer(b *testing.B) {
	buf := make([]byte, 0, 256)

	b.Run("NewFramer", func(b *testing.B) {
		tests := []struct {
			name string
			dst  func() []byte
			want uint64
		}{
			{name: "in a reused buffer", dst: func() []byte { return buf[:0] }, want: 0},
			{name: "in a sized buffer", dst: func() []byte { return make([]byte, 0, 128) }, want: 1},
			{name: "in a nil buffer", dst: func() []byte { return nil }, want: 2},
		}
		for _, tt := range tests {
			b.Run(tt.name, func(b *testing.B) {
				var got []byte

				c := bench.Start(b).MaxAllocs(tt.want)
				defer c.End()

				for c.Loop() {
					f := crypto.NewFramer(tt.dst(), framerDomain)
					got = f.Frame()
				}

				assert.NotEmpty(b, got, "the benchmark must measure a frame")
			})
		}
	})

	tests := []struct {
		name   string
		append func(f *crypto.Framer)
	}{
		{name: "Fixed", append: func(f *crypto.Framer) { f.Fixed(framerPayload) }},
		{name: "Bytes", append: func(f *crypto.Framer) { f.Bytes(framerPayload) }},
		{name: "String", append: func(f *crypto.Framer) { f.String("payload") }},
		{name: "Uint64", append: func(f *crypto.Framer) { f.Uint64(42) }},
		{name: "Uint32", append: func(f *crypto.Framer) { f.Uint32(42) }},
	}
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			var f crypto.Framer
			var got []byte

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				f = crypto.NewFramer(buf[:0], framerDomain)
				tt.append(&f)
				got = f.Frame()
			}

			assert.NotEmpty(b, got, "the benchmark must measure a frame")
		})
	}
}

// framerBoundaries checks that two splits of a drawn field into two
// adjacent fields, at two drawn offsets, give two frames.
func framerBoundaries(c *prop.Case) {
	field := c.Draw(prop.Bytes(prop.MinSize(1), prop.MaxSize(32)), "field")
	i := c.Draw(prop.Integer(0, len(field)-1), "first boundary")
	j := c.Draw(prop.Integer(i+1, len(field)), "second boundary")
	a := crypto.NewFramer(nil, framerDomain)
	a.Bytes(field[:i])
	a.Bytes(field[i:])
	b := crypto.NewFramer(nil, framerDomain)
	b.Bytes(field[:j])
	b.Bytes(field[j:])
	assert.NotEqual(c, a.Frame(), b.Frame(), "a moved boundary must change the frame")
}
