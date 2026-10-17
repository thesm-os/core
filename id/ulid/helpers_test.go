// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ulid_test

import (
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/ulid"
)

// specText is the example ULID of the ULID specification, and specBytes
// its bytes, whose timestamp is 1469922850259 milliseconds, as
// github.com/oklog/ulid/v2 decodes them.
var (
	specText  = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	specBytes = [id.Size128]byte{
		0x01, 0x56, 0x3e, 0x3a, 0xb5, 0xd3, 0xd6, 0x76, 0x4c, 0x61, 0xef, 0xb9, 0x93, 0x02, 0xbd, 0x5b,
	}
)

// ulids generates IDs of 128 bits.
var ulids = prop.Bytes(prop.MinSize(id.Size128), prop.MaxSize(id.Size128)).Map(func(b []byte) id.ID {
	return id.New128([id.Size128]byte(b))
})

// refusedTexts generates texts that ParseULID refuses: up to 52 arbitrary
// bytes, the text of a ULID with the letter U in place of one character, and
// the text of a ULID whose first character is above 7.
var refusedTexts = prop.OneOf(
	prop.Bytes(prop.MaxSize(52)),
	prop.Composite(func(c *prop.Case) []byte {
		text := []byte(ulid.Format(c.Draw(ulids, "id")))
		text[c.Draw(prop.Integer(0, len(text)-1), "position")] = 'U'

		return text
	}),
	prop.Composite(func(c *prop.Case) []byte {
		text := []byte(ulid.Format(c.Draw(ulids, "id")))
		text[0] = c.Draw(prop.StringMatching(`[89A-HJKMNP-TV-Za-hjkmnp-tv-z]`), "first")[0]

		return text
	}),
).Filter(func(text []byte) bool {
	_, err := ulid.ParseULID(string(text))

	return err != nil
})
