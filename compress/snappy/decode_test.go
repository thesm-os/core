// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package snappy_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/compress/snappy"
)

// decodeContract is the contract of decodeChanged.
const decodeContract = "AppendDecode refuses a changed block with ErrCorrupt, or returns the bytes of the length " +
	"that DecodedLen reads, which encode and decode to the same bytes"

// The low two bits of the tag byte of a copy give its kind. A literal of
// more than 60 bytes has the upper six bits 59 plus the number of bytes of its
// length that follow the tag.
const (
	kindCopy1      = 0x01
	kindCopy2      = 0x02
	kindCopy4      = 0x03
	longLiteralTag = 59
)

// overstated is a block whose preamble states 43 bytes. Its elements, a
// literal of one byte, take 2 bytes, which decode to at most 42 bytes.
var overstated = []byte{43, 0x00, 'a'}

// block is a Snappy block and the bytes that it decodes to.
type block struct {
	enc  []byte
	want []byte
}

// blocks generates valid blocks of 1 to 64 elements and the bytes that each
// block decodes to. A literal has 1 to 300 bytes. Its tag or the 1 to 4 bytes
// after it give its length, in the shortest form or a longer one. A copy of
// each of the three kinds takes an offset of up to 16 or up to the number of
// bytes before it, so the blocks contain runs, short offsets and long offsets.
var blocks = prop.Composite(func(c *prop.Case) block {
	r := rand.New(rand.NewPCG(c.Draw(prop.Integer[uint64](0, math.MaxUint64), "seed"), 0))
	var body, want []byte
	for range c.Draw(prop.Integer(1, 64), "elements") {
		if len(want) == 0 || c.Draw(prop.Boolean(), "literal") {
			n := c.Draw(prop.Integer(1, 300), "literal length")
			k := c.Draw(prop.Integer(0, 4), "length bytes")
			if n > 60 {
				k = max(k, 1)
			}
			if n > 256 {
				k = max(k, 2)
			}
			if k == 0 {
				body = append(body, byte(n-1)<<2)
			} else {
				body = append(body, byte(longLiteralTag+k)<<2)
				for i := range k {
					body = append(body, byte((n-1)>>(8*i)))
				}
			}
			for range n {
				b := byte(r.Uint32())
				body = append(body, b)
				want = append(want, b)
			}

			continue
		}
		kind := c.Draw(prop.SampledFrom(kindCopy1, kindCopy2, kindCopy4), "copy kind")
		far := len(want)
		if kind == kindCopy1 {
			far = min(far, 2047)
		}
		offset := c.Draw(prop.OneOf(prop.Integer(1, min(far, 16)), prop.Integer(1, far)), "offset")
		var length int
		if kind == kindCopy1 {
			length = c.Draw(prop.Integer(4, 11), "copy length")
			body = append(body, byte(offset>>8)<<5|byte(length-4)<<2|kindCopy1, byte(offset))
		} else {
			length = c.Draw(prop.Integer(1, 64), "copy length")
			body = append(body, byte(length-1)<<2|byte(kind))
			if kind == kindCopy2 {
				body = binary.LittleEndian.AppendUint16(body, uint16(offset))
			} else {
				body = binary.LittleEndian.AppendUint32(body, uint32(offset))
			}
		}
		for range length {
			want = append(want, want[len(want)-offset])
		}
	}

	return block{enc: append(binary.AppendUvarint(nil, uint64(len(want))), body...), want: want}
})

func TestDecode(t *testing.T) {
	t.Parallel()

	t.Run("DecodedLen", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			give    []byte
			wantLen int
			wantErr error
		}{
			{name: "returns 0 for the preamble 0x00", give: []byte{0x00}, wantLen: 0},
			{name: "returns 64 for the preamble 0x40", give: []byte{0x40}, wantLen: 64},
			{name: "returns 2097150 for the preamble 0xfe 0xff 0x7f", give: []byte{0xfe, 0xff, 0x7f}, wantLen: 2097150},
			{
				name: "returns MaxBlockLen for its preamble", give: []byte{0xff, 0xff, 0xff, 0xff, 0x0f},
				wantLen: snappy.MaxBlockLen,
			},
			{name: "returns ErrCorrupt for no bytes", give: nil, wantErr: snappy.ErrCorrupt},
			{name: "returns ErrCorrupt for a truncated preamble", give: []byte{0x80}, wantErr: snappy.ErrCorrupt},
			{
				name: "returns ErrCorrupt for a preamble above MaxBlockLen", give: []byte{0x80, 0x80, 0x80, 0x80, 0x10},
				wantErr: snappy.ErrCorrupt,
			},
			{
				name: "returns ErrCorrupt for a preamble beyond 64 bits",
				give: bytes.Repeat([]byte{0xff}, 11), wantErr: snappy.ErrCorrupt,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := snappy.DecodedLen(tt.give)
				if tt.wantErr != nil {
					assert.ErrorIs(t, err, tt.wantErr, "DecodedLen refuses the preamble")

					return
				}
				assert.NoError(t, err, "DecodedLen accepts the preamble")
				assert.Equal(t, got, tt.wantLen, "DecodedLen returns the length that the preamble states")
			})
		}
	})

	t.Run("AppendDecode", func(t *testing.T) {
		t.Parallel()

		t.Run("decodes the encoding of the Opticks to its text", func(t *testing.T) {
			t.Parallel()

			want, enc := opticks(t)
			got, err := snappy.AppendDecode(nil, enc)
			assert.NoError(t, err, "AppendDecode decodes the encoding")
			assert.Equal(t, got, want, "AppendDecode returns the text")
		})

		t.Run("appends the bytes to dst", func(t *testing.T) {
			t.Parallel()

			want, enc := opticks(t)
			got, err := snappy.AppendDecode([]byte("prefix"), enc)
			assert.NoError(t, err, "AppendDecode decodes the encoding")
			assert.Equal(t, got, append([]byte("prefix"), want...), "AppendDecode keeps the bytes of dst")
		})

		t.Run("decodes the encodings of the corpus to their sources", func(t *testing.T) {
			t.Parallel()

			for i, src := range corpus() {
				enc, err := snappy.AppendEncode(nil, src)
				assert.NoError(t, err, "AppendEncode encodes the source")
				got, err := snappy.AppendDecode(nil, enc)
				assert.NoError(t, err, "AppendDecode decodes the encoding")
				expect.Equal(t, got, src, "AppendDecode returns source "+strconv.Itoa(i), assert.EquateEmpty())
			}
		})

		// The cases decode into a dst whose capacity is the decoded length, so
		// a move past the end of dst panics.
		exact := []struct {
			name string
			give []byte
			want []byte
		}{
			{
				name: "decodes literals of one byte into the last 9 bytes of dst",
				give: append([]byte{0x09}, bytes.Repeat([]byte{0x00, 'a'}, 9)...),
				want: bytes.Repeat([]byte("a"), 9),
			},
			{
				name: "decodes a literal of 20 bytes into the last 60 bytes of dst",
				give: slices.Concat([]byte{0x46, 9 << 2}, bytes.Repeat([]byte("b"), 10), []byte{19 << 2},
					bytes.Repeat([]byte("c"), 20), bytes.Repeat([]byte{0x00, 'd'}, 40)),
				want: slices.Concat(bytes.Repeat([]byte("b"), 10), bytes.Repeat([]byte("c"), 20),
					bytes.Repeat([]byte("d"), 40)),
			},
		}
		for _, tt := range exact {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := snappy.AppendDecode(make([]byte, 0, len(tt.want)), tt.give)
				assert.NoError(t, err, "AppendDecode decodes the block")
				assert.Equal(t, got, tt.want, "AppendDecode returns the bytes of the elements")
			})
		}

		overruns := []struct {
			name string
			give []byte
		}{
			{
				name: "returns ErrCorrupt for a literal past the end of dst before another element",
				give: []byte{0x04, 0x04, 'a', 'b', 0x14, 'c', 'd', 'e', 'f', 'g', 'h', 0x00, 'i'},
			},
			{
				name: "returns ErrCorrupt for a copy past the end of dst",
				give: []byte{0x05, 0x0c, 'a', 'b', 'c', 'd', 0x11, 0x01},
			},
		}
		for _, tt := range overruns {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				n, err := snappy.DecodedLen(tt.give)
				assert.NoError(t, err, "DecodedLen reads the preamble")
				_, err = snappy.AppendDecode(make([]byte, 0, n), tt.give)
				assert.ErrorIs(t, err, snappy.ErrCorrupt, "AppendDecode refuses the block")
			})
		}

		t.Run("decodes every valid block to the bytes of its elements", func(t *testing.T) {
			t.Parallel()

			prop.ForAll(t, "AppendDecode returns the bytes of the elements of a block", func(c *prop.Case) {
				b := c.Draw(blocks, "block")
				got, err := snappy.AppendDecode(nil, b.enc)
				assert.NoError(c, err, "AppendDecode decodes the block")
				assert.Equal(c, got, b.want, "AppendDecode returns the bytes of the elements")
			})
		})

		abcd := []byte{0x0c, 'a', 'b', 'c', 'd'}
		elements := []struct {
			name string
			give []byte
			want []byte
		}{
			{name: "decodes a literal of one byte", give: []byte{0x01, 0x00, 'a'}, want: []byte("a")},
			{
				name: "decodes a literal with one byte of length",
				give: append([]byte{0x3d, 0xf0, 0x3c}, bytes.Repeat([]byte("x"), 61)...),
				want: bytes.Repeat([]byte("x"), 61),
			},
			{
				name: "decodes a literal with two bytes of length",
				give: append([]byte{0xac, 0x02, 0xf4, 0x2b, 0x01}, bytes.Repeat([]byte("y"), 300)...),
				want: bytes.Repeat([]byte("y"), 300),
			},
			{
				name: "decodes a literal with three bytes of length",
				give: []byte{0x03, 0xf8, 0x02, 0x00, 0x00, 'a', 'b', 'c'},
				want: []byte("abc"),
			},
			{
				name: "decodes a literal with four bytes of length",
				give: []byte{0x03, 0xfc, 0x02, 0x00, 0x00, 0x00, 'a', 'b', 'c'},
				want: []byte("abc"),
			},
			{
				name: "decodes a copy with a 1-byte offset",
				give: append(append([]byte{0x08}, abcd...), 0x01, 0x04),
				want: []byte("abcdabcd"),
			},
			{
				name: "decodes a copy with a 2-byte offset",
				give: append(append([]byte{0x08}, abcd...), 0x0e, 0x04, 0x00),
				want: []byte("abcdabcd"),
			},
			{
				name: "decodes a copy with a 4-byte offset",
				give: append(append([]byte{0x08}, abcd...), 0x0f, 0x04, 0x00, 0x00, 0x00),
				want: []byte("abcdabcd"),
			},
			{
				name: "repeats a run for a copy longer than its offset",
				give: []byte{0x07, 0x08, 'x', 'a', 'b', 0x01, 0x02},
				want: []byte("xababab"),
			},
			{name: "decodes the block of no bytes", give: []byte{0x00}, want: nil},
		}
		for _, tt := range elements {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := snappy.AppendDecode(nil, tt.give)
				assert.NoError(t, err, "AppendDecode decodes the block")
				assert.Equal(t, got, tt.want, "AppendDecode returns the bytes of the elements", assert.EquateEmpty())
			})
		}

		tests := []struct {
			name string
			give []byte
		}{
			{name: "returns ErrCorrupt for no bytes", give: nil},
			{name: "returns ErrCorrupt for fewer bytes than the preamble states", give: []byte{0x02, 0x00, 'a'}},
			{
				name: "returns ErrCorrupt for a literal past the length that the preamble states",
				give: []byte{0x01, 0x04, 'a', 'b'},
			},
			{name: "returns ErrCorrupt for a literal past the end of the block", give: []byte{0x03, 0x08, 'a'}},
			{name: "returns ErrCorrupt for a truncated length of a literal", give: []byte{0x01, 0xf0}},
			{name: "returns ErrCorrupt for a copy with offset 0", give: []byte{0x05, 0x00, 'a', 0x01, 0x00}},
			{name: "returns ErrCorrupt for a copy before the first byte", give: []byte{0x05, 0x00, 'a', 0x01, 0x02}},
			{
				name: "returns ErrCorrupt for a copy past the length that the preamble states",
				give: []byte{0x04, 0x00, 'a', 0x01, 0x01},
			},
			{name: "returns ErrCorrupt for a truncated copy with a 1-byte offset", give: []byte{0x05, 0x00, 'a', 0x01}},
			{
				name: "returns ErrCorrupt for a truncated copy with a 2-byte offset",
				give: []byte{0x05, 0x00, 'a', 0x0e, 0x01},
			},
			{
				name: "returns ErrCorrupt for a truncated copy with a 4-byte offset",
				give: []byte{0x05, 0x00, 'a', 0x0f, 0x01, 0x00, 0x00},
			},
			{
				name: "returns ErrCorrupt for an element past the length that the preamble states",
				give: []byte{0x01, 0x00, 'a', 0x00, 'b'},
			},
			{
				name: "returns ErrCorrupt for a preamble above MaxBlockLen",
				give: []byte{0x80, 0x80, 0x80, 0x80, 0x10, 0x00, 'a'},
			},
			{
				name: "returns ErrCorrupt for a stated length above what the elements can decode to",
				give: overstated,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				dst := make([]byte, 3, 64)
				got, err := snappy.AppendDecode(dst, tt.give)
				assert.ErrorIs(t, err, snappy.ErrCorrupt, "AppendDecode refuses the block")
				assert.Equal(t, got, dst, "AppendDecode returns dst unchanged", assert.ByIdentity())
			})
		}

		t.Run("returns ErrCorrupt for every truncation of the encoding of the Opticks", func(t *testing.T) {
			t.Parallel()

			_, enc := opticks(t)
			for n := range len(enc) {
				_, err := snappy.AppendDecode(nil, enc[:n])
				expect.ErrorIs(t, err, snappy.ErrCorrupt, "AppendDecode refuses the first "+strconv.Itoa(n)+" bytes")
			}
		})

		t.Run("refuses a changed block or decodes it to bytes that encode and decode alike", func(t *testing.T) {
			t.Parallel()

			prop.ForAll(t, decodeContract, decodeChanged)
		})
	})
}

func TestDecodeAllocs(t *testing.T) {
	src := leveldbBlock(32<<10, 3)
	enc, err := snappy.AppendEncode(nil, src)
	assert.NoError(t, err, "AppendEncode encodes the block")

	t.Run("AppendDecode", func(t *testing.T) {
		t.Run("does not allocate into a dst with room for the decoded bytes", func(t *testing.T) {
			dst := make([]byte, 0, len(src))

			var got []byte
			expect.MaxAllocs(t, func() { got, err = snappy.AppendDecode(dst, enc) }, 0,
				"AppendDecode does not allocate into a dst with room for the decoded bytes")
			assert.NoError(t, err, "the test measures a block that decodes")
			assert.Equal(t, got, src, "the test measures the decoding of the block")
		})

		t.Run("does not allocate for a stated length above what the elements can decode to", func(t *testing.T) {
			expect.MaxAllocs(t, func() { _, err = snappy.AppendDecode(nil, overstated) }, 0,
				"AppendDecode refuses the stated length before it grows dst")
			assert.ErrorIs(t, err, snappy.ErrCorrupt, "the test measures a block that AppendDecode refuses")
		})
	})

	t.Run("DecodedLen", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got, _ = snappy.DecodedLen(enc) }, 0, "DecodedLen does not allocate")
		assert.Equal(t, got, len(src), "the test measures the length of the block")
	})
}

func BenchmarkDecode(b *testing.B) {
	b.Run("AppendDecode", func(b *testing.B) {
		for _, size := range []int{4 << 10, 32 << 10} {
			b.Run(strconv.Itoa(size), func(b *testing.B) {
				src := leveldbBlock(size, 4)
				enc, err := snappy.AppendEncode(nil, src)
				assert.NoError(b, err, "AppendEncode encodes the block")
				dst := make([]byte, 0, len(src))
				b.SetBytes(int64(len(src)))

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				var got []byte
				for c.Loop() {
					got, err = snappy.AppendDecode(dst, enc)
				}

				assert.NoError(b, err, "the benchmark measures blocks that decode")
				assert.Equal(b, got, src, "the benchmark measures the decoding of the block")
			})
		}
	})

	b.Run("DecodedLen", func(b *testing.B) {
		enc := binary.AppendUvarint(nil, 32<<10)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var got int
		for c.Loop() {
			got, _ = snappy.DecodedLen(enc)
		}

		assert.Equal(b, got, 32<<10, "the benchmark measures the preamble of 32 KiB")
	})
}

// FuzzAppendDecode checks that AppendDecode refuses a changed block with
// ErrCorrupt, or returns the bytes of the length that DecodedLen reads, which
// encode and decode to the same bytes.
func FuzzAppendDecode(f *testing.F) {
	prop.Fuzz(f, decodeContract, decodeChanged)
}

// decodeChanged encodes a drawn source, sets one byte past the preamble of
// the encoding to a drawn value, cuts the encoding to a drawn length and
// decodes it. A change of the preamble could state a length of up to 4 GiB,
// which AppendDecode allocates before it reads the elements, so the change
// leaves the preamble as it is.
func decodeChanged(c *prop.Case) {
	src := c.Draw(prop.Bytes(prop.MaxSize(300)), "source")
	enc, err := snappy.AppendEncode(nil, src)
	assert.NoError(c, err, "AppendEncode encodes the source")
	if preamble := len(binary.AppendUvarint(nil, uint64(len(src)))); len(enc) > preamble {
		enc[c.Draw(prop.Integer(preamble, len(enc)-1), "index")] = c.Draw(prop.Integer[byte](0, 255), "byte")
	}
	enc = enc[:c.Draw(prop.Integer(0, len(enc)), "length")]

	got, err := snappy.AppendDecode(nil, enc)
	if err != nil {
		assert.ErrorIs(c, err, snappy.ErrCorrupt, "AppendDecode refuses the changed block with ErrCorrupt")

		return
	}
	n, err := snappy.DecodedLen(enc)
	assert.NoError(c, err, "DecodedLen reads the preamble of a block that decodes")
	assert.Length(c, got, n, "AppendDecode returns the length that the preamble states")
	assert.RoundTrip(c,
		func(b []byte) ([]byte, error) { return snappy.AppendEncode(nil, b) },
		func(e []byte) ([]byte, error) { return snappy.AppendDecode(nil, e) },
		got, "the decoded bytes encode and decode to the same bytes", assert.EquateEmpty())
}
