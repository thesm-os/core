// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clock

//go:generate testkit sentinel -o errors.gen_test.go

import "errors"

// Sentinel errors returned by [Instant.UnmarshalBinary].
var (
	// ErrInstantSize is returned when an encoded [Instant] is not
	// exactly [InstantSize] bytes long.
	ErrInstantSize = errors.New("clock: instant encoding must be 16 bytes")
)
