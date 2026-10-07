// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ulid_test

import (
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/ulid"
)

// The contracts of parsesText and roundTrips, which the tests and the
// fuzz targets check.
const (
	parseContract     = "Parse of the encoding of the ID that ParseULID returns must return the same ID"
	roundTripContract = "ParseULID must return the ID whose encoding Format returns"
)

// The canonical form of a ULID: a first character of '0' to '7', and 25
// characters of the Crockford alphabet.
const canonical = `[0-7][0-9A-HJKMNP-TV-Z]{25}`

// specText is the example ULID of the ULID specification, and specBytes
// its bytes, whose timestamp is 1469922850259 milliseconds, as
// github.com/oklog/ulid/v2 decodes them.
var (
	specText  = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	specBytes = [id.Size128]byte{
		0x01, 0x56, 0x3e, 0x3a, 0xb5, 0xd3, 0xd6, 0x76, 0x4c, 0x61, 0xef, 0xb9, 0x93, 0x02, 0xbd, 0x5b,
	}
)

// asymmetric is an ID whose 5-bit groups map to distinct characters, and
// asymmetricText its encoding, as github.com/oklog/ulid/v2 encodes it.
var (
	asymmetric = id.New128([id.Size128]byte{
		0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF, 0xFE, 0xDC,
	})
	asymmetricText = "014D2PF2DB04HMASW9NF6YZZPW"
)

// ulids generates IDs of 128 bits.
var ulids = prop.Bytes(prop.MinSize(id.Size128), prop.MaxSize(id.Size128)).Map(func(b []byte) id.ID {
	return id.New128([id.Size128]byte(b))
})

func TestParse(t *testing.T) {
	t.Parallel()

	t.Run("Format", func(t *testing.T) {
		t.Parallel()

		// The encodings of github.com/oklog/ulid/v2 for the same bytes.
		tests := []struct {
			name string
			give id.ID
			want string
		}{
			{name: "returns the empty string for Zero", give: id.Zero, want: ""},
			{
				name: "returns 26 zeros for an ID of zero bytes",
				give: id.New128([id.Size128]byte{}),
				want: strings.Repeat("0", 26),
			},
			{
				name: "returns a leading 01 for a first timestamp byte of 0x01",
				give: id.New128([id.Size128]byte{0x01}),
				want: "01" + strings.Repeat("0", 24),
			},
			{
				name: "returns 16 Zs for a random half of all ones",
				give: id.New128([id.Size128]byte{
					0, 0, 0, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
				}),
				want: strings.Repeat("0", 10) + strings.Repeat("Z", 16),
			},
			{
				name: "returns the reference encoding of an asymmetric random half",
				give: id.New128([id.Size128]byte{
					0, 0, 0, 0, 0, 0, 0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0, 0x12, 0x34,
				}),
				want: "000000000028T5CY4TQKFF04HM",
			},
			{name: "returns the reference encoding of an asymmetric ID", give: asymmetric, want: asymmetricText},
			{
				name: "returns the largest ULID for an ID of all ones",
				give: id.New128([id.Size128]byte{
					0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
				}),
				want: "7" + strings.Repeat("Z", 25),
			},
			{
				name: "returns the example of the specification for its bytes",
				give: id.New128(specBytes),
				want: specText,
			},
			{
				name: "returns the encoding of the first 128 bits of a 256-bit ID",
				give: id.New256([id.Size256]byte(append(specBytes[:], make([]byte, 16)...))),
				want: specText,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, ulid.Format(tt.give), tt.want, "Format must return the Crockford base32 encoding")
			})
		}

		t.Run("returns a canonical ULID for every 128-bit ID", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Format must return the canonical form of a ULID", func(c *prop.Case) {
				s := ulid.Format(c.Draw(ulids, "id"))
				assert.Matches(c, s, "^"+canonical+"$", "Format must return a canonical ULID")
			})
		})
	})

	t.Run("ParseULID", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the ID that Format encodes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, roundTripContract, roundTrips)
		})

		t.Run("returns the ID of the example of the specification", func(t *testing.T) {
			t.Parallel()
			got, err := ulid.ParseULID(specText)
			assert.NoError(t, err, "ParseULID must accept the example")
			expect.Equal(t, got, id.New128(specBytes), "ParseULID must decode the bytes of the example")
			expect.Equal(t, ulid.TimestampMillis(got), uint64(1469922850259),
				"ParseULID must decode the timestamp of the example")
		})

		vectors := []struct {
			name string
			give string
			want id.ID
		}{
			{
				name: "returns the ID of zero bytes for 26 zeros",
				give: strings.Repeat("0", 26),
				want: id.New128([id.Size128]byte{}),
			},
			{
				name: "returns the ID of the reference encoding of an asymmetric ID",
				give: asymmetricText,
				want: asymmetric,
			},
			{
				name: "returns the reference ID of the uppercase characters 0 to S",
				give: "0123456789ABCDEFGHJKMNPQRS",
				want: id.New128([id.Size128]byte{
					0x01, 0x10, 0xc8, 0x53, 0x1d, 0x09, 0x52, 0xd8, 0xd7, 0x3e, 0x11, 0x94, 0xe9, 0x5b, 0x5f, 0x19,
				}),
			},
			{
				name: "returns the reference ID of the lowercase characters",
				give: "0abcdefghjkmnpqrstvwxyz000",
				want: id.New128([id.Size128]byte{
					0x0a, 0x5b, 0x1a, 0xe7, 0xc2, 0x32, 0x9d, 0x2b, 0x6b, 0xe3, 0x3a, 0xdf, 0x3b, 0xef, 0x80, 0x00,
				}),
			},
			{
				name: "returns the ID of all ones for the largest ULID",
				give: "7" + strings.Repeat("Z", 25),
				want: id.New128([id.Size128]byte{
					0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
				}),
			},
		}
		for _, tt := range vectors {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := ulid.ParseULID(tt.give)
				assert.NoError(t, err, "ParseULID must accept the encoding")
				assert.Equal(t, got, tt.want, "ParseULID must decode the reference bytes")
			})
		}

		t.Run("returns the ID of the uppercase form for a lowercase form", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "ParseULID must ignore the case of a letter", func(c *prop.Case) {
				want := c.Draw(ulids, "id")

				got, err := ulid.ParseULID(strings.ToLower(ulid.Format(want)))
				assert.NoError(c, err, "ParseULID must accept the lowercase form")
				assert.Equal(c, got, want, "the lowercase form must decode to the ID")
			})
		})

		substitutions := []struct {
			name string
			give string
			want string
		}{
			{name: "returns the ID of 1 for an uppercase I", give: "I", want: "1"},
			{name: "returns the ID of 1 for a lowercase i", give: "i", want: "1"},
			{name: "returns the ID of 1 for an uppercase L", give: "L", want: "1"},
			{name: "returns the ID of 1 for a lowercase l", give: "l", want: "1"},
			{name: "returns the ID of 0 for an uppercase O", give: "O", want: "0"},
			{name: "returns the ID of 0 for a lowercase o", give: "o", want: "0"},
		}
		for _, tt := range substitutions {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				prefix := "0" + strings.Repeat("7", 5)
				suffix := strings.Repeat("Z", 19)
				got, err := ulid.ParseULID(prefix + tt.give + suffix)
				assert.NoError(t, err, "ParseULID must accept the substitution")
				want, err := ulid.ParseULID(prefix + tt.want + suffix)
				assert.NoError(t, err, "ParseULID must accept the digit")
				assert.Equal(t, got, want, "the substitution must decode as its digit")
			})
		}

		t.Run("returns ErrInvalidLength for a text that is not 26 characters", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := ulid.ParseULID(strings.Repeat("0", n))

				return err
			}, ulid.ErrInvalidLength, "a text of another length must be refused",
				prop.Using(prop.Integer(0, 100).Filter(func(n int) bool { return n != 26 })),
				prop.Example(0), prop.Example(1), prop.Example(25), prop.Example(27), prop.Example(100))
		})

		t.Run("returns ErrInvalidChar for a character outside the Crockford alphabet", func(t *testing.T) {
			t.Parallel()
			outside := prop.Integer(0, 255).Filter(func(b int) bool {
				return !strings.ContainsRune("0123456789ABCDEFGHIJKLMNOPQRSTVWXYZabcdefghijklmnopqrstvwxyz", rune(b))
			})
			prop.ForAll(t, "ParseULID must refuse a character outside the alphabet", func(c *prop.Case) {
				at := c.Draw(prop.Integer(0, 25), "position")
				b := c.Draw(outside, "character")

				text := []byte(strings.Repeat("0", 26))
				text[at] = byte(b)
				_, err := ulid.ParseULID(string(text))
				assert.ErrorIs(c, err, ulid.ErrInvalidChar, "ParseULID must return ErrInvalidChar")
			})
		})

		t.Run("returns ErrInvalidChar for the U that the Crockford alphabet excludes", func(t *testing.T) {
			t.Parallel()
			_, err := ulid.ParseULID("U" + strings.Repeat("0", 25))
			assert.ErrorIs(t, err, ulid.ErrInvalidChar, "ParseULID must refuse a U")
		})

		t.Run("returns ErrInvalidTimestamp for a first character above 7", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(first string) error {
				_, err := ulid.ParseULID(first + strings.Repeat("0", 25))

				return err
			}, ulid.ErrInvalidTimestamp, "a first character above 7 must overflow the timestamp",
				prop.Using(prop.StringMatching(`[89A-HJKMNP-TV-Za-hjkmnp-tv-z]`)), prop.Example("8"), prop.Example("Z"))
		})

		classes := []struct {
			name string
			give string
		}{
			{name: "returns an error of class Invalid for a text that is not 26 characters", give: "0"},
			{
				name: "returns an error of class Invalid for a character outside the alphabet",
				give: "U" + strings.Repeat("0", 25),
			},
			{
				name: "returns an error of class Invalid for a first character above 7",
				give: "8" + strings.Repeat("0", 25),
			},
		}
		for _, tt := range classes {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := ulid.ParseULID(tt.give)
				assert.Equal(t, errs.Classify(err), errs.Invalid, "the refusal must classify as Invalid")
			})
		}

		t.Run("returns an ID that Format encodes again for every text that it accepts", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, parseContract, parsesText)
		})
	})
}

// TestParseAllocs checks the allocation contracts of Format and ParseULID.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestParseAllocs(t *testing.T) {
	t.Run("Format", func(t *testing.T) {
		var s string
		expect.MaxAllocs(t, func() { s = ulid.Format(asymmetric) }, 1, "Format must allocate only the returned string")
		assert.Equal(t, s, asymmetricText, "the test must measure the encoding of the ULID")
	})

	t.Run("ParseULID", func(t *testing.T) {
		var (
			got id.ID
			err error
		)
		expect.MaxAllocs(t, func() { got, err = ulid.ParseULID(asymmetricText) }, 0, "ParseULID must not allocate")
		assert.NoError(t, err, "the test must measure an encoding that ParseULID accepts")
		assert.Equal(t, got, asymmetric, "the test must measure the ID of the encoding")
	})
}

// BenchmarkParse reports the cost of Format and ParseULID, and fails when
// one allocates more than TestParseAllocs allows.
func BenchmarkParse(b *testing.B) {
	b.Run("Format", func(b *testing.B) {
		var s string

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			s = ulid.Format(asymmetric)
		}

		assert.Equal(b, s, asymmetricText, "the benchmark must measure the encoding of the ULID")
	})

	b.Run("ParseULID", func(b *testing.B) {
		var (
			got id.ID
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, err = ulid.ParseULID(asymmetricText)
		}

		assert.NoError(b, err, "the benchmark must measure an encoding that ParseULID accepts")
		assert.Equal(b, got, asymmetric, "the benchmark must measure the ID of the encoding")
	})
}

// FuzzParseULID checks the contract of parsesText on the texts that a
// fuzzer finds. Each seed is a text after the octet of its length that the
// bridge reads first.
func FuzzParseULID(f *testing.F) {
	for _, s := range []string{
		specText, strings.Repeat("0", 26), "7" + strings.Repeat("Z", 25), "0iIlLoO" + strings.Repeat("0", 19),
	} {
		f.Add(append([]byte{byte(len(s))}, s...))
	}

	prop.Fuzz(f, parseContract, parsesText)
}

// FuzzULIDRoundTrip checks the contract of roundTrips on the IDs that a
// fuzzer finds.
func FuzzULIDRoundTrip(f *testing.F) {
	f.Add(make([]byte, id.Size128))
	f.Add(specBytes[:])
	f.Add([]byte(strings.Repeat("\xff", id.Size128)))

	prop.Fuzz(f, roundTripContract, roundTrips)
}

// parsesText checks that ParseULID refuses a drawn text, or returns an ID
// that it returns again from the encoding of the ID. ParseULID ignores
// case and accepts substitutions, so the text itself is not recovered.
func parsesText(c *prop.Case) {
	s := string(c.Draw(prop.Bytes(prop.MaxSize(52)), "text"))

	got, err := ulid.ParseULID(s)
	if err == nil {
		assert.RoundTrip(c, func(i id.ID) (string, error) { return ulid.Format(i), nil }, ulid.ParseULID, got,
			"the encoding must decode to the same ID")
	}
}

// roundTrips checks that ParseULID returns a drawn 128-bit ID from the
// encoding that Format returns for it.
func roundTrips(c *prop.Case) {
	want := c.Draw(ulids, "id")
	assert.RoundTrip(c, func(i id.ID) (string, error) { return ulid.Format(i), nil }, ulid.ParseULID, want,
		"ParseULID must return the ID that Format encoded")
}
