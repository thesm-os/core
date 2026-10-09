// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tsptest

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors of misuse. Both classify as
// [go.thesmos.sh/core/errs.Invalid], because the same call fails again.
var (
	// ErrConfig reports a Config without a Clock or a Policy, or with a
	// Key, a Digest, an ESS or a Usage that is not Valid, and a Spec
	// without a TSTInfo. Classifies as Invalid.
	ErrConfig = errs.WithClass(errors.New("tsptest: invalid configuration"), errs.Invalid)

	// ErrRequest reports a request that is not the DER of a TimeStampReq:
	// a SEQUENCE of a version, a messageImprint, and the optional
	// reqPolicy, nonce, certReq and extensions in their order. Classifies
	// as Invalid.
	ErrRequest = errs.WithClass(errors.New("tsptest: malformed time-stamp request"), errs.Invalid)
)
