// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv7_test

import (
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/id"
)

// Fixtures of the tests.
const (
	// exampleText is the text form of exampleID.
	exampleText = "01856d53-f1fb-774e-8048-d159e26af37b"

	// exampleMillis is the Unix milliseconds of exampleTime, and the
	// unix_ts_ms field of exampleID.
	exampleMillis = 0x01856d53f1fb
)

// exampleBytes are the bytes of the UUIDv7 that a Generator returns at
// exampleTime from a source whose Uint64 returns randomBits: the
// milliseconds 0x01856d53f1fb, version 7 and the fraction 0x74e, then
// variant 10 and the high 62 bits of randomBits.
var exampleBytes = [id.Size128]byte{
	0x01, 0x85, 0x6d, 0x53, 0xf1, 0xfb, 0x77, 0x4e,
	0x80, 0x48, 0xd1, 0x59, 0xe2, 0x6a, 0xf3, 0x7b,
}

// exampleID is the UUIDv7 whose bytes are exampleBytes.
var exampleID = id.New128(exampleBytes)

// notUUIDs are IDs that are not 128 bits.
var notUUIDs = []struct {
	name string
	id   id.ID
}{
	{name: "the zero ID", id: id.Zero},
	{name: "a 160-bit ID", id: id.New160([id.Size160]byte{1})},
	{name: "a 256-bit ID", id: id.New256([id.Size256]byte{1})},
}

// uuids generates IDs of 128 bits.
var uuids = prop.Bytes(prop.MinSize(id.Size128), prop.MaxSize(id.Size128)).Map(func(b []byte) id.ID {
	return id.New128([id.Size128]byte(b))
})
