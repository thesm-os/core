// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package epoch

//go:generate testkit sentinel -o errors.gen_test.go

import "errors"

// Sentinel errors returned by the fence checks and the binary encoding.
// They are plain sentinels, because [go.thesmos.sh/core/errs] imports
// this package, and [go.thesmos.sh/core/errs.Classify] recognises each
// of them on an error's chain.
var (
	// ErrFenced reports that a write's fence epoch is behind the
	// scope's watermark: another holder has been granted a later
	// epoch and this caller's authority is revoked. Classifies as
	// Conflict under [go.thesmos.sh/core/errs.Classify].
	//
	// The remedy is NOT to retry the write. It is to abdicate: stop
	// writing, release state derived from the revoked tenure, and
	// re-acquire authority through whatever election issued the
	// epoch. Only then is retry sound. The sentinel is distinct from
	// [go.thesmos.sh/core/version.ErrMismatch]. Both classify as
	// Conflict, and errors.Is separates "re-read and retry" from
	// "abdicate and re-elect".
	ErrFenced = errors.New("epoch: fence epoch superseded")

	// ErrSize is returned by [Epoch.UnmarshalBinary] when the input
	// is not exactly [EpochSize] bytes. A truncated read returns this
	// error instead of a panic or a partial value. Classifies as
	// Invalid under [go.thesmos.sh/core/errs.Classify].
	ErrSize = errors.New("epoch: encoded epoch must be 8 bytes")
)
