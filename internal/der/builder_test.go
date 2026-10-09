// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package der_test

import (
	"errors"
	"math"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/internal/der"
)

// errRefused is the error of the inverse of a round trip whose Reader or
// value decoder reported false.
var errRefused = errors.New("the decoder reported false")

func TestBuilder(t *testing.T) {
	t.Parallel()

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			length int
			header []byte
		}{
			{name: "appends a length of 127 in one octet", length: 127, header: []byte{0x04, 0x7f}},
			{
				name: "appends a length of 128 in the long form of one octet", length: 128,
				header: []byte{0x04, 0x81, 0x80},
			},
			{
				name: "appends a length of 255 in the long form of one octet", length: 255,
				header: []byte{0x04, 0x81, 0xff},
			},
			{
				name: "appends a length of 256 in the long form of two octets", length: 256,
				header: []byte{0x04, 0x82, 0x01, 0x00},
			},
			{
				name: "appends a length of 64 KiB in the long form of three octets", length: 1 << 16,
				header: []byte{0x04, 0x83, 0x01, 0x00, 0x00},
			},
			{
				name: "appends the two octets of a length of 0x1234 in big-endian order", length: 0x1234,
				header: []byte{0x04, 0x82, 0x12, 0x34},
			},
			{
				name: "appends the three octets of a length of 0x12345 in big-endian order", length: 0x12345,
				header: []byte{0x04, 0x83, 0x01, 0x23, 0x45},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				b := der.NewBuilder(nil)
				b.Add(der.TagOctetString, make([]byte, tt.length))

				got := b.Bytes()
				assert.Length(t, got, len(tt.header)+tt.length, "Add must append the header and the content")
				assert.Equal(t, got[:len(tt.header)], tt.header, "Add must append the tag and the DER length")
			})
		}

		t.Run("appends to the slice of the caller", func(t *testing.T) {
			t.Parallel()
			b := der.NewBuilder([]byte{0xee})
			b.Add(der.TagNull, nil)
			assert.Equal(t, b.Bytes(), []byte{0xee, 0x05, 0x00}, "Add must keep the octets before it")
		})
	})

	t.Run("AddElement", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the element as it is", func(t *testing.T) {
			t.Parallel()
			b := der.NewBuilder(nil)
			b.AddElement([]byte{0x05, 0x00})
			assert.Equal(t, b.Bytes(), []byte{0x05, 0x00}, "AddElement must copy the element")
		})
	})

	t.Run("AddUint64", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give uint64
			want []byte
		}{
			{name: "appends 0 as one zero octet", give: 0, want: []byte{0x02, 0x01, 0x00}},
			{name: "appends 127 as one octet", give: 127, want: []byte{0x02, 0x01, 0x7f}},
			{name: "appends 128 after a sign octet", give: 128, want: []byte{0x02, 0x02, 0x00, 0x80}},
			{name: "appends 255 after a sign octet", give: 255, want: []byte{0x02, 0x02, 0x00, 0xff}},
			{name: "appends 256 as two octets", give: 256, want: []byte{0x02, 0x02, 0x01, 0x00}},
			{
				name: "appends the largest uint64 after a sign octet", give: math.MaxUint64,
				want: []byte{0x02, 0x09, 0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				b := der.NewBuilder(nil)
				b.AddUint64(tt.give)
				assert.Equal(t, b.Bytes(), tt.want, "AddUint64 must append the DER INTEGER")
			})
		}

		t.Run("appends an INTEGER that Uint64 reads back", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(v uint64) ([]byte, error) {
				b := der.NewBuilder(nil)
				b.AddUint64(v)

				return b.Bytes(), nil
			}, func(element []byte) (uint64, error) {
				r := der.NewReader(element)
				content, isInteger := r.Read(der.TagInteger)
				v, fits := der.Uint64(content)
				if !isInteger || !fits {
					return 0, errRefused
				}

				return v, nil
			}, "Uint64 must read back the INTEGER that AddUint64 appends",
				prop.Using(prop.Integer[uint64](0, math.MaxUint64)))
		})
	})

	t.Run("AddBoolean", func(t *testing.T) {
		t.Parallel()

		t.Run("appends TRUE as 0xff", func(t *testing.T) {
			t.Parallel()
			b := der.NewBuilder(nil)
			b.AddBoolean(true)
			assert.Equal(t, b.Bytes(), []byte{0x01, 0x01, 0xff}, "AddBoolean must append 0xff for true")
		})

		t.Run("appends FALSE as 0x00", func(t *testing.T) {
			t.Parallel()
			b := der.NewBuilder(nil)
			b.AddBoolean(false)
			assert.Equal(t, b.Bytes(), []byte{0x01, 0x01, 0x00}, "AddBoolean must append 0x00 for false")
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			content int
			header  []byte
		}{
			{name: "writes a length of 0 into the placeholder", content: 0, header: []byte{0x30, 0x00}},
			{name: "writes a length of 127 into the placeholder", content: 127, header: []byte{0x30, 0x7f}},
			{name: "inserts one long-form octet for a length of 128", content: 128, header: []byte{0x30, 0x81, 0x80}},
			{name: "inserts one long-form octet for a length of 255", content: 255, header: []byte{0x30, 0x81, 0xff}},
			{
				name: "inserts two long-form octets for a length of 256", content: 256,
				header: []byte{0x30, 0x82, 0x01, 0x00},
			},
			{
				name: "inserts three long-form octets for a length of 64 KiB", content: 1 << 16,
				header: []byte{0x30, 0x83, 0x01, 0x00, 0x00},
			},
			{
				name: "inserts the two octets of a length of 0x1234 in big-endian order", content: 0x1234,
				header: []byte{0x30, 0x82, 0x12, 0x34},
			},
			{
				name: "inserts the three octets of a length of 0x12345 in big-endian order", content: 0x12345,
				header: []byte{0x30, 0x83, 0x01, 0x23, 0x45},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				content := make([]byte, tt.content)
				for i := range content {
					content[i] = byte(i)
				}

				b := der.NewBuilder(nil)
				at := b.Open(der.TagSequence)
				b.AddElement(content)
				b.Close(at)

				got := b.Bytes()
				assert.Length(t, got, len(tt.header)+tt.content, "Close must keep the header and the content")
				expect.Equal(t, got[:len(tt.header)], tt.header, "Close must write the DER length")
				expect.Equal(t, got[len(tt.header):], content, "Close must keep the content after the length")
			})
		}

		t.Run("writes the length that Add writes for the same content", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Open and Close must bracket the element that Add appends", func(c *prop.Case) {
				content := make([]byte, c.Draw(prop.Integer(0, 1<<17), "length"))
				for i := range content {
					content[i] = byte(i)
				}

				opened := der.NewBuilder(nil)
				at := opened.Open(der.TagSequence)
				opened.AddElement(content)
				opened.Close(at)

				added := der.NewBuilder(nil)
				added.Add(der.TagSequence, content)
				assert.Equal(c, opened.Bytes(), added.Bytes(), "Close must write the length that Add writes")
			})
		})

		t.Run("writes the lengths of nested elements", func(t *testing.T) {
			t.Parallel()
			b := der.NewBuilder(nil)
			outer := b.Open(der.TagSequence)
			inner := b.Open(der.TagSet)
			b.AddBoolean(true)
			b.Close(inner)
			b.Add(der.TagNull, nil)
			b.Close(outer)

			assert.Equal(t, b.Bytes(), []byte{0x30, 0x07, 0x31, 0x03, 0x01, 0x01, 0xff, 0x05, 0x00},
				"nested elements must close with their own lengths")
		})
	})
}

// TestBuilderAllocs checks the allocation contract of each method that
// appends, into a slice with room. The content of 200 octets takes a
// long-form length of one octet, and the largest uint64 takes an INTEGER
// of 11 octets. MaxAllocs counts the allocations of the whole process, so
// the test does not run in parallel.
func TestBuilderAllocs(t *testing.T) {
	dst := make([]byte, 0, 512)
	content := make([]byte, 200)

	t.Run("Add", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() {
			builder := der.NewBuilder(dst[:0])
			builder.Add(der.TagOctetString, content)
			got = builder.Bytes()
		}, 0, "Add into a slice with room must not allocate")
		assert.Length(t, got, 3+200, "the test must measure the whole element")
	})

	t.Run("AddElement", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() {
			builder := der.NewBuilder(dst[:0])
			builder.AddElement(content)
			got = builder.Bytes()
		}, 0, "AddElement into a slice with room must not allocate")
		assert.Length(t, got, 200, "the test must measure the whole element")
	})

	t.Run("AddUint64", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() {
			builder := der.NewBuilder(dst[:0])
			builder.AddUint64(math.MaxUint64)
			got = builder.Bytes()
		}, 0, "AddUint64 into a slice with room must not allocate")
		assert.Length(t, got, 11, "the test must measure the whole element")
	})

	t.Run("AddBoolean", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() {
			builder := der.NewBuilder(dst[:0])
			builder.AddBoolean(true)
			got = builder.Bytes()
		}, 0, "AddBoolean into a slice with room must not allocate")
		assert.Length(t, got, 3, "the test must measure the whole element")
	})

	t.Run("Close", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() {
			builder := der.NewBuilder(dst[:0])
			at := builder.Open(der.TagSequence)
			builder.AddElement(content)
			builder.AddUint64(math.MaxUint64)
			builder.Close(at)
			got = builder.Bytes()
		}, 0, "a long-form Close into a slice with room must not allocate")
		assert.Length(t, got, 3+200+11, "the test must measure the whole element")
	})
}

// BenchmarkBuilder reports the cost of each method that appends, into a
// slice with room, and fails when one allocates. The content of 200 octets
// takes a long-form length of one octet.
func BenchmarkBuilder(b *testing.B) {
	dst := make([]byte, 0, 512)
	content := make([]byte, 200)

	b.Run("Add", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			builder := der.NewBuilder(dst[:0])
			builder.Add(der.TagOctetString, content)
			got = builder.Bytes()
		}

		assert.Length(b, got, 3+200, "the benchmark must measure the whole element")
	})

	b.Run("AddElement", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			builder := der.NewBuilder(dst[:0])
			builder.AddElement(content)
			got = builder.Bytes()
		}

		assert.Length(b, got, 200, "the benchmark must measure the whole element")
	})

	b.Run("AddUint64", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			builder := der.NewBuilder(dst[:0])
			builder.AddUint64(math.MaxUint64)
			got = builder.Bytes()
		}

		assert.Length(b, got, 11, "the benchmark must measure the whole element")
	})

	b.Run("AddBoolean", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			builder := der.NewBuilder(dst[:0])
			builder.AddBoolean(true)
			got = builder.Bytes()
		}

		assert.Length(b, got, 3, "the benchmark must measure the whole element")
	})

	b.Run("Close", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			builder := der.NewBuilder(dst[:0])
			at := builder.Open(der.TagSequence)
			builder.AddElement(content)
			builder.AddUint64(math.MaxUint64)
			builder.Close(at)
			got = builder.Bytes()
		}

		assert.Length(b, got, 3+200+11, "the benchmark must measure the whole element")
	})
}
