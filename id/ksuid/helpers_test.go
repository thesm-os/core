// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ksuid_test

import (
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/ksuid"
)

// The reference KSUID of segmentio/ksuid: its bytes, a timestamp of
// 2017-10-09T21:46:47Z and 16 random bytes, and its encoding.
var (
	referenceBytes = [id.Size160]byte{
		0x06, 0x69, 0xf7, 0xef,
		0xb5, 0xa1, 0xcd, 0x34, 0xb5, 0xf9, 0x9d, 0x12,
		0x14, 0xe5, 0xb9, 0x16, 0x9d, 0xa6, 0x9c, 0x32,
	}
	referenceText = "0ujtsYcgvSTl8PAuR7PHXnl95SE"
)

// largestText is the encoding of the largest KSUID, whose 160 bits are
// all set. An encoding above it in the order of the alphabet overflows.
var largestText = ksuid.Format(id.New160([id.Size160]byte{
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
}))

// ksuids generates IDs of 160 bits.
var ksuids = prop.Bytes(prop.MinSize(id.Size160), prop.MaxSize(id.Size160)).Map(func(b []byte) id.ID {
	return id.New160([id.Size160]byte(b))
})

// refusedTexts generates texts that Parse refuses: up to 54 arbitrary bytes,
// the text of a KSUID with an exclamation mark in place of one character,
// and the texts of 27 characters above the largest KSUID.
var refusedTexts = prop.OneOf(
	prop.Bytes(prop.MaxSize(54)),
	prop.Composite(func(c *prop.Case) []byte {
		text := []byte(ksuid.Format(c.Draw(ksuids, "id")))
		text[c.Draw(prop.Integer(0, len(text)-1), "position")] = '!'

		return text
	}),
	prop.StringMatching(`[a-z][0-9A-Za-z]{26}`).Filter(func(s string) bool { return s > largestText }).Map(
		func(s string) []byte { return []byte(s) },
	),
).Filter(func(text []byte) bool {
	_, err := ksuid.Parse(string(text))

	return err != nil
})
