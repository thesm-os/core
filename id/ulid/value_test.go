// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ulid_test

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.thesmos.sh/kanon"
	"go.thesmos.sh/kanon/kanontest"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/ulid"
)

// The interfaces that ULID satisfies. A missing method is a build failure.
var (
	_ fmt.Stringer               = ulid.ULID{}
	_ encoding.TextAppender      = ulid.ULID{}
	_ encoding.TextMarshaler     = ulid.ULID{}
	_ encoding.TextUnmarshaler   = (*ulid.ULID)(nil)
	_ encoding.BinaryAppender    = ulid.ULID{}
	_ encoding.BinaryMarshaler   = ulid.ULID{}
	_ encoding.BinaryUnmarshaler = (*ulid.ULID)(nil)
	_ kanon.Appender             = ulid.ULID{}
)

// zeroText is the text of the zero ULID: 26 zeros.
const zeroText = "00000000000000000000000000"

// specULID is the ULID of specBytes.
var specULID = ulid.ULID(specBytes)

// values generates ULIDs of any 16 bytes, the zero ULID included.
var values = prop.Bytes(prop.MinSize(id.Size128), prop.MaxSize(id.Size128)).Map(func(b []byte) ulid.ULID {
	return ulid.ULID(b)
})

// nonZeroValues generates the ULIDs other than the zero ULID.
var nonZeroValues = values.Filter(func(u ulid.ULID) bool { return u != ulid.ULID{} })

// ulidTexts generates the text of a ULID in uppercase or in lowercase.
var ulidTexts = prop.Composite(func(c *prop.Case) []byte {
	text := []byte(ulid.Format(c.Draw(ulids, "id")))
	if c.Draw(prop.Boolean(), "lowercase") {
		return bytes.ToLower(text)
	}

	return text
})

// refusedData generates data that is not the binary form of a ULID: up to 40
// bytes of another length than 16, and 16 zero bytes.
var refusedData = prop.OneOf(
	prop.Bytes(prop.MinSize(1), prop.MaxSize(40)).Filter(func(b []byte) bool { return len(b) != id.Size128 }),
	prop.Just(make([]byte, id.Size128)),
)

// ulidDocument is a JSON document with a field of ULID. The option omitzero
// leaves out the field when it is the zero ULID.
type ulidDocument struct {
	ULID ulid.ULID `json:"ulid,omitzero"`
}

func TestValue(t *testing.T) {
	t.Parallel()

	t.Run("FromID", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the ULID with the bytes of a 128-bit ID", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "FromID must copy the bytes of a 128-bit ID", func(c *prop.Case) {
				i := c.Draw(ulids, "id")

				got, ok := ulid.FromID(i)
				assert.True(c, ok, "FromID must convert a 128-bit ID")
				assert.Equal(c, got[:], i.Bytes(), "the ULID must have the bytes of the ID")
			})
		})

		t.Run("returns the ULID whose ID it converts", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "FromID must invert ID", func(c *prop.Case) {
				u := c.Draw(values, "ulid")

				got, ok := ulid.FromID(u.ID())
				assert.True(c, ok, "FromID must convert the ID of a ULID")
				assert.Equal(c, got, u, "FromID must return the ULID of the ID")
			})
		})

		t.Run("returns the zero ULID for Zero", func(t *testing.T) {
			t.Parallel()
			got, ok := ulid.FromID(id.Zero)
			assert.True(t, ok, "FromID must convert Zero")
			assert.Equal(t, got, ulid.ULID{}, "Zero must convert to the zero ULID")
		})

		tests := []struct {
			name string
			give id.ID
		}{
			{name: "reports false for a 160-bit ID", give: id.New160([id.Size160]byte{1})},
			{name: "reports false for a 256-bit ID", give: id.New256([id.Size256]byte{1})},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := ulid.FromID(tt.give)
				expect.False(t, ok, "FromID must refuse an ID of another size")
				expect.Equal(t, got, ulid.ULID{}, "FromID must return the zero ULID for an ID that it refuses")
			})
		}
	})

	t.Run("ID", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the 128-bit ID of the bytes of a ULID other than zero", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, ulid.ULID.ID, func(u ulid.ULID) id.ID { return id.New128(u) },
				"ID must return the ID of the bytes of the ULID", prop.Using(nonZeroValues))
		})

		t.Run("returns Zero for the zero ULID", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, ulid.ULID{}.ID(), id.Zero, "the zero ULID must convert to Zero")
		})
	})

	t.Run("IsZero", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for the zero ULID", func(t *testing.T) {
			t.Parallel()
			assert.True(t, ulid.ULID{}.IsZero(), "the zero ULID must be zero")
		})

		t.Run("reports false for a ULID other than zero", func(t *testing.T) {
			t.Parallel()
			prop.False(t, ulid.ULID.IsZero, "a ULID with a byte other than zero is not zero",
				prop.Using(nonZeroValues))
		})
	})

	t.Run("Compare", func(t *testing.T) {
		t.Parallel()

		t.Run("returns what bytes.Compare returns for the bytes of the ULIDs", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Compare must order ULIDs as their bytes", func(c *prop.Case) {
				a := c.Draw(values, "a")
				b := c.Draw(values, "b")
				assert.Equal(c, a.Compare(b), bytes.Compare(a[:], b[:]), "Compare must return the order of the bytes")
			})
		})
	})

	t.Run("String", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the text that Format returns for the ID of the bytes", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, ulid.ULID.String, func(u ulid.ULID) string { return ulid.Format(id.New128(u)) },
				"String must return the text of the bytes", prop.Using(values))
		})

		t.Run("returns 26 zeros for the zero ULID", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, ulid.ULID{}.String(), zeroText, "the zero ULID must have the text of 26 zeros")
		})
	})

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the text that String returns to dst", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendText must keep dst and append the text of the ULID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(8)), "dst")
				u := c.Draw(values, "ulid")
				want := append(slices.Clone(dst), u.String()...)

				got, err := u.AppendText(dst)
				assert.NoError(c, err, "AppendText must succeed")
				assert.Equal(c, got, want, "AppendText must extend dst with the text of the ULID")
			})
		})
	})

	t.Run("MarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the text that String returns", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "MarshalText must return the text of String", func(c *prop.Case) {
				u := c.Draw(values, "ulid")

				got, err := u.MarshalText()
				assert.NoError(c, err, "MarshalText must succeed")
				assert.Equal(c, string(got), u.String(), "MarshalText must return the text of the ULID")
			})
		})

		t.Run("makes json.Marshal write a field as a JSON string of the text", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "json.Marshal must write a ULID as the JSON string of its text", func(c *prop.Case) {
				u := c.Draw(nonZeroValues, "ulid")

				got, err := json.Marshal(ulidDocument{ULID: u})
				assert.NoError(c, err, "json.Marshal must encode a ULID")
				assert.Equal(c, string(got), `{"ulid":"`+ulid.Format(id.New128(u))+`"}`,
					"the field must be the JSON string of the text of the ULID")
			})
		})

		t.Run("makes json.Marshal write a map key as the text", func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(map[ulid.ULID]int{specULID: 1})
			assert.NoError(t, err, "json.Marshal must encode a map keyed by ULIDs")
			assert.Equal(t, string(got), `{"`+specText+`":1}`, "the name of the member must be the text of the key")
		})

		t.Run("makes json.Marshal leave out the zero ULID of a field with omitzero", func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(ulidDocument{})
			assert.NoError(t, err, "json.Marshal must encode the zero ULID")
			assert.Equal(t, string(got), `{}`, "omitzero must leave out the zero ULID")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("decodes the text that MarshalText returns", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, ulid.ULID.MarshalText, func(text []byte) (ulid.ULID, error) {
				var u ulid.ULID
				err := u.UnmarshalText(text)

				return u, err
			}, "UnmarshalText must invert MarshalText", prop.Using(values))
		})

		t.Run("sets u to the bytes that ParseULID decodes from a text that ParseULID accepts", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalText must accept every text that ParseULID accepts", func(c *prop.Case) {
				text := c.Draw(ulidTexts, "text")
				want, err := ulid.ParseULID(string(text))
				assert.NoError(c, err, "the property must draw a text that ParseULID accepts")

				var got ulid.ULID
				assert.NoError(c, got.UnmarshalText(text), "UnmarshalText must accept a text that ParseULID accepts")
				assert.Equal(c, got[:], want.Bytes(), "UnmarshalText must decode the bytes that ParseULID decodes")
			})
		})

		t.Run("sets u to the bytes of the digit for a substitution", func(t *testing.T) {
			t.Parallel()
			var got ulid.ULID
			assert.NoError(t, got.UnmarshalText([]byte("0iIlLoO"+strings.Repeat("0", 19))),
				"UnmarshalText must accept the substitutions of the ULID specification")
			assert.Equal(t, got.String(), "0111100"+strings.Repeat("0", 19), "a substitution must decode as its digit")
		})

		t.Run("returns the error that ParseULID returns for a text that ParseULID refuses", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalText must return the sentinel of ParseULID", func(c *prop.Case) {
				text := c.Draw(refusedTexts, "text")
				_, want := ulid.ParseULID(string(text))

				var u ulid.ULID
				assert.Equal(c, u.UnmarshalText(text), want, "UnmarshalText must return the error of ParseULID")
			})
		})

		t.Run("leaves u unchanged for a text that it refuses", func(t *testing.T) {
			t.Parallel()
			u := specULID
			prop.Pure(t, func() ulid.ULID { return u }, func(text []byte) {
				_ = u.UnmarshalText(text)
			}, "a refused decode must leave the ULID unchanged", prop.Using(refusedTexts))
		})

		t.Run("sets the zero ULID for 26 zeros", func(t *testing.T) {
			t.Parallel()
			got := specULID
			assert.NoError(t, got.UnmarshalText([]byte(zeroText)), "UnmarshalText must accept 26 zeros")
			assert.Equal(t, got, ulid.ULID{}, "26 zeros must decode to the zero ULID")
		})

		t.Run("returns ErrInvalidLength for the empty text", func(t *testing.T) {
			t.Parallel()
			var u ulid.ULID
			assert.ErrorIs(t, u.UnmarshalText(nil), ulid.ErrInvalidLength, "UnmarshalText must refuse the empty text")
		})

		t.Run("decodes a JSON field that json.Marshal writes", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(u ulid.ULID) ([]byte, error) {
				return json.Marshal(ulidDocument{ULID: u})
			}, func(document []byte) (ulid.ULID, error) {
				var v ulidDocument
				err := json.Unmarshal(document, &v)

				return v.ULID, err
			}, "json.Unmarshal must undo json.Marshal of a field", prop.Using(values))
		})

		t.Run("decodes the JSON object that json.Marshal writes for a map", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(m map[ulid.ULID]int) ([]byte, error) {
				return json.Marshal(m)
			}, func(document []byte) (map[ulid.ULID]int, error) {
				var m map[ulid.ULID]int
				err := json.Unmarshal(document, &m)

				return m, err
			}, "json.Unmarshal must undo json.Marshal of a map keyed by ULIDs",
				prop.Using(prop.Dict(values, prop.Integer(0, 9), prop.MaxSize(4))))
		})

		t.Run("returns ErrInvalidLength through json.Unmarshal", func(t *testing.T) {
			t.Parallel()
			var got ulidDocument
			err := json.Unmarshal([]byte(`{"ulid":"01ARZ3NDEK"}`), &got)
			assert.ErrorIs(t, err, ulid.ErrInvalidLength, "json.Unmarshal must return the error of UnmarshalText")
		})
	})

	t.Run("AppendBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the 16 bytes of a ULID other than zero to dst", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendBinary must keep dst and append the bytes of the ULID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(8)), "dst")
				u := c.Draw(nonZeroValues, "ulid")
				want := append(slices.Clone(dst), u[:]...)

				got, err := u.AppendBinary(dst)
				assert.NoError(c, err, "AppendBinary must succeed")
				assert.Equal(c, got, want, "AppendBinary must extend dst with the bytes of the ULID")
			})
		})

		t.Run("returns dst unchanged for the zero ULID", func(t *testing.T) {
			t.Parallel()
			got, err := ulid.ULID{}.AppendBinary([]byte(specText))
			assert.NoError(t, err, "AppendBinary must succeed")
			assert.Equal(t, string(got), specText, "the binary form of the zero ULID must be no bytes")
		})

		t.Run("appends what AppendBinary of the ID of the ULID appends", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendBinary must append the binary form of the ID of the ULID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(8)), "dst")
				u := c.Draw(values, "ulid")

				want, err := u.ID().AppendBinary(slices.Clone(dst))
				assert.NoError(c, err, "AppendBinary of the ID must succeed")

				got, err := u.AppendBinary(dst)
				assert.NoError(c, err, "AppendBinary must succeed")
				assert.Equal(c, got, want, "AppendBinary must append the bytes of the ID of the ULID",
					assert.EquateEmpty())
			})
		})
	})

	t.Run("MarshalBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the bytes that AppendBinary appends", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "MarshalBinary must return the binary form", func(c *prop.Case) {
				u := c.Draw(values, "ulid")
				want, err := u.AppendBinary(nil)
				assert.NoError(c, err, "AppendBinary must succeed")

				got, err := u.MarshalBinary()
				assert.NoError(c, err, "MarshalBinary must succeed")
				assert.Equal(c, got, want, "MarshalBinary must return the bytes that AppendBinary appends",
					assert.EquateEmpty())
			})
		})
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("decodes the bytes that MarshalBinary returns", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, ulid.ULID.MarshalBinary, func(data []byte) (ulid.ULID, error) {
				var u ulid.ULID
				err := u.UnmarshalBinary(data)

				return u, err
			}, "UnmarshalBinary must invert MarshalBinary", prop.Using(values))
		})

		t.Run("sets the zero ULID for no bytes", func(t *testing.T) {
			t.Parallel()
			got := specULID
			assert.NoError(t, got.UnmarshalBinary(nil), "UnmarshalBinary must accept no bytes")
			assert.Equal(t, got, ulid.ULID{}, "no bytes must decode to the zero ULID")
		})

		t.Run("returns ErrInvalidBytes for a length that no ULID encodes to", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				var u ulid.ULID

				return u.UnmarshalBinary(make([]byte, n))
			}, ulid.ErrInvalidBytes, "UnmarshalBinary must refuse a length that no ULID encodes to",
				prop.Using(prop.Integer(1, 40).Filter(func(n int) bool { return n != id.Size128 })),
				prop.Examples(1, id.Size128-1, id.Size128+1))
		})

		t.Run("returns ErrInvalidBytes for 16 zero bytes", func(t *testing.T) {
			t.Parallel()
			var u ulid.ULID
			err := u.UnmarshalBinary(make([]byte, id.Size128))
			expect.ErrorIs(t, err, ulid.ErrInvalidBytes, "UnmarshalBinary must refuse 16 zero bytes")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrInvalidBytes must classify as Invalid")
		})

		t.Run("leaves u unchanged for data that it refuses", func(t *testing.T) {
			t.Parallel()
			u := specULID
			prop.Pure(t, func() ulid.ULID { return u }, func(data []byte) {
				_ = u.UnmarshalBinary(data)
			}, "a refused decode must leave the ULID unchanged", prop.Using(refusedData))
		})

		t.Run("returns a ULID that a later write to data leaves unchanged", func(t *testing.T) {
			t.Parallel()
			data := slices.Clone(specBytes[:])
			var got ulid.ULID
			assert.NoError(t, got.UnmarshalBinary(data), "UnmarshalBinary must accept the bytes of a ULID")
			data[0] = 0xff
			assert.Equal(t, got, specULID, "a write to data must not change the ULID")
		})
	})

	t.Run("SizeKanon", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the length of the bytes that AppendKanon appends", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, ulid.ULID.SizeKanon, func(u ulid.ULID) int { return len(u.AppendKanon(nil)) },
				"SizeKanon must size the binary form", prop.Using(values))
		})
	})

	t.Run("AppendKanon", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the bytes that AppendBinary appends", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendKanon must append the binary form of the ULID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(8)), "dst")
				u := c.Draw(values, "ulid")

				want, err := u.AppendBinary(slices.Clone(dst))
				assert.NoError(c, err, "AppendBinary must succeed")
				assert.Equal(c, u.AppendKanon(dst), want, "AppendKanon must equal AppendBinary", assert.EquateEmpty())
			})
		})
	})
}

// TestExactKanon checks the two guarantees that ExactKanon declares, and the
// bytes of AppendKanon, with kanon's conformance suite.
func TestExactKanon(t *testing.T) {
	t.Parallel()
	kanontest.RunExact[ulid.ULID](t)
}

// TestValueAllocs checks the allocation contracts of FromID and the methods
// of ULID. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestValueAllocs(t *testing.T) {
	text := []byte(specText)
	data := specBytes[:]
	specID := id.New128(specBytes)

	t.Run("FromID", func(t *testing.T) {
		var (
			got ulid.ULID
			ok  bool
		)
		expect.MaxAllocs(t, func() { got, ok = ulid.FromID(specID) }, 0, "FromID must not allocate")
		assert.True(t, ok, "the test must measure a conversion that succeeds")
		assert.Equal(t, got, specULID, "the test must measure the ULID of the ID")
	})

	t.Run("ID", func(t *testing.T) {
		var got id.ID
		expect.MaxAllocs(t, func() { got = specULID.ID() }, 0, "ID must not allocate")
		assert.Equal(t, got, specID, "the test must measure the ID of the ULID")
	})

	t.Run("IsZero", func(t *testing.T) {
		got := true
		expect.MaxAllocs(t, func() { got = specULID.IsZero() }, 0, "IsZero must not allocate")
		assert.False(t, got, "the test must measure a ULID other than zero")
	})

	t.Run("Compare", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = specULID.Compare(ulid.ULID{}) }, 0, "Compare must not allocate")
		assert.Equal(t, got, 1, "the test must measure the order of two ULIDs")
	})

	t.Run("String", func(t *testing.T) {
		var got string
		expect.MaxAllocs(t, func() { got = specULID.String() }, 1, "String must allocate only the returned string")
		assert.Equal(t, got, specText, "the test must measure the text of the ULID")
	})

	t.Run("AppendText", func(t *testing.T) {
		buf := make([]byte, 0, len(specText))

		var err error
		expect.MaxAllocs(t, func() { buf, err = specULID.AppendText(buf[:0]) }, 0,
			"AppendText must not allocate when dst has room for the text")
		assert.NoError(t, err, "AppendText must succeed")
		assert.Equal(t, string(buf), specText, "the test must measure the text of the ULID")
	})

	t.Run("MarshalText", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got, _ = specULID.MarshalText() }, 1,
			"MarshalText must allocate only the returned slice")
		assert.Equal(t, string(got), specText, "the test must measure the text of the ULID")
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		var (
			got ulid.ULID
			err error
		)
		expect.MaxAllocs(t, func() { err = got.UnmarshalText(text) }, 0, "UnmarshalText must not allocate")
		assert.NoError(t, err, "the test must measure a decode that succeeds")
		assert.Equal(t, got, specULID, "the test must measure the decode of the text")
	})

	t.Run("AppendBinary", func(t *testing.T) {
		buf := make([]byte, 0, id.Size128)

		var err error
		expect.MaxAllocs(t, func() { buf, err = specULID.AppendBinary(buf[:0]) }, 0,
			"AppendBinary must not allocate when dst has room for the bytes")
		assert.NoError(t, err, "AppendBinary must succeed")
		assert.Equal(t, buf, data, "the test must measure the binary form of the ULID")
	})

	t.Run("MarshalBinary", func(t *testing.T) {
		t.Run("allocates only the slice that it returns", func(t *testing.T) {
			var got []byte
			expect.MaxAllocs(t, func() { got, _ = specULID.MarshalBinary() }, 1,
				"MarshalBinary must allocate only the returned slice")
			assert.Equal(t, got, data, "the test must measure the binary form of the ULID")
		})

		t.Run("allocates nothing for the zero ULID", func(t *testing.T) {
			got := []byte{0}
			expect.MaxAllocs(t, func() { got, _ = ulid.ULID{}.MarshalBinary() }, 0,
				"MarshalBinary must not allocate for the zero ULID")
			assert.Empty(t, got, "the test must measure the binary form of the zero ULID")
		})
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		var (
			got ulid.ULID
			err error
		)
		expect.MaxAllocs(t, func() { err = got.UnmarshalBinary(data) }, 0, "UnmarshalBinary must not allocate")
		assert.NoError(t, err, "the test must measure a decode that succeeds")
		assert.Equal(t, got, specULID, "the test must measure the decode of the bytes")
	})

	t.Run("SizeKanon", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = specULID.SizeKanon() }, 0, "SizeKanon must not allocate")
		assert.Equal(t, got, id.Size128, "the test must measure the size of the ULID")
	})

	t.Run("AppendKanon", func(t *testing.T) {
		buf := make([]byte, 0, id.Size128)
		expect.MaxAllocs(t, func() { buf = specULID.AppendKanon(buf[:0]) }, 0,
			"AppendKanon must not allocate when dst has room for the bytes")
		assert.Equal(t, buf, data, "the test must measure the binary form of the ULID")
	})
}

// BenchmarkValue reports the cost of FromID and the methods of ULID. It
// fails when one of them allocates more than TestValueAllocs allows.
func BenchmarkValue(b *testing.B) {
	text := []byte(specText)
	data := specBytes[:]
	specID := id.New128(specBytes)

	b.Run("FromID", func(b *testing.B) {
		var got ulid.ULID

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = ulid.FromID(specID)
		}

		assert.Equal(b, got, specULID, "the benchmark must measure the ULID of the ID")
	})

	b.Run("ID", func(b *testing.B) {
		var got id.ID

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = specULID.ID()
		}

		assert.Equal(b, got, specID, "the benchmark must measure the ID of the ULID")
	})

	b.Run("IsZero", func(b *testing.B) {
		got := true

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = specULID.IsZero()
		}

		assert.False(b, got, "the benchmark must measure a ULID other than zero")
	})

	b.Run("Compare", func(b *testing.B) {
		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = specULID.Compare(ulid.ULID{})
		}

		assert.Equal(b, got, 1, "the benchmark must measure the order of two ULIDs")
	})

	b.Run("String", func(b *testing.B) {
		var got string

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got = specULID.String()
		}

		assert.Equal(b, got, specText, "the benchmark must measure the text of the ULID")
	})

	b.Run("AppendText", func(b *testing.B) {
		buf := make([]byte, 0, len(specText))
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			buf, err = specULID.AppendText(buf[:0])
		}

		assert.NoError(b, err, "AppendText must succeed")
		assert.Equal(b, string(buf), specText, "the benchmark must measure the text of the ULID")
	})

	b.Run("MarshalText", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = specULID.MarshalText()
		}

		assert.Equal(b, string(got), specText, "the benchmark must measure the text of the ULID")
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		var (
			got ulid.ULID
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = got.UnmarshalText(text)
		}

		assert.NoError(b, err, "the benchmark must measure a decode that succeeds")
		assert.Equal(b, got, specULID, "the benchmark must measure the decode of the text")
	})

	b.Run("AppendBinary", func(b *testing.B) {
		buf := make([]byte, 0, id.Size128)
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			buf, err = specULID.AppendBinary(buf[:0])
		}

		assert.NoError(b, err, "AppendBinary must succeed")
		assert.Equal(b, buf, data, "the benchmark must measure the binary form of the ULID")
	})

	b.Run("MarshalBinary", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = specULID.MarshalBinary()
		}

		assert.Equal(b, got, data, "the benchmark must measure the binary form of the ULID")
	})

	b.Run("UnmarshalBinary", func(b *testing.B) {
		var (
			got ulid.ULID
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = got.UnmarshalBinary(data)
		}

		assert.NoError(b, err, "the benchmark must measure a decode that succeeds")
		assert.Equal(b, got, specULID, "the benchmark must measure the decode of the bytes")
	})

	b.Run("SizeKanon", func(b *testing.B) {
		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = specULID.SizeKanon()
		}

		assert.Equal(b, got, id.Size128, "the benchmark must measure the size of the ULID")
	})

	b.Run("AppendKanon", func(b *testing.B) {
		buf := make([]byte, 0, id.Size128)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			buf = specULID.AppendKanon(buf[:0])
		}

		assert.Equal(b, buf, data, "the benchmark must measure the binary form of the ULID")
	})
}
