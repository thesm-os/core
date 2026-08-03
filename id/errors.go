// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package id

//go:generate testkit sentinel -o errors.gen_test.go

import "errors"

// Sentinel errors returned by [FromBytes].
var (
	// ErrSize is returned when the input length is not one of
	// [Size128], [Size160], or [Size256]. A truncated or
	// over-long input is a decode error, never a panic and never
	// a silently-resized identifier.
	ErrSize = errors.New("id: invalid length, want 16, 20, or 32 bytes")
)
