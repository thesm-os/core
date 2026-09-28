// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ed25519

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by the package's constructors. Each
// classifies as [go.thesmos.sh/core/errs.Invalid]: the key the caller
// passed is wrong, so the same call never succeeds.
var (
	// ErrInvalidPublicKeySize is returned when a verifier
	// constructor receives a public-key byte slice whose length
	// is not [crypto/ed25519.PublicKeySize] (32 bytes).
	ErrInvalidPublicKeySize = errs.WithClass(errors.New("ed25519: public key must be 32 bytes"), errs.Invalid)

	// ErrInvalidPrivateKeySize is returned when a signer
	// constructor receives a private-key byte slice whose length
	// is not [crypto/ed25519.PrivateKeySize] (64 bytes — Go's
	// expanded representation including the public-key suffix).
	ErrInvalidPrivateKeySize = errs.WithClass(errors.New(
		"ed25519: private key must be 64 bytes (Go expanded representation)",
	), errs.Invalid)
)
