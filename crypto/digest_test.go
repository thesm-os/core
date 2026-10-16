// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bytes"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.thesmos.sh/kanon/kanontest"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
)

// The text interfaces that Digest satisfies. A missing method is a build
// failure.
var (
	_ encoding.TextAppender    = crypto.Digest{}
	_ encoding.TextMarshaler   = crypto.Digest{}
	_ encoding.TextUnmarshaler = (*crypto.Digest)(nil)
)

// The digests of the allocation tests and the benchmarks.
var (
	// digest42 is a digest of DigestSize256 whose bytes are all 0x42.
	digest42 = crypto.NewDigest256([crypto.DigestSize256]byte(bytes.Repeat([]byte{0x42}, crypto.DigestSize256)))

	// digest43 is a digest of DigestSize256 whose bytes are all 0x43.
	digest43 = crypto.NewDigest256([crypto.DigestSize256]byte(bytes.Repeat([]byte{0x43}, crypto.DigestSize256)))

	// digestDst is a buffer with capacity for the encoding of digest42.
	digestDst = make([]byte, 0, crypto.DigestSize256)

	// digestTextDst is a buffer with capacity for the text of digest42.
	digestTextDst = make([]byte, 0, 2*crypto.DigestSize256)

	// digest43Text is the text of digest43, the hexadecimal of its bytes.
	digest43Text = []byte(hex.EncodeToString(digest43.Bytes()))

	// digestDecoded is the Digest that the decode of a measured call
	// writes.
	digestDecoded crypto.Digest
)

// digestBytes generates the bytes of a digest of each size.
var digestBytes = prop.SampledFrom(crypto.DigestSize256, crypto.DigestSize384, crypto.DigestSize512).
	Bind(func(n int) prop.Generator[[]byte] { return prop.Bytes(prop.MinSize(n), prop.MaxSize(n)) })

// digests generates a Digest of each size over arbitrary bytes. It
// builds each digest with the constructor of its size, so a defect of
// DigestFromBytes cannot shape the inputs of its own tests.
var digests = digestBytes.Map(newDigest)

// digestPairs generates two digests that are equal in a third of the
// cases, one byte apart in a third, and independent in the rest.
var digestPairs = prop.Composite(func(c *prop.Case) [2]crypto.Digest {
	b := c.Draw(digestBytes, "first")
	relation := c.Draw(prop.Integer(0, 2), "relation")
	if relation == 0 {
		return [2]crypto.Digest{newDigest(b), newDigest(b)}
	}
	if relation == 1 {
		other := bytes.Clone(b)
		other[c.Draw(prop.Integer(0, len(b)-1), "differing byte")] ^= 0x01

		return [2]crypto.Digest{newDigest(b), newDigest(other)}
	}

	return [2]crypto.Digest{newDigest(b), c.Draw(digests, "second")}
})

// badLengths generates the lengths up to twice MaxDigestSize that no
// digest has.
var badLengths = prop.Integer(0, 2*crypto.MaxDigestSize).Filter(func(n int) bool {
	return n != crypto.DigestSize256 && n != crypto.DigestSize384 && n != crypto.DigestSize512
})

// badTextLengths generates the lengths up to twice the longest text of a
// digest that the text of no digest has.
var badTextLengths = prop.Integer(0, 4*crypto.MaxDigestSize).Filter(func(n int) bool {
	return n != 2*crypto.DigestSize256 && n != 2*crypto.DigestSize384 && n != 2*crypto.DigestSize512
})

// upperHexLetters generates the uppercase letters A to F, which
// encoding/hex decodes and which the text of a digest does not contain.
var upperHexLetters = prop.Integer(byte('A'), byte('F'))

// nonHexBytes generates the bytes that encoding/hex refuses as a
// hexadecimal digit.
var nonHexBytes = prop.Integer(byte(0), byte(255)).Filter(func(b byte) bool {
	_, err := hex.DecodeString(string([]byte{b, b}))

	return err != nil
})

// digestCalls are the calls on the hot path of a Digest, each with its
// allocation ceiling. Each call reports whether it returned the result
// that its fixtures determine, so the measurement keeps the result alive.
var digestCalls = []struct {
	name   string
	allocs uint64
	call   func() bool
}{
	{name: "NewDigest256", call: func() bool {
		return crypto.NewDigest256([crypto.DigestSize256]byte{1}).Size() == crypto.DigestSize256
	}},
	{name: "NewDigest384", call: func() bool {
		return crypto.NewDigest384([crypto.DigestSize384]byte{1}).Size() == crypto.DigestSize384
	}},
	{name: "NewDigest512", call: func() bool {
		return crypto.NewDigest512([crypto.DigestSize512]byte{1}).Size() == crypto.DigestSize512
	}},
	{name: "DigestFromBytes", call: func() bool {
		_, err := crypto.DigestFromBytes(digest42.Bytes())

		return err == nil
	}},
	{name: "AppendBinary", call: func() bool {
		b, err := digest42.AppendBinary(digestDst[:0])

		return err == nil && len(b) == crypto.DigestSize256
	}},
	{name: "UnmarshalBinary", call: func() bool {
		return digestDecoded.UnmarshalBinary(digest43.Bytes()) == nil
	}},
	{name: "AppendText", call: func() bool {
		b, err := digest42.AppendText(digestTextDst[:0])

		return err == nil && len(b) == 2*crypto.DigestSize256
	}},
	{name: "MarshalText", allocs: 1, call: func() bool {
		b, err := digest42.MarshalText()

		return err == nil && len(b) == 2*crypto.DigestSize256
	}},
	{name: "UnmarshalText", call: func() bool {
		return digestDecoded.UnmarshalText(digest43Text) == nil && digestDecoded == digest43
	}},
	{name: "SizeKanon", call: func() bool { return digest42.SizeKanon() == crypto.DigestSize256 }},
	{name: "Size", call: func() bool { return digest42.Size() == crypto.DigestSize256 }},
	{name: "Bytes", call: func() bool { return len(digest42.Bytes()) == crypto.DigestSize256 }},
	{name: "IsZero", call: func() bool { return !digest42.IsZero() }},
	{name: "Equal", call: func() bool { return !digest42.Equal(digest43) }},
	{name: "ConstantTimeEqual", call: func() bool { return !digest42.ConstantTimeEqual(digest43) }},
	{name: "Compare", call: func() bool { return digest42.Compare(digest43) < 0 }},
}

// digestDocument is a JSON document with a field of Digest.
type digestDocument struct {
	Digest crypto.Digest `json:"digest"`
}

func TestDigest(t *testing.T) {
	t.Parallel()

	constructors := []struct {
		name      string
		size      int
		construct func([]byte) crypto.Digest
	}{
		{
			name:      "NewDigest256",
			size:      32,
			construct: func(b []byte) crypto.Digest { return crypto.NewDigest256([crypto.DigestSize256]byte(b)) },
		},
		{
			name:      "NewDigest384",
			size:      48,
			construct: func(b []byte) crypto.Digest { return crypto.NewDigest384([crypto.DigestSize384]byte(b)) },
		},
		{
			name:      "NewDigest512",
			size:      64,
			construct: func(b []byte) crypto.Digest { return crypto.NewDigest512([crypto.DigestSize512]byte(b)) },
		},
	}
	for _, tt := range constructors {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("returns a digest of the bytes of b", func(t *testing.T) {
				t.Parallel()
				prop.Equal(t, func(b []byte) []byte { return tt.construct(b).Bytes() },
					func(b []byte) []byte { return b }, "the digest must contain every byte of b",
					prop.Using(prop.Bytes(prop.MinSize(tt.size), prop.MaxSize(tt.size))))
			})

			t.Run("returns a digest whose Size is the length of b", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.construct(make([]byte, tt.size)).Size(), tt.size,
					"the size constant must keep its value")
			})
		})
	}

	t.Run("DigestFromBytes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a digest of the bytes of b", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, crypto.DigestFromBytes, func(d crypto.Digest) ([]byte, error) {
				return d.Bytes(), nil
			}, "the digest must contain every byte of b", prop.Using(digestBytes))
		})

		t.Run("returns ErrDigestSize for a length that no digest has", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := crypto.DigestFromBytes(make([]byte, n))

				return err
			}, crypto.ErrDigestSize, "DigestFromBytes must refuse the length",
				prop.Using(badLengths), prop.Example(0), prop.Example(crypto.DigestSize256-1),
				prop.Example(crypto.DigestSize384+1), prop.Example(crypto.DigestSize512+1))
		})

		t.Run("returns an error of class Invalid for a length that no digest has", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.DigestFromBytes(make([]byte, 40))
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrDigestSize must classify as Invalid")
		})

		t.Run("returns the zero Digest for a length that no digest has", func(t *testing.T) {
			t.Parallel()
			prop.True(t, func(n int) bool {
				d, _ := crypto.DigestFromBytes(make([]byte, n))

				return d.IsZero()
			}, "DigestFromBytes must return the zero Digest with its error", prop.Using(badLengths))
		})

		t.Run("returns a digest that does not alias b", func(t *testing.T) {
			t.Parallel()
			b := make([]byte, crypto.DigestSize256)
			got, err := crypto.DigestFromBytes(b)
			assert.NoError(t, err, "DigestFromBytes must accept a length that a digest has")
			b[0] = 0xFF
			assert.Equal(t, got.Bytes()[0], byte(0), "a write to b must not change the digest")
		})
	})

	t.Run("AppendBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the bytes of d to dst", func(t *testing.T) {
			t.Parallel()
			// An error leaves dst unchanged, which the comparison refuses.
			prop.Equal(t, func(d crypto.Digest) []byte {
				out, _ := d.AppendBinary([]byte{0xAA})

				return out
			}, func(d crypto.Digest) []byte {
				return append([]byte{0xAA}, d.Bytes()...)
			}, "AppendBinary must keep dst and append the bytes of d", prop.Using(digests))
		})

		t.Run("returns ErrDigestZero for the zero Digest", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.Digest{}.AppendBinary(nil)
			assert.ErrorIs(t, err, crypto.ErrDigestZero, "the zero Digest must have no encoding")
		})

		t.Run("returns dst unchanged for the zero Digest", func(t *testing.T) {
			t.Parallel()
			got, _ := crypto.Digest{}.AppendBinary([]byte{0xAA})
			assert.Equal(t, got, []byte{0xAA}, "AppendBinary must leave dst unchanged on error")
		})
	})

	t.Run("MarshalBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the bytes that AppendBinary appends", func(t *testing.T) {
			t.Parallel()
			// No generated digest is zero, so neither call returns an error.
			prop.Equal(t, func(d crypto.Digest) []byte {
				out, _ := d.MarshalBinary()

				return out
			}, func(d crypto.Digest) []byte {
				out, _ := d.AppendBinary(nil)

				return out
			}, "MarshalBinary must return the encoding of AppendBinary", prop.Using(digests))
		})

		t.Run("returns ErrDigestZero for the zero Digest", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.Digest{}.MarshalBinary()
			assert.ErrorIs(t, err, crypto.ErrDigestZero, "the zero Digest must have no encoding")
		})

		t.Run("returns an error of class Invalid for the zero Digest", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.Digest{}.MarshalBinary()
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrDigestZero must classify as Invalid")
		})

		t.Run("returns nil for the zero Digest", func(t *testing.T) {
			t.Parallel()
			got, _ := crypto.Digest{}.MarshalBinary()
			assert.Nil(t, got, "MarshalBinary must return no bytes with its error")
		})
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("decodes the encoding that MarshalBinary returns", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, crypto.Digest.MarshalBinary, func(b []byte) (crypto.Digest, error) {
				var d crypto.Digest
				err := d.UnmarshalBinary(b)

				return d, err
			}, "UnmarshalBinary must invert MarshalBinary", prop.Using(digests))
		})

		t.Run("returns the digest of data whatever d contained before", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalBinary must overwrite every byte of the digest in d",
				func(c *prop.Case) {
					got := c.Draw(digests, "before")
					want := c.Draw(digests, "decoded")
					assert.NoError(c, got.UnmarshalBinary(want.Bytes()), "UnmarshalBinary must accept a digest")
					assert.Equal(c, got, want, "the decoded digest must equal the digest of the same bytes")
				})
		})

		t.Run("returns ErrDigestSize for a length that no digest has", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				var d crypto.Digest

				return d.UnmarshalBinary(make([]byte, n))
			}, crypto.ErrDigestSize, "UnmarshalBinary must refuse the length",
				prop.Using(badLengths), prop.Example(0), prop.Example(crypto.DigestSize256-1),
				prop.Example(crypto.DigestSize384+1), prop.Example(crypto.DigestSize512+1))
		})

		t.Run("leaves d unchanged for a length that no digest has", func(t *testing.T) {
			t.Parallel()
			d := crypto.NewDigest384([crypto.DigestSize384]byte(bytes.Repeat([]byte{0x22}, crypto.DigestSize384)))
			prop.Pure(t, func() crypto.Digest { return d }, func(n int) {
				_ = d.UnmarshalBinary(make([]byte, n))
			}, "a refused decode must leave d unchanged", prop.Using(badLengths), prop.Example(40))
		})
	})

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the hexadecimal of the bytes of d to dst", func(t *testing.T) {
			t.Parallel()
			// An error leaves dst unchanged, which the comparison refuses.
			prop.Equal(t, func(d crypto.Digest) []byte {
				out, _ := d.AppendText([]byte{0xAA})

				return out
			}, func(d crypto.Digest) []byte {
				return append([]byte{0xAA}, hex.EncodeToString(d.Bytes())...)
			}, "AppendText must keep dst and append the hexadecimal of d", prop.Using(digests))
		})

		t.Run("returns ErrDigestZero for the zero Digest", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.Digest{}.AppendText(nil)
			assert.ErrorIs(t, err, crypto.ErrDigestZero, "the zero Digest must have no text")
		})

		t.Run("returns dst unchanged for the zero Digest", func(t *testing.T) {
			t.Parallel()
			got, _ := crypto.Digest{}.AppendText([]byte{0xAA})
			assert.Equal(t, got, []byte{0xAA}, "AppendText must leave dst unchanged on error")
		})
	})

	t.Run("MarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the text that AppendText appends", func(t *testing.T) {
			t.Parallel()
			// No generated digest is zero, so neither call returns an error.
			prop.Equal(t, func(d crypto.Digest) []byte {
				out, _ := d.MarshalText()

				return out
			}, func(d crypto.Digest) []byte {
				out, _ := d.AppendText(nil)

				return out
			}, "MarshalText must return the text of AppendText", prop.Using(digests))
		})

		t.Run("returns the bytes in lowercase hexadecimal", func(t *testing.T) {
			t.Parallel()
			var b [crypto.DigestSize256]byte
			b[0], b[1], b[2] = 0x01, 0x23, 0x45
			b[29], b[30], b[31] = 0xab, 0xcd, 0xef
			got, err := crypto.NewDigest256(b).MarshalText()
			assert.NoError(t, err, "MarshalText must encode a digest")
			assert.Equal(t, string(got), "012345"+"0000000000000000000000000000000000000000000000000000"+"abcdef",
				"MarshalText must encode the bytes in order")
		})

		t.Run("returns ErrDigestZero for the zero Digest", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.Digest{}.MarshalText()
			assert.ErrorIs(t, err, crypto.ErrDigestZero, "the zero Digest must have no text")
		})

		t.Run("returns nil for the zero Digest", func(t *testing.T) {
			t.Parallel()
			got, _ := crypto.Digest{}.MarshalText()
			assert.Nil(t, got, "MarshalText must return no text with its error")
		})

		t.Run("makes json.Marshal write d as a JSON string of its text", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "json.Marshal must write a Digest as the JSON string of its text", func(c *prop.Case) {
				d := c.Draw(digests, "digest")
				got, err := json.Marshal(digestDocument{Digest: d})
				assert.NoError(c, err, "json.Marshal must encode a digest")
				assert.Equal(c, string(got), `{"digest":"`+hex.EncodeToString(d.Bytes())+`"}`,
					"the field must be the JSON string of the text of the digest")
			})
		})

		t.Run("returns ErrDigestZero through json.Marshal for the zero Digest", func(t *testing.T) {
			t.Parallel()
			_, err := json.Marshal(digestDocument{})
			assert.ErrorIs(t, err, crypto.ErrDigestZero, "json.Marshal must return the error of MarshalText")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("decodes the text that MarshalText returns", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, crypto.Digest.MarshalText, func(text []byte) (crypto.Digest, error) {
				var d crypto.Digest
				err := d.UnmarshalText(text)

				return d, err
			}, "UnmarshalText must invert MarshalText", prop.Using(digests))
		})

		t.Run("returns the digest of text whatever d contained before", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalText must overwrite every byte of the digest in d",
				func(c *prop.Case) {
					got := c.Draw(digests, "before")
					want := c.Draw(digests, "decoded")
					text := []byte(hex.EncodeToString(want.Bytes()))
					assert.NoError(c, got.UnmarshalText(text), "UnmarshalText must accept the text of a digest")
					assert.Equal(c, got, want, "the decoded digest must equal the digest of the same text")
				})
		})

		t.Run("returns ErrDigestSize for a length that the text of no digest has", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				var d crypto.Digest

				return d.UnmarshalText(bytes.Repeat([]byte{'0'}, n))
			}, crypto.ErrDigestSize, "UnmarshalText must refuse the length",
				prop.Using(badTextLengths), prop.Example(0), prop.Example(2*crypto.DigestSize256-1),
				prop.Example(2*crypto.DigestSize256+1), prop.Example(2*crypto.DigestSize512+1))
		})

		t.Run("returns ErrDigestText for an uppercase letter", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(text []byte) error {
				var d crypto.Digest

				return d.UnmarshalText(text)
			}, crypto.ErrDigestText, "UnmarshalText must refuse a text that AppendText does not write",
				prop.Using(digestTextWith(upperHexLetters)))
		})

		t.Run("returns ErrDigestText for a byte that is not a hexadecimal digit", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(text []byte) error {
				var d crypto.Digest

				return d.UnmarshalText(text)
			}, crypto.ErrDigestText, "UnmarshalText must refuse a byte that is not a digit",
				prop.Using(digestTextWith(nonHexBytes)))
		})

		t.Run("returns an error of class Invalid for a byte that is not a hexadecimal digit", func(t *testing.T) {
			t.Parallel()
			var d crypto.Digest
			err := d.UnmarshalText(bytes.Repeat([]byte{'g'}, 2*crypto.DigestSize256))
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrDigestText must classify as Invalid")
		})

		t.Run("leaves d unchanged for a text that it refuses", func(t *testing.T) {
			t.Parallel()
			d := crypto.NewDigest384([crypto.DigestSize384]byte(bytes.Repeat([]byte{0x22}, crypto.DigestSize384)))
			refused := prop.OneOf(digestTextWith(nonHexBytes), digestTextWith(upperHexLetters),
				badTextLengths.Map(func(n int) []byte { return bytes.Repeat([]byte{'0'}, n) }))
			prop.Pure(t, func() crypto.Digest { return d }, func(text []byte) {
				_ = d.UnmarshalText(text)
			}, "a refused decode must leave d unchanged", prop.Using(refused))
		})

		t.Run("decodes a JSON field that json.Marshal writes", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(d crypto.Digest) ([]byte, error) {
				return json.Marshal(digestDocument{Digest: d})
			}, func(document []byte) (crypto.Digest, error) {
				var v digestDocument
				err := json.Unmarshal(document, &v)

				return v.Digest, err
			}, "json.Unmarshal must undo json.Marshal of a field", prop.Using(digests))
		})

		t.Run("decodes the JSON object that json.Marshal writes for a map", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(m map[crypto.Digest]int) ([]byte, error) {
				return json.Marshal(m)
			}, func(document []byte) (map[crypto.Digest]int, error) {
				var m map[crypto.Digest]int
				err := json.Unmarshal(document, &m)

				return m, err
			}, "json.Unmarshal must undo json.Marshal of a map keyed by digests",
				prop.Using(prop.Dict(digests, prop.Integer(0, 9), prop.MaxSize(4))))
		})

		t.Run("returns ErrDigestText through json.Unmarshal", func(t *testing.T) {
			t.Parallel()
			var got crypto.Digest
			err := json.Unmarshal([]byte(`"`+strings.Repeat("A", 2*crypto.DigestSize256)+`"`), &got)
			assert.ErrorIs(t, err, crypto.ErrDigestText, "json.Unmarshal must return the error of UnmarshalText")
		})
	})

	t.Run("SizeKanon", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the length of the encoding of d", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, crypto.Digest.SizeKanon, func(d crypto.Digest) int {
				out, _ := d.AppendBinary(nil)

				return len(out)
			}, "SizeKanon must equal the length of the encoding", prop.Using(digests))
		})

		t.Run("returns 0 for the zero Digest", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, crypto.Digest{}.SizeKanon(), 0, "the zero Digest must have no encoding")
		})
	})

	t.Run("ExactKanon", func(t *testing.T) {
		t.Parallel()

		t.Run("declares a type that keeps the guarantees of kanon.Exact", func(t *testing.T) {
			t.Parallel()
			kanontest.RunExact[crypto.Digest](t)
		})
	})

	t.Run("Size", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0 for the zero Digest", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, crypto.Digest{}.Size(), 0, "the zero Digest must have no bytes")
		})
	})

	t.Run("Bytes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a slice whose writes leave d unchanged", func(t *testing.T) {
			t.Parallel()
			d := digest42
			d.Bytes()[0] = 0x00
			assert.Equal(t, d, digest42, "Bytes must return the bytes of a copy of d")
		})

		t.Run("returns an empty slice for the zero Digest", func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, crypto.Digest{}.Bytes(), "the zero Digest must have no bytes")
		})
	})

	t.Run("IsZero", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for the zero Digest", func(t *testing.T) {
			t.Parallel()
			assert.True(t, crypto.Digest{}.IsZero(), "the zero value must be the zero Digest")
		})

		t.Run("reports false for a digest of a size", func(t *testing.T) {
			t.Parallel()
			prop.False(t, crypto.Digest.IsZero, "a digest of a size must not be zero", prop.Using(digests))
		})

		t.Run("reports false for a digest whose bytes are all zero", func(t *testing.T) {
			t.Parallel()
			assert.False(t, crypto.NewDigest256([crypto.DigestSize256]byte{}).IsZero(),
				"IsZero must read the size and not the bytes")
		})

		t.Run("makes json.Marshal omit the zero Digest from a field with omitzero", func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(struct {
				D crypto.Digest `json:",omitzero"`
			}{})
			assert.NoError(t, err, "json.Marshal must omit the zero Digest")
			assert.Equal(t, string(got), "{}", "the field with omitzero must not appear")
		})
	})

	t.Run("Equal", func(t *testing.T) {
		t.Parallel()

		t.Run("reports what == reports", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(p [2]crypto.Digest) bool { return p[0].Equal(p[1]) },
				func(p [2]crypto.Digest) bool { return p[0] == p[1] },
				"Equal must agree with ==", prop.Using(digestPairs))
		})
	})

	t.Run("ConstantTimeEqual", func(t *testing.T) {
		t.Parallel()

		t.Run("reports what Equal reports", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(p [2]crypto.Digest) bool { return p[0].ConstantTimeEqual(p[1]) },
				func(p [2]crypto.Digest) bool { return p[0].Equal(p[1]) },
				"ConstantTimeEqual must agree with Equal", prop.Using(digestPairs))
		})

		t.Run("reports true for two zero Digests", func(t *testing.T) {
			t.Parallel()
			assert.True(t, crypto.Digest{}.ConstantTimeEqual(crypto.Digest{}), "the zero Digest must equal itself")
		})

		t.Run("reports false for digests of two sizes whose bytes are all zero", func(t *testing.T) {
			t.Parallel()
			short := crypto.NewDigest256([crypto.DigestSize256]byte{})
			long := crypto.NewDigest384([crypto.DigestSize384]byte{})
			assert.False(t, short.ConstantTimeEqual(long), "the size must take part in the comparison")
		})
	})

	t.Run("Compare", func(t *testing.T) {
		t.Parallel()

		t.Run("orders digests as bytes.Compare orders their bytes", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(p [2]crypto.Digest) int { return p[0].Compare(p[1]) },
				func(p [2]crypto.Digest) int { return bytes.Compare(p[0].Bytes(), p[1].Bytes()) },
				"Compare must order the active bytes lexicographically", prop.Using(digestPairs))
		})

		t.Run("returns -1 for a shorter digest whose bytes prefix the other", func(t *testing.T) {
			t.Parallel()
			short := crypto.NewDigest256([crypto.DigestSize256]byte{})
			long := crypto.NewDigest384([crypto.DigestSize384]byte{})
			assert.Equal(t, short.Compare(long), -1, "the shorter digest must order first")
		})
	})

	t.Run("String", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the bytes in lowercase hexadecimal", func(t *testing.T) {
			t.Parallel()
			var b [crypto.DigestSize256]byte
			b[0], b[1], b[2] = 0x01, 0x23, 0x45
			b[29], b[30], b[31] = 0xab, 0xcd, 0xef
			assert.Equal(t, crypto.NewDigest256(b).String(),
				"012345"+"0000000000000000000000000000000000000000000000000000"+"abcdef",
				"String must encode the bytes in order")
		})

		t.Run("returns a string that decodes to d", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(d crypto.Digest) (string, error) {
				return d.String(), nil
			}, func(s string) (crypto.Digest, error) {
				b, err := hex.DecodeString(s)
				if err != nil {
					return crypto.Digest{}, err
				}

				return crypto.DigestFromBytes(b)
			}, "String must encode every active byte of d", prop.Using(digests))
		})

		t.Run("returns an empty string for the zero Digest", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, crypto.Digest{}.String(), "", "the zero Digest must have no bytes")
		})
	})
}

// TestDigestAllocs checks the allocation contract of every method of a
// Digest. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestDigestAllocs(t *testing.T) {
	for _, tt := range digestCalls {
		t.Run(tt.name, func(t *testing.T) {
			var ok bool
			expect.MaxAllocs(t, func() { ok = tt.call() }, tt.allocs, "the call must keep its allocation ceiling")
			assert.True(t, ok, "the test must measure a call that returns its expected result")
		})
	}

	t.Run("MarshalBinary", func(t *testing.T) {
		var out []byte
		expect.MaxAllocs(t, func() { out, _ = digest42.MarshalBinary() }, 1,
			"MarshalBinary must allocate only the returned slice")
		assert.Length(t, out, crypto.DigestSize256, "the test must measure an encoding")
	})

	t.Run("String", func(t *testing.T) {
		var s string
		expect.MaxAllocs(t, func() { s = digest42.String() }, 1, "String must allocate only the returned string")
		assert.Length(t, s, 2*crypto.DigestSize256, "the test must measure a string")
	})
}

// BenchmarkDigest reports the cost of each call on the hot path of a
// Digest, and fails when one of them allocates more than TestDigestAllocs
// allows.
func BenchmarkDigest(b *testing.B) {
	for _, tt := range digestCalls {
		b.Run(tt.name, func(b *testing.B) {
			var ok bool

			c := bench.Start(b).MaxAllocs(tt.allocs)
			defer c.End()

			for c.Loop() {
				ok = tt.call()
			}

			assert.True(b, ok, "the benchmark must measure a call that returns its expected result")
		})
	}
}

// newDigest returns the Digest of b, which has DigestSize256,
// DigestSize384 or DigestSize512 bytes, through the constructor of that
// size. It panics on another length, which digestBytes never draws.
func newDigest(b []byte) crypto.Digest {
	if len(b) == crypto.DigestSize256 {
		return crypto.NewDigest256([crypto.DigestSize256]byte(b))
	}
	if len(b) == crypto.DigestSize384 {
		return crypto.NewDigest384([crypto.DigestSize384]byte(b))
	}

	return crypto.NewDigest512([crypto.DigestSize512]byte(b))
}

// digestTextWith returns a generator of the text of a digest of each size
// in which a byte of bad replaces the digit at one position.
func digestTextWith(bad prop.Generator[byte]) prop.Generator[[]byte] {
	return prop.Composite(func(c *prop.Case) []byte {
		text := []byte(hex.EncodeToString(c.Draw(digests, "digest").Bytes()))
		text[c.Draw(prop.Integer(0, len(text)-1), "position")] = c.Draw(bad, "byte")

		return text
	})
}
