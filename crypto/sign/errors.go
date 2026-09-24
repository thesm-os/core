// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

//go:generate testkit sentinel -o errors.gen_test.go

package sign

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

var (
	// ErrUnknownAlgorithm is returned by [Resolver.Verifier] for an
	// algorithm the Resolver does not contain. It classifies as
	// [errs.Unsupported].
	ErrUnknownAlgorithm = errs.WithClass(errors.New("sign: unknown algorithm"), errs.Unsupported)

	// ErrPolicy is returned by [NewPolicy] for a policy that cannot be
	// satisfied or that lists a key twice, and by [Policy.Check] on the
	// zero Policy. It classifies as [errs.Invalid].
	ErrPolicy = errs.WithClass(errors.New("sign: invalid policy"), errs.Invalid)

	// ErrThreshold is returned by [Policy.Check] when the signatures
	// satisfy fewer parties than the threshold. It classifies as
	// [errs.Integrity].
	ErrThreshold = errs.WithClass(errors.New("sign: signature threshold not met"), errs.Integrity)
)
