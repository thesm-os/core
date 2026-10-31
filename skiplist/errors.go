// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package skiplist

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// The functions of this package return these errors.
var (
	// ErrFull is returned by [List.Add] for an entry whose node or bytes do
	// not fit the arenas of the [List]. It classifies as [errs.Transient]:
	// an engine freezes the full List and adds the entry to a new one.
	ErrFull = errs.WithClass(errors.New("skiplist: the arenas are full"), errs.Transient)

	// ErrExists is returned by List.Add for an entry with the key and the
	// sequence of an entry of the List. It classifies as [errs.Invalid].
	ErrExists = errs.WithClass(errors.New("skiplist: the entry exists"), errs.Invalid)

	// ErrSize is returned by [New] for a capacity of an arena below 1 or
	// above 2^32 - 1 bytes, for memory of an arena that is shorter than the
	// arena, and for a negative width of the versions. It classifies as
	// errs.Invalid.
	ErrSize = errs.WithClass(errors.New("skiplist: a size of the config is out of range"), errs.Invalid)
)
