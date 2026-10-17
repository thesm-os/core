// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package uuidtext_test

import (
	"errors"

	"go.thesmos.sh/core/id/internal/uuidtext"
)

// The errors that the tests pass to Parse and DecodeBinary, one for each
// field of uuidtext.Errors.
var (
	errLength = errors.New("the text is not 36 bytes")
	errFormat = errors.New("a hyphen is missing")
	errChar   = errors.New("a group has a byte other than a hex digit")
	errBytes  = errors.New("the data is not the binary form of a UUID value")

	// sentinels are the errors that the tests pass to Parse and DecodeBinary.
	sentinels = uuidtext.Errors{Length: errLength, Format: errFormat, Char: errChar, Bytes: errBytes}
)
