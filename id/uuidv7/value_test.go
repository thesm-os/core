// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv7_test

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.thesmos.sh/kanon"
	"go.thesmos.sh/kanon/kanontest"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/uuidv7"
	"go.thesmos.sh/core/rand/constant"
)

// The interfaces that UUID satisfies. A missing method is a build failure.
var (
	_ fmt.Stringer               = uuidv7.UUID{}
	_ encoding.TextAppender      = uuidv7.UUID{}
	_ encoding.TextMarshaler     = uuidv7.UUID{}
	_ encoding.TextUnmarshaler   = (*uuidv7.UUID)(nil)
	_ encoding.BinaryAppender    = uuidv7.UUID{}
	_ encoding.BinaryMarshaler   = uuidv7.UUID{}
	_ encoding.BinaryUnmarshaler = (*uuidv7.UUID)(nil)
	_ kanon.Appender             = uuidv7.UUID{}
)

// nilText is the text of the Nil UUID of RFC 9562, which is the text of the
// zero UUID.
const nilText = "00000000-0000-0000-0000-000000000000"

// The version of a UUIDv7 and the variant of RFC 9562. A UUIDv7 has the
// version in the high 4 bits of byte 6 and the variant in the high 2 bits of
// byte 8.
const (
	uuidVersion = 7
	uuidVariant = 0b10
)

// exampleUUID is the UUID of exampleBytes.
var exampleUUID = uuidv7.UUID(exampleBytes)

// generated generates the UUIDs that a Generator returns for a drawn clock
// time and a drawn value of its source.
var generated = prop.Composite(func(c *prop.Case) uuidv7.UUID {
	clk := fake.New(time.Unix(0, c.Draw(prop.Integer[int64](0, math.MaxInt64), "nanoseconds")))
	src := constant.New(c.Draw(prop.Integer[uint64](0, math.MaxUint64), "random"))

	return uuidv7.UUID(uuidv7.New(clk, src).Generate().Bytes())
})

// otherVersions generates the values of the high 4 bits of byte 6 other than
// the version of a UUIDv7.
var otherVersions = prop.Integer(byte(0), byte(15)).Filter(func(v byte) bool { return v != uuidVersion })

// otherVariants generates the values of the high 2 bits of byte 8 other than
// the variant of RFC 9562.
var otherVariants = prop.Integer(byte(0), byte(3)).Filter(func(v byte) bool { return v != uuidVariant })

// values generates UUIDs of any 16 bytes, the zero UUID included.
var values = prop.Bytes(prop.MinSize(id.Size128), prop.MaxSize(id.Size128)).Map(func(b []byte) uuidv7.UUID {
	return uuidv7.UUID(b)
})

// nonZeroValues generates the UUIDs other than the zero UUID.
var nonZeroValues = values.Filter(func(u uuidv7.UUID) bool { return u != uuidv7.UUID{} })

// uuidTexts generates the text of a UUID in lowercase or in uppercase.
var uuidTexts = prop.Composite(func(c *prop.Case) []byte {
	text := []byte(uuidv7.Format(c.Draw(uuids, "id")))
	if c.Draw(prop.Boolean(), "uppercase") {
		return bytes.ToUpper(text)
	}

	return text
})

// refusedTexts generates texts that Parse refuses: up to 40 arbitrary bytes,
// and the text of a UUID with the letter g in place of one byte.
var refusedTexts = prop.OneOf(
	prop.Bytes(prop.MaxSize(40)),
	prop.Composite(func(c *prop.Case) []byte {
		text := []byte(uuidv7.Format(c.Draw(uuids, "id")))
		text[c.Draw(prop.Integer(0, len(text)-1), "position")] = 'g'

		return text
	}),
).Filter(func(text []byte) bool {
	_, err := uuidv7.Parse(string(text))

	return err != nil
})

// refusedData generates data that is not the binary form of a UUID: up to 40
// bytes of another length than 16, and 16 zero bytes.
var refusedData = prop.OneOf(
	prop.Bytes(prop.MinSize(1), prop.MaxSize(40)).Filter(func(b []byte) bool { return len(b) != id.Size128 }),
	prop.Just(make([]byte, id.Size128)),
)

// uuidDocument is a JSON document with a field of UUID. The option omitzero
// leaves out the field when it is the zero UUID.
type uuidDocument struct {
	UUID uuidv7.UUID `json:"uuid,omitzero"`
}

func TestValue(t *testing.T) {
	t.Parallel()

	t.Run("FromID", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the UUID with the bytes of a 128-bit ID", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "FromID must copy the bytes of a 128-bit ID", func(c *prop.Case) {
				i := c.Draw(uuids, "id")

				got, ok := uuidv7.FromID(i)
				assert.True(c, ok, "FromID must convert a 128-bit ID")
				assert.Equal(c, got[:], i.Bytes(), "the UUID must have the bytes of the ID")
			})
		})

		t.Run("returns the UUID whose ID it converts", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "FromID must invert ID", func(c *prop.Case) {
				u := c.Draw(values, "uuid")

				got, ok := uuidv7.FromID(u.ID())
				assert.True(c, ok, "FromID must convert the ID of a UUID")
				assert.Equal(c, got, u, "FromID must return the UUID of the ID")
			})
		})

		t.Run("returns the zero UUID for Zero", func(t *testing.T) {
			t.Parallel()
			got, ok := uuidv7.FromID(id.Zero)
			assert.True(t, ok, "FromID must convert Zero")
			assert.Equal(t, got, uuidv7.UUID{}, "Zero must convert to the zero UUID")
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
				got, ok := uuidv7.FromID(tt.give)
				expect.False(t, ok, "FromID must refuse an ID of another size")
				expect.Equal(t, got, uuidv7.UUID{}, "FromID must return the zero UUID for an ID that it refuses")
			})
		}
	})

	t.Run("ID", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the 128-bit ID of the bytes of a UUID other than zero", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, uuidv7.UUID.ID, func(u uuidv7.UUID) id.ID { return id.New128(u) },
				"ID must return the ID of the bytes of the UUID", prop.Using(nonZeroValues))
		})

		t.Run("returns Zero for the zero UUID", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, uuidv7.UUID{}.ID(), id.Zero, "the zero UUID must convert to Zero")
		})
	})

	t.Run("IsZero", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for the zero UUID", func(t *testing.T) {
			t.Parallel()
			assert.True(t, uuidv7.UUID{}.IsZero(), "the zero UUID must be zero")
		})

		t.Run("reports false for a UUID other than zero", func(t *testing.T) {
			t.Parallel()
			prop.False(t, uuidv7.UUID.IsZero, "a UUID with a byte other than zero is not zero",
				prop.Using(nonZeroValues))
		})
	})

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for a UUID that Generate returns", func(t *testing.T) {
			t.Parallel()
			prop.True(t, uuidv7.UUID.Valid, "a UUID of a Generator is a UUIDv7", prop.Using(generated))
		})

		t.Run("reports false for a UUID of another version", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Valid must report false for every version other than 7", func(c *prop.Case) {
				u := c.Draw(generated, "uuid")
				u[6] = u[6]&0x0F | c.Draw(otherVersions, "version")<<4
				assert.False(c, u.Valid(), "a UUID of another version is not a UUIDv7")
			})
		})

		t.Run("reports false for a UUID of another variant", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Valid must report false for every variant other than the variant of RFC 9562",
				func(c *prop.Case) {
					u := c.Draw(generated, "uuid")
					u[8] = u[8]&0x3F | c.Draw(otherVariants, "variant")<<6
					assert.False(c, u.Valid(), "a UUID of another variant is not a UUIDv7")
				})
		})

		t.Run("reports false for the zero UUID", func(t *testing.T) {
			t.Parallel()
			assert.False(t, uuidv7.UUID{}.Valid(), "the Nil UUID is not a UUIDv7")
		})
	})

	t.Run("Compare", func(t *testing.T) {
		t.Parallel()

		t.Run("returns what bytes.Compare returns for the bytes of the UUIDs", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Compare must order UUIDs as their bytes", func(c *prop.Case) {
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
			prop.Equal(t, uuidv7.UUID.String, func(u uuidv7.UUID) string { return uuidv7.Format(id.New128(u)) },
				"String must return the text of the bytes", prop.Using(values))
		})

		t.Run("returns the text of the Nil UUID for the zero UUID", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, uuidv7.UUID{}.String(), nilText, "the zero UUID must have the text of the Nil UUID")
		})
	})

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the text that String returns to dst", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendText must keep dst and append the text of the UUID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(8)), "dst")
				u := c.Draw(values, "uuid")
				want := append(slices.Clone(dst), u.String()...)

				got, err := u.AppendText(dst)
				assert.NoError(c, err, "AppendText must succeed")
				assert.Equal(c, got, want, "AppendText must extend dst with the text of the UUID")
			})
		})
	})

	t.Run("MarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the text that String returns", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "MarshalText must return the text of String", func(c *prop.Case) {
				u := c.Draw(values, "uuid")

				got, err := u.MarshalText()
				assert.NoError(c, err, "MarshalText must succeed")
				assert.Equal(c, string(got), u.String(), "MarshalText must return the text of the UUID")
			})
		})

		t.Run("makes json.Marshal write a field as a JSON string of the text", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "json.Marshal must write a UUID as the JSON string of its text", func(c *prop.Case) {
				u := c.Draw(nonZeroValues, "uuid")

				got, err := json.Marshal(uuidDocument{UUID: u})
				assert.NoError(c, err, "json.Marshal must encode a UUID")
				assert.Equal(c, string(got), `{"uuid":"`+uuidv7.Format(id.New128(u))+`"}`,
					"the field must be the JSON string of the text of the UUID")
			})
		})

		t.Run("makes json.Marshal write a map key as the text", func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(map[uuidv7.UUID]int{exampleUUID: 1})
			assert.NoError(t, err, "json.Marshal must encode a map keyed by UUIDs")
			assert.Equal(t, string(got), `{"`+exampleText+`":1}`, "the name of the member must be the text of the key")
		})

		t.Run("makes json.Marshal leave out the zero UUID of a field with omitzero", func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(uuidDocument{})
			assert.NoError(t, err, "json.Marshal must encode the zero UUID")
			assert.Equal(t, string(got), `{}`, "omitzero must leave out the zero UUID")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("decodes the text that MarshalText returns", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, uuidv7.UUID.MarshalText, func(text []byte) (uuidv7.UUID, error) {
				var u uuidv7.UUID
				err := u.UnmarshalText(text)

				return u, err
			}, "UnmarshalText must invert MarshalText", prop.Using(values))
		})

		t.Run("sets u to the bytes that Parse decodes from a text that Parse accepts", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalText must accept every text that Parse accepts", func(c *prop.Case) {
				text := c.Draw(uuidTexts, "text")
				want, err := uuidv7.Parse(string(text))
				assert.NoError(c, err, "the property must draw a text that Parse accepts")

				var got uuidv7.UUID
				assert.NoError(c, got.UnmarshalText(text), "UnmarshalText must accept a text that Parse accepts")
				assert.Equal(c, got[:], want.Bytes(), "UnmarshalText must decode the bytes that Parse decodes")
			})
		})

		t.Run("returns the error that Parse returns for a text that Parse refuses", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalText must return the sentinel of Parse", func(c *prop.Case) {
				text := c.Draw(refusedTexts, "text")
				_, want := uuidv7.Parse(string(text))

				var u uuidv7.UUID
				assert.Equal(c, u.UnmarshalText(text), want, "UnmarshalText must return the error of Parse")
			})
		})

		t.Run("leaves u unchanged for a text that it refuses", func(t *testing.T) {
			t.Parallel()
			u := exampleUUID
			prop.Pure(t, func() uuidv7.UUID { return u }, func(text []byte) {
				_ = u.UnmarshalText(text)
			}, "a refused decode must leave the UUID unchanged", prop.Using(refusedTexts))
		})

		t.Run("sets the zero UUID for the text of the Nil UUID", func(t *testing.T) {
			t.Parallel()
			got := exampleUUID
			assert.NoError(t, got.UnmarshalText([]byte(nilText)), "UnmarshalText must accept the text of the Nil UUID")
			assert.Equal(t, got, uuidv7.UUID{}, "the text of the Nil UUID must decode to the zero UUID")
		})

		t.Run("returns ErrInvalidLength for the empty text", func(t *testing.T) {
			t.Parallel()
			var u uuidv7.UUID
			assert.ErrorIs(t, u.UnmarshalText(nil), uuidv7.ErrInvalidLength, "UnmarshalText must refuse the empty text")
		})

		t.Run("decodes a JSON field that json.Marshal writes", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(u uuidv7.UUID) ([]byte, error) {
				return json.Marshal(uuidDocument{UUID: u})
			}, func(document []byte) (uuidv7.UUID, error) {
				var v uuidDocument
				err := json.Unmarshal(document, &v)

				return v.UUID, err
			}, "json.Unmarshal must undo json.Marshal of a field", prop.Using(values))
		})

		t.Run("decodes the JSON object that json.Marshal writes for a map", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(m map[uuidv7.UUID]int) ([]byte, error) {
				return json.Marshal(m)
			}, func(document []byte) (map[uuidv7.UUID]int, error) {
				var m map[uuidv7.UUID]int
				err := json.Unmarshal(document, &m)

				return m, err
			}, "json.Unmarshal must undo json.Marshal of a map keyed by UUIDs",
				prop.Using(prop.Dict(values, prop.Integer(0, 9), prop.MaxSize(4))))
		})

		t.Run("returns ErrInvalidLength through json.Unmarshal", func(t *testing.T) {
			t.Parallel()
			var got uuidDocument
			err := json.Unmarshal([]byte(`{"uuid":"12345678"}`), &got)
			assert.ErrorIs(t, err, uuidv7.ErrInvalidLength, "json.Unmarshal must return the error of UnmarshalText")
		})
	})

	t.Run("AppendBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the 16 bytes of a UUID other than zero to dst", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendBinary must keep dst and append the bytes of the UUID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(8)), "dst")
				u := c.Draw(nonZeroValues, "uuid")
				want := append(slices.Clone(dst), u[:]...)

				got, err := u.AppendBinary(dst)
				assert.NoError(c, err, "AppendBinary must succeed")
				assert.Equal(c, got, want, "AppendBinary must extend dst with the bytes of the UUID")
			})
		})

		t.Run("returns dst unchanged for the zero UUID", func(t *testing.T) {
			t.Parallel()
			got, err := uuidv7.UUID{}.AppendBinary([]byte(exampleText))
			assert.NoError(t, err, "AppendBinary must succeed")
			assert.Equal(t, string(got), exampleText, "the binary form of the zero UUID must be no bytes")
		})

		t.Run("appends what AppendBinary of the ID of the UUID appends", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendBinary must append the binary form of the ID of the UUID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(8)), "dst")
				u := c.Draw(values, "uuid")

				want, err := u.ID().AppendBinary(slices.Clone(dst))
				assert.NoError(c, err, "AppendBinary of the ID must succeed")

				got, err := u.AppendBinary(dst)
				assert.NoError(c, err, "AppendBinary must succeed")
				assert.Equal(c, got, want, "AppendBinary must append the bytes of the ID of the UUID",
					assert.EquateEmpty())
			})
		})
	})

	t.Run("MarshalBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the bytes that AppendBinary appends", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "MarshalBinary must return the binary form", func(c *prop.Case) {
				u := c.Draw(values, "uuid")
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
			prop.RoundTrip(t, uuidv7.UUID.MarshalBinary, func(data []byte) (uuidv7.UUID, error) {
				var u uuidv7.UUID
				err := u.UnmarshalBinary(data)

				return u, err
			}, "UnmarshalBinary must invert MarshalBinary", prop.Using(values))
		})

		t.Run("sets the zero UUID for no bytes", func(t *testing.T) {
			t.Parallel()
			got := exampleUUID
			assert.NoError(t, got.UnmarshalBinary(nil), "UnmarshalBinary must accept no bytes")
			assert.Equal(t, got, uuidv7.UUID{}, "no bytes must decode to the zero UUID")
		})

		t.Run("returns ErrInvalidBytes for a length that no UUID encodes to", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				var u uuidv7.UUID

				return u.UnmarshalBinary(make([]byte, n))
			}, uuidv7.ErrInvalidBytes, "UnmarshalBinary must refuse a length that no UUID encodes to",
				prop.Using(prop.Integer(1, 40).Filter(func(n int) bool { return n != id.Size128 })),
				prop.Examples(1, id.Size128-1, id.Size128+1))
		})

		t.Run("returns ErrInvalidBytes for 16 zero bytes", func(t *testing.T) {
			t.Parallel()
			var u uuidv7.UUID
			err := u.UnmarshalBinary(make([]byte, id.Size128))
			expect.ErrorIs(t, err, uuidv7.ErrInvalidBytes, "UnmarshalBinary must refuse the bytes of the Nil UUID")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrInvalidBytes must classify as Invalid")
		})

		t.Run("leaves u unchanged for data that it refuses", func(t *testing.T) {
			t.Parallel()
			u := exampleUUID
			prop.Pure(t, func() uuidv7.UUID { return u }, func(data []byte) {
				_ = u.UnmarshalBinary(data)
			}, "a refused decode must leave the UUID unchanged", prop.Using(refusedData))
		})
	})

	t.Run("SizeKanon", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the length of the bytes that AppendKanon appends", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, uuidv7.UUID.SizeKanon, func(u uuidv7.UUID) int { return len(u.AppendKanon(nil)) },
				"SizeKanon must size the binary form", prop.Using(values))
		})
	})

	t.Run("AppendKanon", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the bytes that AppendBinary appends", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendKanon must append the binary form of the UUID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(8)), "dst")
				u := c.Draw(values, "uuid")

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
	kanontest.RunExact[uuidv7.UUID](t)
}

// TestValueAllocs checks the allocation contracts of FromID and the methods
// of UUID. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestValueAllocs(t *testing.T) {
	text := []byte(exampleText)
	data := exampleID.Bytes()

	t.Run("FromID", func(t *testing.T) {
		var (
			got uuidv7.UUID
			ok  bool
		)
		expect.MaxAllocs(t, func() { got, ok = uuidv7.FromID(exampleID) }, 0, "FromID must not allocate")
		assert.True(t, ok, "the test must measure a conversion that succeeds")
		assert.Equal(t, got, exampleUUID, "the test must measure the UUID of the ID")
	})

	t.Run("ID", func(t *testing.T) {
		var got id.ID
		expect.MaxAllocs(t, func() { got = exampleUUID.ID() }, 0, "ID must not allocate")
		assert.Equal(t, got, exampleID, "the test must measure the ID of the UUID")
	})

	t.Run("IsZero", func(t *testing.T) {
		got := true
		expect.MaxAllocs(t, func() { got = exampleUUID.IsZero() }, 0, "IsZero must not allocate")
		assert.False(t, got, "the test must measure a UUID other than zero")
	})

	t.Run("Valid", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = exampleUUID.Valid() }, 0, "Valid must not allocate")
		assert.True(t, got, "the test must measure a UUIDv7")
	})

	t.Run("Compare", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = exampleUUID.Compare(uuidv7.UUID{}) }, 0, "Compare must not allocate")
		assert.Equal(t, got, 1, "the test must measure the order of two UUIDs")
	})

	t.Run("String", func(t *testing.T) {
		var got string
		expect.MaxAllocs(t, func() { got = exampleUUID.String() }, 1, "String must allocate only the returned string")
		assert.Equal(t, got, exampleText, "the test must measure the text of the UUID")
	})

	t.Run("AppendText", func(t *testing.T) {
		buf := make([]byte, 0, len(exampleText))

		var err error
		expect.MaxAllocs(t, func() { buf, err = exampleUUID.AppendText(buf[:0]) }, 0,
			"AppendText must not allocate when dst has room for the text")
		assert.NoError(t, err, "AppendText must succeed")
		assert.Equal(t, string(buf), exampleText, "the test must measure the text of the UUID")
	})

	t.Run("MarshalText", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got, _ = exampleUUID.MarshalText() }, 1,
			"MarshalText must allocate only the returned slice")
		assert.Equal(t, string(got), exampleText, "the test must measure the text of the UUID")
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		var (
			got uuidv7.UUID
			err error
		)
		expect.MaxAllocs(t, func() { err = got.UnmarshalText(text) }, 0, "UnmarshalText must not allocate")
		assert.NoError(t, err, "the test must measure a decode that succeeds")
		assert.Equal(t, got, exampleUUID, "the test must measure the decode of the text")
	})

	t.Run("AppendBinary", func(t *testing.T) {
		buf := make([]byte, 0, id.Size128)

		var err error
		expect.MaxAllocs(t, func() { buf, err = exampleUUID.AppendBinary(buf[:0]) }, 0,
			"AppendBinary must not allocate when dst has room for the bytes")
		assert.NoError(t, err, "AppendBinary must succeed")
		assert.Equal(t, buf, data, "the test must measure the binary form of the UUID")
	})

	t.Run("MarshalBinary", func(t *testing.T) {
		t.Run("allocates only the slice that it returns", func(t *testing.T) {
			var got []byte
			expect.MaxAllocs(t, func() { got, _ = exampleUUID.MarshalBinary() }, 1,
				"MarshalBinary must allocate only the returned slice")
			assert.Equal(t, got, data, "the test must measure the binary form of the UUID")
		})

		t.Run("allocates nothing for the zero UUID", func(t *testing.T) {
			got := []byte{0}
			expect.MaxAllocs(t, func() { got, _ = uuidv7.UUID{}.MarshalBinary() }, 0,
				"MarshalBinary must not allocate for the zero UUID")
			assert.Empty(t, got, "the test must measure the binary form of the zero UUID")
		})
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		var (
			got uuidv7.UUID
			err error
		)
		expect.MaxAllocs(t, func() { err = got.UnmarshalBinary(data) }, 0, "UnmarshalBinary must not allocate")
		assert.NoError(t, err, "the test must measure a decode that succeeds")
		assert.Equal(t, got, exampleUUID, "the test must measure the decode of the bytes")
	})

	t.Run("SizeKanon", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = exampleUUID.SizeKanon() }, 0, "SizeKanon must not allocate")
		assert.Equal(t, got, id.Size128, "the test must measure the size of the UUID")
	})

	t.Run("AppendKanon", func(t *testing.T) {
		buf := make([]byte, 0, id.Size128)
		expect.MaxAllocs(t, func() { buf = exampleUUID.AppendKanon(buf[:0]) }, 0,
			"AppendKanon must not allocate when dst has room for the bytes")
		assert.Equal(t, buf, data, "the test must measure the binary form of the UUID")
	})
}

// BenchmarkValue reports the cost of FromID and the methods of UUID. It
// fails when one of them allocates more than TestValueAllocs allows.
func BenchmarkValue(b *testing.B) {
	text := []byte(exampleText)
	data := exampleID.Bytes()

	b.Run("FromID", func(b *testing.B) {
		var got uuidv7.UUID

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = uuidv7.FromID(exampleID)
		}

		assert.Equal(b, got, exampleUUID, "the benchmark must measure the UUID of the ID")
	})

	b.Run("ID", func(b *testing.B) {
		var got id.ID

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = exampleUUID.ID()
		}

		assert.Equal(b, got, exampleID, "the benchmark must measure the ID of the UUID")
	})

	b.Run("IsZero", func(b *testing.B) {
		got := true

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = exampleUUID.IsZero()
		}

		assert.False(b, got, "the benchmark must measure a UUID other than zero")
	})

	b.Run("Valid", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = exampleUUID.Valid()
		}

		assert.True(b, got, "the benchmark must measure a UUIDv7")
	})

	b.Run("Compare", func(b *testing.B) {
		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = exampleUUID.Compare(uuidv7.UUID{})
		}

		assert.Equal(b, got, 1, "the benchmark must measure the order of two UUIDs")
	})

	b.Run("String", func(b *testing.B) {
		var got string

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got = exampleUUID.String()
		}

		assert.Equal(b, got, exampleText, "the benchmark must measure the text of the UUID")
	})

	b.Run("AppendText", func(b *testing.B) {
		buf := make([]byte, 0, len(exampleText))
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			buf, err = exampleUUID.AppendText(buf[:0])
		}

		assert.NoError(b, err, "AppendText must succeed")
		assert.Equal(b, string(buf), exampleText, "the benchmark must measure the text of the UUID")
	})

	b.Run("MarshalText", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = exampleUUID.MarshalText()
		}

		assert.Equal(b, string(got), exampleText, "the benchmark must measure the text of the UUID")
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		var (
			got uuidv7.UUID
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = got.UnmarshalText(text)
		}

		assert.NoError(b, err, "the benchmark must measure a decode that succeeds")
		assert.Equal(b, got, exampleUUID, "the benchmark must measure the decode of the text")
	})

	b.Run("AppendBinary", func(b *testing.B) {
		buf := make([]byte, 0, id.Size128)
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			buf, err = exampleUUID.AppendBinary(buf[:0])
		}

		assert.NoError(b, err, "AppendBinary must succeed")
		assert.Equal(b, buf, data, "the benchmark must measure the binary form of the UUID")
	})

	b.Run("MarshalBinary", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = exampleUUID.MarshalBinary()
		}

		assert.Equal(b, got, data, "the benchmark must measure the binary form of the UUID")
	})

	b.Run("UnmarshalBinary", func(b *testing.B) {
		var (
			got uuidv7.UUID
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = got.UnmarshalBinary(data)
		}

		assert.NoError(b, err, "the benchmark must measure a decode that succeeds")
		assert.Equal(b, got, exampleUUID, "the benchmark must measure the decode of the bytes")
	})

	b.Run("SizeKanon", func(b *testing.B) {
		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = exampleUUID.SizeKanon()
		}

		assert.Equal(b, got, id.Size128, "the benchmark must measure the size of the UUID")
	})

	b.Run("AppendKanon", func(b *testing.B) {
		buf := make([]byte, 0, id.Size128)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			buf = exampleUUID.AppendKanon(buf[:0])
		}

		assert.Equal(b, buf, data, "the benchmark must measure the binary form of the UUID")
	})
}
