// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package snappy

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// The functions of this package return these errors. [ErrTooLarge]
// classifies as [errs.Invalid]: the same input fails the same way on every
// call. [ErrCorrupt] classifies as [errs.Integrity]: a block failed the checks
// of the format.
var (
	// ErrTooLarge reports a source of more than [MaxBlockLen] bytes, and a
	// length above the largest int of the platform.
	ErrTooLarge = errs.WithClass(errors.New("snappy: block too large"), errs.Invalid)

	// ErrCorrupt reports a block that the format does not allow: a preamble
	// that is not a varint of at most MaxBlockLen, a truncated element, a
	// copy with an offset of 0 or before the first byte, an element past the
	// length that the preamble states, or fewer bytes than it states.
	ErrCorrupt = errs.WithClass(errors.New("snappy: corrupt block"), errs.Integrity)
)
