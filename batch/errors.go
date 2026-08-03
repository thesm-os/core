// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package batch

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by this package.
var (
	// ErrConfig is returned by [NewLoader] when a required field is
	// missing or out of range. A window left at zero would coalesce
	// nothing, which looks exactly like a loader that is working.
	ErrConfig = errors.New("batch: invalid configuration")

	// ErrClosed reports a [Loader.Load] or [Loader.LoadAll] on a
	// Loader whose [Loader.Close] has returned.
	//
	// Classifies as Invalid under
	// [go.thesmos.sh/core/errs.Classify]: the caller's wiring is
	// wrong, and retrying cannot help.
	ErrClosed = errs.WithClass(errors.New("batch: loader closed"), errs.Invalid)

	// ErrNotFound reports a key the batch function did not resolve.
	//
	// Classifies as NotFound. Absence is the common case a batch
	// loader has to report correctly, and returning it as an error
	// means the ordinary error check at the call site handles it —
	// where a per-key (V, bool) would let a caller ignore it silently.
	ErrNotFound = errs.WithClass(errors.New("batch: key not in result"), errs.NotFound)
)
