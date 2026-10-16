// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package radix

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// The methods of a Map return these errors.
var (
	// ErrFull is returned by [Map.Set] for a new key when the map contains
	// its limit of keys. A Set of the key succeeds once a Delete makes room,
	// so ErrFull classifies as [errs.Transient].
	ErrFull = errs.WithClass(errors.New("radix: map is full"), errs.Transient)

	// ErrTooLong is returned by Map.Set for a key of 2^32 bytes or more. A
	// leaf stores the length of its key in 32 bits. ErrTooLong classifies as
	// [errs.Invalid].
	ErrTooLong = errs.WithClass(errors.New("radix: key is too long"), errs.Invalid)
)
