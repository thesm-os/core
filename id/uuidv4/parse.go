// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4

import (
	"encoding/hex"

	"go.thesmos.sh/core/id"
)

// Format returns the canonical hyphenated hex encoding of u
// ("xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx"). Diagnostic and
// serialization use; allocates the result string.
//
// Format does NOT validate that u is a well-formed UUIDv4 — it
// formats whatever 16 bytes it is given. Consumers that need
// validation check the version and variant bits themselves.
//
// Returns the empty string if u is shorter than [id.Size128]
// (for example [id.Zero]).
//
// # Allocation contract
//
// Allocates the 36-byte result string.
func Format(u id.ID) string {
	b := u.Bytes()
	if len(b) < id.Size128 {
		return ""
	}
	var buf [36]byte
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])
	return string(buf[:])
}

// Parse parses the canonical 36-character hyphenated hex
// encoding of a UUIDv4 into an [id.ID] of size [id.Size128].
//
// Parse does NOT validate that the resulting bytes form a
// well-formed UUIDv4 (correct version/variant bits) — it accepts
// any 16-byte hex payload that fits the layout. Consumers that
// require version/variant validation inspect the returned [id.ID]
// bytes directly.
//
// # Allocation contract
//
// Allocates the returned [id.ID] only.
func Parse(s string) (id.ID, error) {
	if len(s) != 36 {
		return id.Zero, ErrInvalidLength
	}
	if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return id.Zero, ErrInvalidFormat
	}
	var raw [id.Size128]byte
	if _, err := hex.Decode(raw[0:4], []byte(s[0:8])); err != nil {
		return id.Zero, ErrInvalidChar
	}
	if _, err := hex.Decode(raw[4:6], []byte(s[9:13])); err != nil {
		return id.Zero, ErrInvalidChar
	}
	if _, err := hex.Decode(raw[6:8], []byte(s[14:18])); err != nil {
		return id.Zero, ErrInvalidChar
	}
	if _, err := hex.Decode(raw[8:10], []byte(s[19:23])); err != nil {
		return id.Zero, ErrInvalidChar
	}
	if _, err := hex.Decode(raw[10:16], []byte(s[24:36])); err != nil {
		return id.Zero, ErrInvalidChar
	}
	return id.New128(raw), nil
}
