// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package id

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by the decoders of [ID].
var (
	// ErrSize is returned when the input length is not one of
	// [Size128], [Size160], or [Size256]. [ID.UnmarshalText] returns
	// it when the digits after the prefix do not encode one of these
	// lengths. A truncated or over-long input returns this error
	// instead of a panic or a resized identifier. It classifies as
	// [go.thesmos.sh/core/errs.Invalid].
	ErrSize = errs.WithClass(errors.New("id: invalid length, want 16, 20, or 32 bytes"), errs.Invalid)

	// ErrText is returned by [ID.UnmarshalText] for a text that does not
	// start with "id:", and for a digit other than 0 to 9 and the
	// lowercase letters a to f. [ID.AppendText] writes only these bytes
	// after the prefix, so each ID has exactly one text. It classifies as
	// [go.thesmos.sh/core/errs.Invalid].
	ErrText = errs.WithClass(errors.New("id: invalid text, want id: and lowercase hexadecimal digits"), errs.Invalid)
)
