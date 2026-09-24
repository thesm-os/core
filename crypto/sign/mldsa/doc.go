// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package mldsa provides [sign.Signer] and [sign.Verifier] for ML-DSA,
// the module-lattice signature scheme of FIPS 204, backed by
// [crypto/mldsa].
//
// [Params] selects the parameter set: [MLDSA44], [MLDSA65] or
// [MLDSA87]. The sets differ in key and signature size and in NIST
// security category. Each reports its own [crypto.Algorithm], so a
// verifier routes a stored signature by name.
//
// # Context strings
//
// FIPS 204 separates signatures made for different purposes with a
// context string of up to 255 bytes. A [Signer] or [Verifier] fixes
// its context when it is built, because [sign.Signer.Sign] takes only
// the message. One seed can serve one signer per purpose. A signature
// made under one context does not verify under another.
//
// # Private keys
//
// A private key is its 32-byte FIPS 204 seed, the format RFC 9881
// recommends for storing and transmitting ML-DSA keys. [New] takes the
// seed and [Signer.Seed] returns it.
//
// # Signing
//
// [Signer.Sign] uses the hedged variant, which mixes fresh randomness
// into every signature. FIPS 204 specifies it as the default.
// Signatures differ on every call, so tests verify them instead of
// comparing bytes.
//
// # Allocation contract
//
// [Verifier.Verify], [Verifier.KeyID], [Verifier.PublicKey] and
// [Verifier.Algorithm] are zero-allocation. [Signer.Sign] allocates the
// returned signature once, because the standard library offers no
// buffer-passing signing function.
//
// # Dependency position
//
// Imports crypto/mldsa, crypto/sha256, errors and fmt from the standard
// library, and go.thesmos.sh/core/crypto, go.thesmos.sh/core/crypto/sign,
// go.thesmos.sh/core/errs and go.thesmos.sh/core/rand from this module.
package mldsa
