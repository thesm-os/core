// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ksuid_test

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.thesmos.sh/kanon"
	"go.thesmos.sh/kanon/kanontest"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/ksuid"
)

// The interfaces that KSUID satisfies. A missing method is a build failure.
var (
	_ fmt.Stringer               = ksuid.KSUID{}
	_ encoding.TextAppender      = ksuid.KSUID{}
	_ encoding.TextMarshaler     = ksuid.KSUID{}
	_ encoding.TextUnmarshaler   = (*ksuid.KSUID)(nil)
	_ encoding.BinaryAppender    = ksuid.KSUID{}
	_ encoding.BinaryMarshaler   = ksuid.KSUID{}
	_ encoding.BinaryUnmarshaler = (*ksuid.KSUID)(nil)
	_ kanon.Appender             = ksuid.KSUID{}
)

// zeroText is the text of the zero KSUID: 27 zeros.
const zeroText = "000000000000000000000000000"

// referenceKSUID is the KSUID of referenceBytes.
var referenceKSUID = ksuid.KSUID(referenceBytes)

// values generates KSUIDs of any 20 bytes, the zero KSUID included.
var values = prop.Bytes(prop.MinSize(id.Size160), prop.MaxSize(id.Size160)).Map(func(b []byte) ksuid.KSUID {
	return ksuid.KSUID(b)
})

// nonZeroValues generates the KSUIDs other than the zero KSUID.
var nonZeroValues = values.Filter(func(k ksuid.KSUID) bool { return k != ksuid.KSUID{} })

// ksuidTexts generates the text of a KSUID.
var ksuidTexts = ksuids.Map(func(i id.ID) []byte { return []byte(ksuid.Format(i)) })

// refusedData generates data that is not the binary form of a KSUID: up to
// 40 bytes of another length than 20, and 20 zero bytes.
var refusedData = prop.OneOf(
	prop.Bytes(prop.MinSize(1), prop.MaxSize(40)).Filter(func(b []byte) bool { return len(b) != id.Size160 }),
	prop.Just(make([]byte, id.Size160)),
)

// ksuidDocument is a JSON document with a field of KSUID. The option
// omitzero leaves out the field when it is the zero KSUID.
type ksuidDocument struct {
	KSUID ksuid.KSUID `json:"ksuid,omitzero"`
}

func TestValue(t *testing.T) {
	t.Parallel()

	t.Run("FromID", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the KSUID with the bytes of a 160-bit ID", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "FromID must copy the bytes of a 160-bit ID", func(c *prop.Case) {
				i := c.Draw(ksuids, "id")

				got, ok := ksuid.FromID(i)
				assert.True(c, ok, "FromID must convert a 160-bit ID")
				assert.Equal(c, got[:], i.Bytes(), "the KSUID must have the bytes of the ID")
			})
		})

		t.Run("returns the KSUID whose ID it converts", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "FromID must invert ID", func(c *prop.Case) {
				k := c.Draw(values, "ksuid")

				got, ok := ksuid.FromID(k.ID())
				assert.True(c, ok, "FromID must convert the ID of a KSUID")
				assert.Equal(c, got, k, "FromID must return the KSUID of the ID")
			})
		})

		t.Run("returns the zero KSUID for Zero", func(t *testing.T) {
			t.Parallel()
			got, ok := ksuid.FromID(id.Zero)
			assert.True(t, ok, "FromID must convert Zero")
			assert.Equal(t, got, ksuid.KSUID{}, "Zero must convert to the zero KSUID")
		})

		tests := []struct {
			name string
			give id.ID
		}{
			{name: "reports false for a 128-bit ID", give: id.New128([id.Size128]byte{1})},
			{name: "reports false for a 256-bit ID", give: id.New256([id.Size256]byte{1})},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := ksuid.FromID(tt.give)
				expect.False(t, ok, "FromID must refuse an ID of another size")
				expect.Equal(t, got, ksuid.KSUID{}, "FromID must return the zero KSUID for an ID that it refuses")
			})
		}
	})

	t.Run("ID", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the 160-bit ID of the bytes of a KSUID other than zero", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, ksuid.KSUID.ID, func(k ksuid.KSUID) id.ID { return id.New160(k) },
				"ID must return the ID of the bytes of the KSUID", prop.Using(nonZeroValues))
		})

		t.Run("returns Zero for the zero KSUID", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, ksuid.KSUID{}.ID(), id.Zero, "the zero KSUID must convert to Zero")
		})
	})

	t.Run("IsZero", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for the zero KSUID", func(t *testing.T) {
			t.Parallel()
			assert.True(t, ksuid.KSUID{}.IsZero(), "the zero KSUID must be zero")
		})

		t.Run("reports false for a KSUID other than zero", func(t *testing.T) {
			t.Parallel()
			prop.False(t, ksuid.KSUID.IsZero, "a KSUID with a byte other than zero is not zero",
				prop.Using(nonZeroValues))
		})
	})

	t.Run("Compare", func(t *testing.T) {
		t.Parallel()

		t.Run("returns what bytes.Compare returns for the bytes of the KSUIDs", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Compare must order KSUIDs as their bytes", func(c *prop.Case) {
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
			prop.Equal(t, ksuid.KSUID.String, func(k ksuid.KSUID) string { return ksuid.Format(id.New160(k)) },
				"String must return the text of the bytes", prop.Using(values))
		})

		t.Run("returns 27 zeros for the zero KSUID", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, ksuid.KSUID{}.String(), zeroText, "the zero KSUID must have the text of 27 zeros")
		})
	})

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the text that String returns to dst", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendText must keep dst and append the text of the KSUID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(8)), "dst")
				k := c.Draw(values, "ksuid")
				want := append(slices.Clone(dst), k.String()...)

				got, err := k.AppendText(dst)
				assert.NoError(c, err, "AppendText must succeed")
				assert.Equal(c, got, want, "AppendText must extend dst with the text of the KSUID")
			})
		})
	})

	t.Run("MarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the text that String returns", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "MarshalText must return the text of String", func(c *prop.Case) {
				k := c.Draw(values, "ksuid")

				got, err := k.MarshalText()
				assert.NoError(c, err, "MarshalText must succeed")
				assert.Equal(c, string(got), k.String(), "MarshalText must return the text of the KSUID")
			})
		})

		t.Run("makes json.Marshal write a field as a JSON string of the text", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "json.Marshal must write a KSUID as the JSON string of its text", func(c *prop.Case) {
				k := c.Draw(nonZeroValues, "ksuid")

				got, err := json.Marshal(ksuidDocument{KSUID: k})
				assert.NoError(c, err, "json.Marshal must encode a KSUID")
				assert.Equal(c, string(got), `{"ksuid":"`+ksuid.Format(id.New160(k))+`"}`,
					"the field must be the JSON string of the text of the KSUID")
			})
		})

		t.Run("makes json.Marshal write a map key as the text", func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(map[ksuid.KSUID]int{referenceKSUID: 1})
			assert.NoError(t, err, "json.Marshal must encode a map keyed by KSUIDs")
			assert.Equal(t, string(got), `{"`+referenceText+`":1}`, "the member name must be the text of the key")
		})

		t.Run("makes json.Marshal leave out the zero KSUID of a field with omitzero", func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(ksuidDocument{})
			assert.NoError(t, err, "json.Marshal must encode the zero KSUID")
			assert.Equal(t, string(got), `{}`, "omitzero must leave out the zero KSUID")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("decodes the text that MarshalText returns", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, ksuid.KSUID.MarshalText, func(text []byte) (ksuid.KSUID, error) {
				var k ksuid.KSUID
				err := k.UnmarshalText(text)

				return k, err
			}, "UnmarshalText must invert MarshalText", prop.Using(values))
		})

		t.Run("sets k to the bytes that Parse decodes from a text that Parse accepts", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalText must accept every text that Parse accepts", func(c *prop.Case) {
				text := c.Draw(ksuidTexts, "text")
				want, err := ksuid.Parse(string(text))
				assert.NoError(c, err, "the property must draw a text that Parse accepts")

				var got ksuid.KSUID
				assert.NoError(c, got.UnmarshalText(text), "UnmarshalText must accept a text that Parse accepts")
				assert.Equal(c, got[:], want.Bytes(), "UnmarshalText must decode the bytes that Parse decodes")
			})
		})

		t.Run("returns the error that Parse returns for a text that Parse refuses", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalText must return the sentinel of Parse", func(c *prop.Case) {
				text := c.Draw(refusedTexts, "text")
				_, want := ksuid.Parse(string(text))

				var k ksuid.KSUID
				assert.Equal(c, k.UnmarshalText(text), want, "UnmarshalText must return the error of Parse")
			})
		})

		t.Run("leaves k unchanged for a text that it refuses", func(t *testing.T) {
			t.Parallel()
			k := referenceKSUID
			prop.Pure(t, func() ksuid.KSUID { return k }, func(text []byte) {
				_ = k.UnmarshalText(text)
			}, "a refused decode must leave the KSUID unchanged", prop.Using(refusedTexts))
		})

		t.Run("sets the zero KSUID for 27 zeros", func(t *testing.T) {
			t.Parallel()
			got := referenceKSUID
			assert.NoError(t, got.UnmarshalText([]byte(zeroText)), "UnmarshalText must accept 27 zeros")
			assert.Equal(t, got, ksuid.KSUID{}, "27 zeros must decode to the zero KSUID")
		})

		t.Run("returns ErrInvalidLength for the empty text", func(t *testing.T) {
			t.Parallel()
			var k ksuid.KSUID
			assert.ErrorIs(t, k.UnmarshalText(nil), ksuid.ErrInvalidLength, "UnmarshalText must refuse the empty text")
		})

		t.Run("decodes a JSON field that json.Marshal writes", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(k ksuid.KSUID) ([]byte, error) {
				return json.Marshal(ksuidDocument{KSUID: k})
			}, func(document []byte) (ksuid.KSUID, error) {
				var v ksuidDocument
				err := json.Unmarshal(document, &v)

				return v.KSUID, err
			}, "json.Unmarshal must undo json.Marshal of a field", prop.Using(values))
		})

		t.Run("decodes the JSON object that json.Marshal writes for a map", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(m map[ksuid.KSUID]int) ([]byte, error) {
				return json.Marshal(m)
			}, func(document []byte) (map[ksuid.KSUID]int, error) {
				var m map[ksuid.KSUID]int
				err := json.Unmarshal(document, &m)

				return m, err
			}, "json.Unmarshal must undo json.Marshal of a map keyed by KSUIDs",
				prop.Using(prop.Dict(values, prop.Integer(0, 9), prop.MaxSize(4))))
		})

		t.Run("returns ErrInvalidLength through json.Unmarshal", func(t *testing.T) {
			t.Parallel()
			var got ksuidDocument
			err := json.Unmarshal([]byte(`{"ksuid":"0ujtsYcgvSTl"}`), &got)
			assert.ErrorIs(t, err, ksuid.ErrInvalidLength, "json.Unmarshal must return the error of UnmarshalText")
		})
	})

	t.Run("AppendBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the 20 bytes of a KSUID other than zero to dst", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendBinary must keep dst and append the bytes of the KSUID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(8)), "dst")
				k := c.Draw(nonZeroValues, "ksuid")
				want := append(slices.Clone(dst), k[:]...)

				got, err := k.AppendBinary(dst)
				assert.NoError(c, err, "AppendBinary must succeed")
				assert.Equal(c, got, want, "AppendBinary must extend dst with the bytes of the KSUID")
			})
		})

		t.Run("returns dst unchanged for the zero KSUID", func(t *testing.T) {
			t.Parallel()
			got, err := ksuid.KSUID{}.AppendBinary([]byte(referenceText))
			assert.NoError(t, err, "AppendBinary must succeed")
			assert.Equal(t, string(got), referenceText, "the binary form of the zero KSUID must be no bytes")
		})

		t.Run("appends what AppendBinary of the ID of the KSUID appends", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendBinary must append the binary form of the ID of the KSUID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(8)), "dst")
				k := c.Draw(values, "ksuid")

				want, err := k.ID().AppendBinary(slices.Clone(dst))
				assert.NoError(c, err, "AppendBinary of the ID must succeed")

				got, err := k.AppendBinary(dst)
				assert.NoError(c, err, "AppendBinary must succeed")
				assert.Equal(c, got, want, "AppendBinary must append the bytes of the ID of the KSUID",
					assert.EquateEmpty())
			})
		})
	})

	t.Run("MarshalBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the bytes that AppendBinary appends", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "MarshalBinary must return the binary form", func(c *prop.Case) {
				k := c.Draw(values, "ksuid")
				want, err := k.AppendBinary(nil)
				assert.NoError(c, err, "AppendBinary must succeed")

				got, err := k.MarshalBinary()
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
			prop.RoundTrip(t, ksuid.KSUID.MarshalBinary, func(data []byte) (ksuid.KSUID, error) {
				var k ksuid.KSUID
				err := k.UnmarshalBinary(data)

				return k, err
			}, "UnmarshalBinary must invert MarshalBinary", prop.Using(values))
		})

		t.Run("sets the zero KSUID for no bytes", func(t *testing.T) {
			t.Parallel()
			got := referenceKSUID
			assert.NoError(t, got.UnmarshalBinary(nil), "UnmarshalBinary must accept no bytes")
			assert.Equal(t, got, ksuid.KSUID{}, "no bytes must decode to the zero KSUID")
		})

		t.Run("returns ErrInvalidBytes for a length that no KSUID encodes to", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				var k ksuid.KSUID

				return k.UnmarshalBinary(make([]byte, n))
			}, ksuid.ErrInvalidBytes, "UnmarshalBinary must refuse a length that no KSUID encodes to",
				prop.Using(prop.Integer(1, 40).Filter(func(n int) bool { return n != id.Size160 })),
				prop.Examples(1, id.Size160-1, id.Size160+1))
		})

		t.Run("returns ErrInvalidBytes for 20 zero bytes", func(t *testing.T) {
			t.Parallel()
			var k ksuid.KSUID
			err := k.UnmarshalBinary(make([]byte, id.Size160))
			expect.ErrorIs(t, err, ksuid.ErrInvalidBytes, "UnmarshalBinary must refuse 20 zero bytes")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrInvalidBytes must classify as Invalid")
		})

		t.Run("leaves k unchanged for data that it refuses", func(t *testing.T) {
			t.Parallel()
			k := referenceKSUID
			prop.Pure(t, func() ksuid.KSUID { return k }, func(data []byte) {
				_ = k.UnmarshalBinary(data)
			}, "a refused decode must leave the KSUID unchanged", prop.Using(refusedData))
		})

		t.Run("returns a KSUID that a later write to data leaves unchanged", func(t *testing.T) {
			t.Parallel()
			data := slices.Clone(referenceBytes[:])
			var got ksuid.KSUID
			assert.NoError(t, got.UnmarshalBinary(data), "UnmarshalBinary must accept the bytes of a KSUID")
			data[0] = 0xff
			assert.Equal(t, got, referenceKSUID, "a write to data must not change the KSUID")
		})
	})

	t.Run("SizeKanon", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the length of the bytes that AppendKanon appends", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, ksuid.KSUID.SizeKanon, func(k ksuid.KSUID) int { return len(k.AppendKanon(nil)) },
				"SizeKanon must size the binary form", prop.Using(values))
		})
	})

	t.Run("AppendKanon", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the bytes that AppendBinary appends", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendKanon must append the binary form of the KSUID", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(8)), "dst")
				k := c.Draw(values, "ksuid")

				want, err := k.AppendBinary(slices.Clone(dst))
				assert.NoError(c, err, "AppendBinary must succeed")
				assert.Equal(c, k.AppendKanon(dst), want, "AppendKanon must equal AppendBinary", assert.EquateEmpty())
			})
		})
	})
}

// TestExactKanon checks the two guarantees that ExactKanon declares, and the
// bytes of AppendKanon, with kanon's conformance suite.
func TestExactKanon(t *testing.T) {
	t.Parallel()
	kanontest.RunExact[ksuid.KSUID](t)
}

// TestValueAllocs checks the allocation contracts of FromID and the methods
// of KSUID. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestValueAllocs(t *testing.T) {
	text := []byte(referenceText)
	data := referenceBytes[:]
	referenceID := id.New160(referenceBytes)

	t.Run("FromID", func(t *testing.T) {
		var (
			got ksuid.KSUID
			ok  bool
		)
		expect.MaxAllocs(t, func() { got, ok = ksuid.FromID(referenceID) }, 0, "FromID must not allocate")
		assert.True(t, ok, "the test must measure a conversion that succeeds")
		assert.Equal(t, got, referenceKSUID, "the test must measure the KSUID of the ID")
	})

	t.Run("ID", func(t *testing.T) {
		var got id.ID
		expect.MaxAllocs(t, func() { got = referenceKSUID.ID() }, 0, "ID must not allocate")
		assert.Equal(t, got, referenceID, "the test must measure the ID of the KSUID")
	})

	t.Run("IsZero", func(t *testing.T) {
		got := true
		expect.MaxAllocs(t, func() { got = referenceKSUID.IsZero() }, 0, "IsZero must not allocate")
		assert.False(t, got, "the test must measure a KSUID other than zero")
	})

	t.Run("Compare", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = referenceKSUID.Compare(ksuid.KSUID{}) }, 0, "Compare must not allocate")
		assert.Equal(t, got, 1, "the test must measure the order of two KSUIDs")
	})

	t.Run("String", func(t *testing.T) {
		var got string
		expect.MaxAllocs(t, func() { got = referenceKSUID.String() }, 1,
			"String must allocate only the returned string")
		assert.Equal(t, got, referenceText, "the test must measure the text of the KSUID")
	})

	t.Run("AppendText", func(t *testing.T) {
		buf := make([]byte, 0, len(referenceText))

		var err error
		expect.MaxAllocs(t, func() { buf, err = referenceKSUID.AppendText(buf[:0]) }, 0,
			"AppendText must not allocate when dst has room for the text")
		assert.NoError(t, err, "AppendText must succeed")
		assert.Equal(t, string(buf), referenceText, "the test must measure the text of the KSUID")
	})

	t.Run("MarshalText", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got, _ = referenceKSUID.MarshalText() }, 1,
			"MarshalText must allocate only the returned slice")
		assert.Equal(t, string(got), referenceText, "the test must measure the text of the KSUID")
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		var (
			got ksuid.KSUID
			err error
		)
		expect.MaxAllocs(t, func() { err = got.UnmarshalText(text) }, 0, "UnmarshalText must not allocate")
		assert.NoError(t, err, "the test must measure a decode that succeeds")
		assert.Equal(t, got, referenceKSUID, "the test must measure the decode of the text")
	})

	t.Run("AppendBinary", func(t *testing.T) {
		buf := make([]byte, 0, id.Size160)

		var err error
		expect.MaxAllocs(t, func() { buf, err = referenceKSUID.AppendBinary(buf[:0]) }, 0,
			"AppendBinary must not allocate when dst has room for the bytes")
		assert.NoError(t, err, "AppendBinary must succeed")
		assert.Equal(t, buf, data, "the test must measure the binary form of the KSUID")
	})

	t.Run("MarshalBinary", func(t *testing.T) {
		t.Run("allocates only the slice that it returns", func(t *testing.T) {
			var got []byte
			expect.MaxAllocs(t, func() { got, _ = referenceKSUID.MarshalBinary() }, 1,
				"MarshalBinary must allocate only the returned slice")
			assert.Equal(t, got, data, "the test must measure the binary form of the KSUID")
		})

		t.Run("allocates nothing for the zero KSUID", func(t *testing.T) {
			got := []byte{0}
			expect.MaxAllocs(t, func() { got, _ = ksuid.KSUID{}.MarshalBinary() }, 0,
				"MarshalBinary must not allocate for the zero KSUID")
			assert.Empty(t, got, "the test must measure the binary form of the zero KSUID")
		})
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		var (
			got ksuid.KSUID
			err error
		)
		expect.MaxAllocs(t, func() { err = got.UnmarshalBinary(data) }, 0, "UnmarshalBinary must not allocate")
		assert.NoError(t, err, "the test must measure a decode that succeeds")
		assert.Equal(t, got, referenceKSUID, "the test must measure the decode of the bytes")
	})

	t.Run("SizeKanon", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = referenceKSUID.SizeKanon() }, 0, "SizeKanon must not allocate")
		assert.Equal(t, got, id.Size160, "the test must measure the size of the KSUID")
	})

	t.Run("AppendKanon", func(t *testing.T) {
		buf := make([]byte, 0, id.Size160)
		expect.MaxAllocs(t, func() { buf = referenceKSUID.AppendKanon(buf[:0]) }, 0,
			"AppendKanon must not allocate when dst has room for the bytes")
		assert.Equal(t, buf, data, "the test must measure the binary form of the KSUID")
	})
}

// BenchmarkValue reports the cost of FromID and the methods of KSUID. It
// fails when one of them allocates more than TestValueAllocs allows.
func BenchmarkValue(b *testing.B) {
	text := []byte(referenceText)
	data := referenceBytes[:]
	referenceID := id.New160(referenceBytes)

	b.Run("FromID", func(b *testing.B) {
		var got ksuid.KSUID

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = ksuid.FromID(referenceID)
		}

		assert.Equal(b, got, referenceKSUID, "the benchmark must measure the KSUID of the ID")
	})

	b.Run("ID", func(b *testing.B) {
		var got id.ID

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = referenceKSUID.ID()
		}

		assert.Equal(b, got, referenceID, "the benchmark must measure the ID of the KSUID")
	})

	b.Run("IsZero", func(b *testing.B) {
		got := true

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = referenceKSUID.IsZero()
		}

		assert.False(b, got, "the benchmark must measure a KSUID other than zero")
	})

	b.Run("Compare", func(b *testing.B) {
		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = referenceKSUID.Compare(ksuid.KSUID{})
		}

		assert.Equal(b, got, 1, "the benchmark must measure the order of two KSUIDs")
	})

	b.Run("String", func(b *testing.B) {
		var got string

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got = referenceKSUID.String()
		}

		assert.Equal(b, got, referenceText, "the benchmark must measure the text of the KSUID")
	})

	b.Run("AppendText", func(b *testing.B) {
		buf := make([]byte, 0, len(referenceText))
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			buf, err = referenceKSUID.AppendText(buf[:0])
		}

		assert.NoError(b, err, "AppendText must succeed")
		assert.Equal(b, string(buf), referenceText, "the benchmark must measure the text of the KSUID")
	})

	b.Run("MarshalText", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = referenceKSUID.MarshalText()
		}

		assert.Equal(b, string(got), referenceText, "the benchmark must measure the text of the KSUID")
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		var (
			got ksuid.KSUID
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = got.UnmarshalText(text)
		}

		assert.NoError(b, err, "the benchmark must measure a decode that succeeds")
		assert.Equal(b, got, referenceKSUID, "the benchmark must measure the decode of the text")
	})

	b.Run("AppendBinary", func(b *testing.B) {
		buf := make([]byte, 0, id.Size160)
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			buf, err = referenceKSUID.AppendBinary(buf[:0])
		}

		assert.NoError(b, err, "AppendBinary must succeed")
		assert.Equal(b, buf, data, "the benchmark must measure the binary form of the KSUID")
	})

	b.Run("MarshalBinary", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = referenceKSUID.MarshalBinary()
		}

		assert.Equal(b, got, data, "the benchmark must measure the binary form of the KSUID")
	})

	b.Run("UnmarshalBinary", func(b *testing.B) {
		var (
			got ksuid.KSUID
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = got.UnmarshalBinary(data)
		}

		assert.NoError(b, err, "the benchmark must measure a decode that succeeds")
		assert.Equal(b, got, referenceKSUID, "the benchmark must measure the decode of the bytes")
	})

	b.Run("SizeKanon", func(b *testing.B) {
		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = referenceKSUID.SizeKanon()
		}

		assert.Equal(b, got, id.Size160, "the benchmark must measure the size of the KSUID")
	})

	b.Run("AppendKanon", func(b *testing.B) {
		buf := make([]byte, 0, id.Size160)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			buf = referenceKSUID.AppendKanon(buf[:0])
		}

		assert.Equal(b, buf, data, "the benchmark must measure the binary form of the KSUID")
	})
}
