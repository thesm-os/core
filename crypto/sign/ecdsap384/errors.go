// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ecdsap384

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by the package's constructors. Each
// classifies as [go.thesmos.sh/core/errs.Invalid]: the key the caller
// passed is wrong, so the same call never succeeds.
var (
	// ErrNilKey is returned when a constructor receives a nil
	// private or public key.
	ErrNilKey = errs.WithClass(errors.New("ecdsap384: nil key"), errs.Invalid)

	// ErrWrongCurve is returned when a constructor receives a
	// key whose curve is not [crypto/elliptic.P384].
	ErrWrongCurve = errs.WithClass(errors.New("ecdsap384: key curve is not P-384"), errs.Invalid)

	// ErrInvalidPublicKey is returned when public-key bytes
	// cannot be parsed as a PKIX-encoded ECDSA P-384 public
	// key.
	ErrInvalidPublicKey = errs.WithClass(errors.New(
		"ecdsap384: public-key bytes are not PKIX-encoded ECDSA P-384",
	), errs.Invalid)

	// ErrOffCurve is returned when an [crypto/ecdsa.PublicKey]'s
	// (X, Y) coordinates do not lie on the P-384 curve. Surfaces
	// from [KeyIDFromPub] and propagates through [NewVerifier]
	// when the supplied key is structurally typed as P-384 but
	// fails curve-membership.
	ErrOffCurve = errs.WithClass(errors.New("ecdsap384: public-key point is not on the P-384 curve"), errs.Invalid)
)
