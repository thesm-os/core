// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

//go:generate testkit sentinel -o errors.gen_test.go

package mldsa

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

var (
	// ErrParams is returned for a [Params] value other than [MLDSA44],
	// [MLDSA65] and [MLDSA87]. It classifies as [errs.Invalid].
	ErrParams = errs.WithClass(errors.New("mldsa: unknown parameter set"), errs.Invalid)

	// ErrSeed is returned by [New] for a seed that is not 32 bytes. It
	// classifies as [errs.Invalid].
	ErrSeed = errs.WithClass(errors.New("mldsa: seed must be 32 bytes"), errs.Invalid)

	// ErrPublicKey is returned by [NewVerifier] for bytes that are not a
	// public key of the parameter set. It classifies as [errs.Invalid].
	ErrPublicKey = errs.WithClass(errors.New("mldsa: invalid public key encoding"), errs.Invalid)

	// ErrContext is returned for a context string longer than 255
	// bytes, the limit FIPS 204 sets. It classifies as [errs.Invalid].
	ErrContext = errs.WithClass(errors.New("mldsa: context longer than 255 bytes"), errs.Invalid)
)
