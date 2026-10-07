// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// [Parse] returns these errors. Each classifies as
// [go.thesmos.sh/core/errs.Invalid]: a text that is not the text form of a
// UUID never parses, so a retry of the same call fails the same way.
var (
	// ErrInvalidLength reports a text that is not 36 bytes long. The text
	// form has 32 hexadecimal digits and 4 hyphens.
	ErrInvalidLength = errs.WithClass(errors.New("uuidv4: invalid length, want 36 characters"), errs.Invalid)

	// ErrInvalidFormat reports a text of 36 bytes without a hyphen at byte
	// 8, 13, 18 or 23.
	ErrInvalidFormat = errs.WithClass(
		errors.New("uuidv4: invalid format, hyphens at wrong positions"), errs.Invalid,
	)

	// ErrInvalidChar reports a text with a byte other than a hexadecimal
	// digit in one of its groups.
	ErrInvalidChar = errs.WithClass(errors.New("uuidv4: invalid hex character"), errs.Invalid)
)
