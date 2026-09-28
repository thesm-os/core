// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package errs

//go:generate testkit sentinel -o errors.gen_test.go

import "errors"

// Sentinel errors returned by this package.
var (
	// ErrUnknownClass is returned by [Class.AppendText] and
	// [Class.MarshalText] for a value outside the eight classes, and by
	// [Class.UnmarshalText] for text that names none of them. It
	// classifies as [Invalid].
	ErrUnknownClass = WithClass(errors.New("errs: unknown class"), Invalid)
)
