// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package kek

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by this package.
var (
	// ErrClosed reports a [Keeper.Wrap] or [Keeper.Unwrap] on a Keeper
	// whose [Keeper.Close] has returned.
	//
	// Classifies as Invalid under [go.thesmos.sh/core/errs.Classify]: the
	// caller's wiring is wrong, and retrying cannot help.
	ErrClosed = errs.WithClass(errors.New("kek: keeper closed"), errs.Invalid)

	// ErrKeyIDMismatch is returned by [New] and [NewAAD] when the
	// unwrapped record is bound to another key ID: the stored record was
	// swapped, or the caller named the wrong key ID.
	ErrKeyIDMismatch = errors.New("kek: record bound to another key ID")
)
