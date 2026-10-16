// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package arena

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// The methods of [Slabs] and [RefSlabs] return these errors. Both classify
// as [errs.Invalid]: the same call fails the same way on every attempt.
var (
	// ErrSizeClass reports a slice that [Slabs.Free] cannot take back,
	// because its capacity is not a size class.
	ErrSizeClass = errs.WithClass(errors.New("arena: the capacity of the slice is not a size class"), errs.Invalid)

	// ErrRef is returned by [RefSlabs.Free] for a [Ref] whose region does not
	// lie inside a slab of the [RefSlabs].
	ErrRef = errs.WithClass(errors.New("arena: the reference is outside the slabs"), errs.Invalid)
)
