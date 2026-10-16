// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package id_test

import (
	"bytes"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
)

// The text interfaces ID satisfies. A missing method is a build failure.
var (
	_ encoding.TextAppender    = id.Zero
	_ encoding.TextMarshaler   = id.Zero
	_ encoding.TextUnmarshaler = (*id.ID)(nil)
)

// textPrefix is the prefix of the text of every ID.
const textPrefix = "id:"

// nonZeroIDs generates IDs of every size, Zero excluded.
var nonZeroIDs = validBytes.Map(func(b []byte) id.ID {
	i, _ := id.FromBytes(b) // a length of an ID never fails

	return i
})

// badDigitCounts generates the numbers of digits up to twice the digits of
// the longest ID that the text of no ID has.
var badDigitCounts = prop.Integer(0, 4*id.MaxSize).Filter(func(n int) bool {
	return n != 0 && n != 2*id.Size128 && n != 2*id.Size160 && n != 2*id.Size256
})

// upperHexLetters generates the uppercase letters A to F, which
// encoding/hex decodes and which the text of an ID does not contain.
var upperHexLetters = prop.Integer(byte('A'), byte('F'))

// nonHexBytes generates the bytes that encoding/hex refuses as a
// hexadecimal digit.
var nonHexBytes = prop.Integer(byte(0), byte(255)).Filter(func(b byte) bool {
	_, err := hex.DecodeString(string([]byte{b, b}))

	return err != nil
})

// unprefixedTexts generates the digits of an ID after up to four bytes that
// do not start with the prefix of the text of an ID.
var unprefixedTexts = prop.Composite(func(c *prop.Case) []byte {
	prefix := c.Draw(prop.Bytes(prop.MaxSize(4)).Filter(func(p []byte) bool {
		return !bytes.HasPrefix(p, []byte(textPrefix))
	}), "prefix")

	return append(prefix, hex.EncodeToString(c.Draw(ids, "id").Bytes())...)
})

// idDocument is a JSON document with a field of ID.
type idDocument struct {
	ID id.ID `json:"id"`
}

func TestText(t *testing.T) {
	t.Parallel()

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the text that String returns to dst", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendText must keep dst and append the text of the ID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(id.MaxSize)), "dst")
				i := c.Draw(ids, "id")
				want := append(slices.Clone(dst), i.String()...)

				got, err := i.AppendText(dst)
				assert.NoError(c, err, "AppendText must succeed")
				assert.Equal(c, got, want, "AppendText must extend dst with the text of the ID")
			})
		})
	})

	t.Run("MarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the text that AppendText appends", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "MarshalText must return the text of AppendText", func(c *prop.Case) {
				i := c.Draw(ids, "id")

				want, err := i.AppendText(nil)
				assert.NoError(c, err, "AppendText must succeed")

				got, err := i.MarshalText()
				assert.NoError(c, err, "MarshalText must succeed")
				assert.Equal(c, got, want, "MarshalText must return the text that AppendText appends")
			})
		})

		t.Run("returns the prefix alone for Zero", func(t *testing.T) {
			t.Parallel()
			got, err := id.Zero.MarshalText()
			assert.NoError(t, err, "MarshalText must encode Zero")
			assert.Equal(t, string(got), textPrefix, "Zero must encode as the bare prefix")
		})

		t.Run("returns the lowercase hexadecimal of a 160-bit ID after the prefix", func(t *testing.T) {
			t.Parallel()
			var b [id.Size160]byte
			b[0], b[1], b[2] = 0x01, 0x23, 0x45
			b[17], b[18], b[19] = 0xab, 0xcd, 0xef
			got, err := id.New160(b).MarshalText()
			assert.NoError(t, err, "MarshalText must encode an ID")
			assert.Equal(t, string(got), textPrefix+"012345"+"0000000000000000000000000000"+"abcdef",
				"MarshalText must encode the bytes in order after the prefix")
		})

		t.Run("makes json.Marshal write an ID as a JSON string of its text", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "json.Marshal must write an ID as the JSON string of its text", func(c *prop.Case) {
				i := c.Draw(ids, "id")
				got, err := json.Marshal(idDocument{ID: i})
				assert.NoError(c, err, "json.Marshal must encode an ID")
				assert.Equal(c, string(got), `{"id":"`+textPrefix+hex.EncodeToString(i.Bytes())+`"}`,
					"the field must be the JSON string of the text of the ID")
			})
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("decodes the text that MarshalText returns", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, id.ID.MarshalText, func(text []byte) (id.ID, error) {
				var i id.ID
				err := i.UnmarshalText(text)

				return i, err
			}, "UnmarshalText must invert MarshalText", prop.Using(ids))
		})

		t.Run("returns the ID of text whatever i contained before", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalText must decode the text of any ID over any ID", func(c *prop.Case) {
				prior := c.Draw(ids, "prior")
				want := c.Draw(ids, "id")
				text := append([]byte(textPrefix), hex.EncodeToString(want.Bytes())...)

				got := prior
				assert.NoError(c, got.UnmarshalText(text), "UnmarshalText must accept the text of an ID")
				assert.Equal(c, got, want, "the decoded ID must equal the encoded one")
			})
		})

		t.Run("returns Zero for the prefix alone", func(t *testing.T) {
			t.Parallel()
			got := id.New128(fill128(0x66))
			assert.NoError(t, got.UnmarshalText([]byte(textPrefix)), "UnmarshalText must accept the text of Zero")
			assert.True(t, got.IsZero(), "the bare prefix must decode to the zero ID")
		})

		t.Run("returns ErrText for a text without the prefix", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(text []byte) error {
				var i id.ID

				return i.UnmarshalText(text)
			}, id.ErrText, "UnmarshalText must refuse a text that does not start with the prefix",
				prop.Using(unprefixedTexts))
		})

		t.Run("returns ErrSize for a number of digits that no ID has", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				var i id.ID

				return i.UnmarshalText(append([]byte(textPrefix), bytes.Repeat([]byte{'0'}, n)...))
			}, id.ErrSize, "UnmarshalText must refuse the number of digits",
				prop.Using(badDigitCounts), prop.Example(1), prop.Example(2*id.Size128-1),
				prop.Example(2*id.Size128+1), prop.Example(2*id.Size256+1))
		})

		t.Run("returns ErrText for an uppercase letter", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(text []byte) error {
				var i id.ID

				return i.UnmarshalText(text)
			}, id.ErrText, "UnmarshalText must refuse a text that AppendText does not write",
				prop.Using(idTextWith(upperHexLetters)))
		})

		t.Run("returns ErrText for a byte that is not a hexadecimal digit", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(text []byte) error {
				var i id.ID

				return i.UnmarshalText(text)
			}, id.ErrText, "UnmarshalText must refuse a byte that is not a digit",
				prop.Using(idTextWith(nonHexBytes)))
		})

		t.Run("returns an error of class Invalid for a text without the prefix", func(t *testing.T) {
			t.Parallel()
			var i id.ID
			assert.Equal(t, errs.Classify(i.UnmarshalText([]byte("uuid:"))), errs.Invalid,
				"ErrText must classify as Invalid")
		})

		t.Run("leaves i unchanged for a text that it refuses", func(t *testing.T) {
			t.Parallel()
			i := id.New128(fill128(0x77))
			refused := prop.OneOf(unprefixedTexts, idTextWith(nonHexBytes), idTextWith(upperHexLetters),
				badDigitCounts.Map(func(n int) []byte {
					return append([]byte(textPrefix), bytes.Repeat([]byte{'0'}, n)...)
				}))
			prop.Pure(t, func() id.ID { return i }, func(text []byte) {
				_ = i.UnmarshalText(text)
			}, "a refused decode must leave the ID unchanged", prop.Using(refused))
		})

		t.Run("returns an ID that a later write to text leaves unchanged", func(t *testing.T) {
			t.Parallel()
			text := append([]byte(textPrefix), bytes.Repeat([]byte("88"), id.Size128)...)
			var got id.ID
			assert.NoError(t, got.UnmarshalText(text), "UnmarshalText must accept the text of an ID")
			text[len(textPrefix)] = '0'
			assert.Equal(t, got.Bytes()[0], byte(0x88), "a write to text must not change the ID")
		})

		t.Run("decodes a JSON field that json.Marshal writes", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(i id.ID) ([]byte, error) {
				return json.Marshal(idDocument{ID: i})
			}, func(document []byte) (id.ID, error) {
				var v idDocument
				err := json.Unmarshal(document, &v)

				return v.ID, err
			}, "json.Unmarshal must undo json.Marshal of a field", prop.Using(ids))
		})

		t.Run("decodes the JSON object that json.Marshal writes for a map", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(m map[id.ID]int) ([]byte, error) {
				return json.Marshal(m)
			}, func(document []byte) (map[id.ID]int, error) {
				var m map[id.ID]int
				err := json.Unmarshal(document, &m)

				return m, err
			}, "json.Unmarshal must undo json.Marshal of a map keyed by IDs",
				prop.Using(prop.Dict(ids, prop.Integer(0, 9), prop.MaxSize(4))))
		})

		t.Run("returns ErrText through json.Unmarshal", func(t *testing.T) {
			t.Parallel()
			var got id.ID
			err := json.Unmarshal([]byte(`"uuid:00"`), &got)
			assert.ErrorIs(t, err, id.ErrText, "json.Unmarshal must return the error of UnmarshalText")
		})
	})
}

// TestTextAllocs checks the allocation contracts of the text methods.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestTextAllocs(t *testing.T) {
	u := id.New256(fill256(0x7f))
	text := append([]byte(textPrefix), hex.EncodeToString(u.Bytes())...)

	t.Run("AppendText", func(t *testing.T) {
		buf := make([]byte, 0, len(text))

		var err error
		expect.MaxAllocs(t, func() { buf, err = u.AppendText(buf[:0]) }, 0,
			"AppendText must not allocate when dst has room for the text")
		assert.NoError(t, err, "AppendText must succeed")
		assert.Equal(t, buf, text, "the test must measure the text of the ID")
	})

	t.Run("MarshalText", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got, _ = u.MarshalText() }, 1,
			"MarshalText must allocate only the returned slice")
		assert.Equal(t, got, text, "the test must measure the text of the ID")
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		var (
			got id.ID
			err error
		)
		expect.MaxAllocs(t, func() { err = got.UnmarshalText(text) }, 0, "UnmarshalText must not allocate")
		assert.NoError(t, err, "the test must measure a decode that succeeds")
		assert.Equal(t, got, u, "the test must measure the decode of the ID")
	})
}

// BenchmarkText reports the cost of the text methods of a 32-byte ID, and
// fails when one allocates more than TestTextAllocs allows.
func BenchmarkText(b *testing.B) {
	u := id.New256(fill256(0x7f))
	text := append([]byte(textPrefix), hex.EncodeToString(u.Bytes())...)

	b.Run("AppendText", func(b *testing.B) {
		buf := make([]byte, 0, len(text))
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			buf, err = u.AppendText(buf[:0])
		}

		assert.NoError(b, err, "AppendText must succeed")
		assert.Equal(b, buf, text, "the benchmark must measure the text of the ID")
	})

	b.Run("MarshalText", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = u.MarshalText()
		}

		assert.Equal(b, got, text, "the benchmark must measure the text of the ID")
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		var (
			got id.ID
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = got.UnmarshalText(text)
		}

		assert.NoError(b, err, "the benchmark must measure a decode that succeeds")
		assert.Equal(b, got, u, "the benchmark must measure the decode of the ID")
	})
}

// idTextWith returns a generator of the text of an ID other than Zero in
// which a byte of bad replaces the digit at one position.
func idTextWith(bad prop.Generator[byte]) prop.Generator[[]byte] {
	return prop.Composite(func(c *prop.Case) []byte {
		i := c.Draw(nonZeroIDs, "id")
		text := append([]byte(textPrefix), hex.EncodeToString(i.Bytes())...)
		text[len(textPrefix)+c.Draw(prop.Integer(0, 2*i.Size()-1), "digit")] = c.Draw(bad, "byte")

		return text
	})
}
