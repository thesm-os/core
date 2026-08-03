// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package version

//go:generate testkit sentinel -o errors.gen_test.go

import "errors"

// Sentinel errors reporting a failed [WriteOptions] precondition.
//
// A conditional write whose precondition failed is a universal
// outcome, and this package already defines the preconditions that
// produce it. Without these, every store invents its own spelling —
// which is the divergence [Version] exists to prevent, reintroduced
// one layer down.
var (
	// ErrMismatch reports that a [WriteOptions.IfMatch] precondition
	// failed: the stored version was not the one supplied.
	//
	// ErrMismatch is the retry signal of the optimistic-concurrency
	// loop documented on [Versioned]. The correct response is to
	// re-read, re-apply, and retry — not to repeat the identical
	// write, which fails identically. Classifies as Conflict under
	// [go.thesmos.sh/core/errs.Classify].
	ErrMismatch = errors.New("version: if-match precondition failed")

	// ErrExists reports that a [WriteOptions.IfNoneMatch]
	// precondition failed: a value already exists and the caller
	// asked to create only. Classifies as Conflict under
	// [go.thesmos.sh/core/errs.Classify].
	ErrExists = errors.New("version: already exists")
)
