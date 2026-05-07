// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ed25519

//go:generate testkit sentinel -o errors.gen_test.go

import "errors"

// Sentinel errors returned by the package's constructors.
var (
	// ErrInvalidPublicKeySize is returned when a verifier
	// constructor receives a public-key byte slice whose length
	// is not [crypto/ed25519.PublicKeySize] (32 bytes).
	ErrInvalidPublicKeySize = errors.New("ed25519: public key must be 32 bytes")

	// ErrInvalidPrivateKeySize is returned when a signer
	// constructor receives a private-key byte slice whose length
	// is not [crypto/ed25519.PrivateKeySize] (64 bytes — Go's
	// expanded representation including the public-key suffix).
	ErrInvalidPrivateKeySize = errors.New(
		"ed25519: private key must be 64 bytes (Go expanded representation)",
	)
)
