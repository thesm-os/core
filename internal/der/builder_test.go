// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package der_test

import (
	"math"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/internal/der"
)

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
				name: "appends a length of 65,536 in the long form of three octets", length: 65_536,
				header: []byte{0x04, 0x83, 0x01, 0x00, 0x00},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				b := der.NewBuilder(nil)
				b.Add(der.TagOctetString, make([]byte, tt.length))

				got := b.Bytes()
				testkit.Equal(t, got[:len(tt.header)], tt.header, "Add must append the tag and the DER length")
				testkit.Len(t, got, len(tt.header)+tt.length, "Add must append the content")
			})
		}

		t.Run("appends to the slice of the caller", func(t *testing.T) {
			t.Parallel()
			b := der.NewBuilder([]byte{0xee})
			b.Add(der.TagNull, nil)
			testkit.Equal(t, b.Bytes(), []byte{0xee, 0x05, 0x00}, "Add must keep the octets before it")
		})
	})

	t.Run("AddElement", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the element as it is", func(t *testing.T) {
			t.Parallel()
			b := der.NewBuilder(nil)
			b.AddElement([]byte{0x05, 0x00})
			testkit.Equal(t, b.Bytes(), []byte{0x05, 0x00}, "AddElement must copy the element")
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
				testkit.Equal(t, b.Bytes(), tt.want, "AddUint64 must append the DER INTEGER")

				r := der.NewReader(b.Bytes())
				content, _ := r.Read(der.TagInteger)
				got, ok := der.Uint64(content)
				testkit.True(t, ok && got == tt.give, "Uint64 must read the value back")
			})
		}
	})

	t.Run("AddBoolean", func(t *testing.T) {
		t.Parallel()

		t.Run("appends TRUE as 0xff", func(t *testing.T) {
			t.Parallel()
			b := der.NewBuilder(nil)
			b.AddBoolean(true)
			testkit.Equal(t, b.Bytes(), []byte{0x01, 0x01, 0xff}, "AddBoolean must append 0xff for true")
		})

		t.Run("appends FALSE as 0x00", func(t *testing.T) {
			t.Parallel()
			b := der.NewBuilder(nil)
			b.AddBoolean(false)
			testkit.Equal(t, b.Bytes(), []byte{0x01, 0x01, 0x00}, "AddBoolean must append 0x00 for false")
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
				name: "inserts three long-form octets for a length of 65,536", content: 65_536,
				header: []byte{0x30, 0x83, 0x01, 0x00, 0x00},
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
				testkit.Equal(t, got[:len(tt.header)], tt.header, "Close must write the DER length")
				testkit.Equal(t, got[len(tt.header):], content, "Close must keep the content after the length")
			})
		}

		t.Run("closes nested elements", func(t *testing.T) {
			t.Parallel()
			b := der.NewBuilder(nil)
			outer := b.Open(der.TagSequence)
			inner := b.Open(der.TagSet)
			b.AddBoolean(true)
			b.Close(inner)
			b.Add(der.TagNull, nil)
			b.Close(outer)

			testkit.Equal(t, b.Bytes(), []byte{0x30, 0x07, 0x31, 0x03, 0x01, 0x01, 0xff, 0x05, 0x00},
				"nested elements must close with their own lengths")
		})
	})
}

func BenchmarkBuilder(b *testing.B) {
	dst := make([]byte, 0, 512)
	content := make([]byte, 200)

	b.Run("Open and Close of a long-form length", func(b *testing.B) {
		derAllocs(b, func() {
			builder := der.NewBuilder(dst[:0])
			at := builder.Open(der.TagSequence)
			builder.AddElement(content)
			builder.AddUint64(math.MaxUint64)
			builder.Close(at)
			sinkBytes = builder.Bytes()
		})
		testkit.Len(b, sinkBytes, 3+200+11, "the benchmark must measure the whole element")
	})
}
