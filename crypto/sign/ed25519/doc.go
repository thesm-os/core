// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package ed25519 provides an Ed25519 [sign.Signer] / [sign.Verifier]
// implementation backed by [crypto/ed25519].
//
// Signing follows RFC 8032 §5.1.6 (PureEdDSA): the raw message
// bytes are signed directly, not pre-hashed. This package does
// NOT implement Ed25519ph (RFC 8032 §5.1) — external verifiers
// expecting PreHash will not accept signatures produced here.
//
// Ed25519 PureEdDSA cannot stream: the signing equation requires
// the message in two separate SHA-512 computations (the second
// depending on the first), so a streaming API would force
// internal buffering. This package does not implement
// [sign.StreamingSigner] / [sign.StreamingVerifier]; consumers
// signing arbitrary algorithms type-assert and fall back to the
// whole-message [sign.Signer.Sign] / [sign.Verifier.Verify]
// path for Ed25519 inputs.
//
// Under `GODEBUG=fips140=on` (or `=only`), every operation runs
// through Go's FIPS-validated module. Ed25519 is approved under
// FIPS 186-5 (2023), so both modes work without modification.
// FIPS-gated wrappers (hard-refusal-outside-FIPS-mode) live in
// consumer modules, not here.
package ed25519
