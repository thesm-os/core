// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

//go:generate testkit sentinel -o errors.gen_test.go

import "errors"

// Sentinel errors returned by [DigestFromBytes], [Digest.AppendBinary],
// [Digest.MarshalBinary], and [Digest.UnmarshalBinary].
var (
	// ErrDigestSize is returned when a digest-shaped byte slice is
	// not exactly [DigestSize256], [DigestSize384], or
	// [DigestSize512] bytes long. A truncated input is a decode
	// error, never a panic and never a silent truncation.
	ErrDigestSize = errors.New("crypto: digest length must be 32, 48, or 64 bytes")

	// ErrDigestZero is returned when the zero [Digest] is marshalled.
	// The zero Digest is an in-memory sentinel with no wire form:
	// encoding it as zero bytes would make every truncated read
	// decode back into a genesis anchor. Encoding the absence of a
	// digest is the containing format's job. See ADR-0007.
	ErrDigestZero = errors.New("crypto: the zero digest has no binary encoding")
)
