// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package version

//go:generate testkit sentinel -o errors.gen_test.go

import "errors"

// Sentinel errors reporting the outcome of a conditional write that
// did not succeed: a failed [WriteOptions] precondition, or an outcome
// the store could not report.
//
// These outcomes are universal, and this package already defines the
// preconditions that produce them. Without these sentinels, every store
// invents its own spelling, which is the divergence [Version] exists to
// prevent, reintroduced one layer down.
var (
	// ErrMismatch reports that a [WriteOptions.IfMatch] precondition
	// failed: the stored version was not the one supplied.
	//
	// ErrMismatch is the retry signal of the optimistic-concurrency
	// loop documented on [Versioned]. The correct response is to
	// re-read, re-apply and retry, because the identical write fails
	// identically. Classifies as Conflict under
	// [go.thesmos.sh/core/errs.Classify].
	ErrMismatch = errors.New("version: if-match precondition failed")

	// ErrExists reports that a [WriteOptions.IfNoneMatch]
	// precondition failed: a value already exists and the caller
	// asked to create only. Classifies as Conflict under
	// [go.thesmos.sh/core/errs.Classify].
	ErrExists = errors.New("version: already exists")

	// ErrOutcomeUnknown reports a write that may or may not have taken
	// effect. The store lost its connection, or the caller's deadline
	// passed, after the write left the process.
	//
	// A retry of the identical write, with the same idempotency key,
	// returns the original outcome when the store recorded one, so the
	// store that returns ErrOutcomeUnknown must make such a retry safe.
	// Classifies as Transient under [go.thesmos.sh/core/errs.Classify].
	ErrOutcomeUnknown = errors.New("version: write outcome unknown")
)
