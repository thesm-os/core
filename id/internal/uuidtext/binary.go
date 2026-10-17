// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package uuidtext

import "go.thesmos.sh/core/id"

// AppendBinary appends the binary form of the UUID value b to dst and
// returns the extended slice. The binary form is the 16 bytes of b, and no
// bytes for the zero value, as for [id.Zero].
//
// # Allocation contract
//
// Zero alloc when dst has room for 16 more bytes.
func AppendBinary(dst []byte, b [id.Size128]byte) []byte {
	if b == ([id.Size128]byte{}) {
		return dst
	}

	return append(dst, b[:]...)
}

// DecodeBinary returns the bytes of the UUID value whose binary form is
// data: the zero value for no bytes, and the 16 bytes of data when they are
// not all zero. The result does not alias data.
//
// It returns e.Bytes for data whose length is not 0 or 16. It also returns
// e.Bytes for 16 zero bytes, because the binary form of the zero value is no
// bytes.
//
// # Allocation contract
//
// Zero alloc.
func DecodeBinary(data []byte, e Errors) ([id.Size128]byte, error) {
	if len(data) == 0 {
		return [id.Size128]byte{}, nil
	}

	if len(data) != id.Size128 {
		return [id.Size128]byte{}, e.Bytes
	}

	b := [id.Size128]byte(data)
	if b == ([id.Size128]byte{}) {
		return [id.Size128]byte{}, e.Bytes
	}

	return b, nil
}
