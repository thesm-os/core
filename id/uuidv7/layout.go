// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv7

import (
	"encoding/binary"

	"go.thesmos.sh/core/id"
)

// The version and variant fields of a UUIDv7.
const (
	// version is the version of a UUIDv7, in the high 4 bits of byte 6.
	version = 7

	// variant is the variant of RFC 9562, in the high 2 bits of byte 8.
	variant = 0b10

	// versionWord is version in the big-endian word of bytes 0-7.
	versionWord = 0x7000

	// variantWord is variant in the big-endian word of bytes 8-15.
	variantWord = 0x8000000000000000
)

// TimestampMillis returns the unix_ts_ms field of u, its Unix
// milliseconds, or 0 when u is not 128 bits. It does not check the version
// of u. [Valid] does.
//
// # Allocation contract
//
// Zero alloc.
func TimestampMillis(u id.ID) uint64 {
	if u.Size() != id.Size128 {
		return 0
	}

	return binary.BigEndian.Uint64(u.Bytes()[0:8]) >> 16
}

// Valid reports whether u is a UUIDv7: 128 bits, with version 7 and the
// variant of RFC 9562.
//
// # Allocation contract
//
// Zero alloc.
func Valid(u id.ID) bool {
	if u.Size() != id.Size128 {
		return false
	}

	b := u.Bytes()

	return b[6]>>4 == version && b[8]>>6 == variant
}

// build returns the UUIDv7 with the Unix milliseconds ms, the 12-bit
// rand_a a and, as rand_b, the high 62 bits of b.
func build(ms uint64, a uint16, b uint64) id.ID {
	var u [id.Size128]byte
	binary.BigEndian.PutUint64(u[0:8], ms<<16|versionWord|uint64(a))
	binary.BigEndian.PutUint64(u[8:16], variantWord|b>>2)

	return id.New128(u)
}
