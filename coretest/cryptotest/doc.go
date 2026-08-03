// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package cryptotest holds testkit-generated test infrastructure
// for the crypto package's interface seams ([go.thesmos.sh/core/crypto.Hasher],
// [go.thesmos.sh/core/crypto.MAC], [go.thesmos.sh/core/crypto.AEAD])
// and consumer-facing assertion bundles. Generated artefacts have a
// `.gen.go` suffix; hand-rolled assertion / fixture files do not.
package cryptotest

// Digest is opaque (unexported fields by design — size-
// discriminated via [crypto.NewDigest256] / [NewDigest384] /
// [NewDigest512]) so testkit builder is not applicable. Canonical
// algorithm-of-record fixture bytes (sign keypairs, sample
// messages, sample signatures) live in sign_fixtures.go; consumer
// tests wrap the stdlib bytes via their own constructors to avoid
// an import cycle with the impl packages under test.

// Hasher
//go:generate testkit stub -p go.thesmos.sh/core/crypto -o hasher_stub.gen.go Hasher
//go:generate testkit suite -p go.thesmos.sh/core/crypto -o hasher_spec.gen.go Hasher
//go:generate testkit bench -p go.thesmos.sh/core/crypto -o hasher_bench.gen.go Hasher
//go:generate testkit model -p go.thesmos.sh/core/crypto -o hasher_model.gen.go Hasher

// Stream
//go:generate testkit stub -p go.thesmos.sh/core/crypto -o stream_stub.gen.go Stream
//go:generate testkit suite -p go.thesmos.sh/core/crypto -o stream_spec.gen.go Stream

// MAC
//go:generate testkit stub -p go.thesmos.sh/core/crypto -o mac_stub.gen.go MAC
//go:generate testkit suite -p go.thesmos.sh/core/crypto -o mac_spec.gen.go MAC
//go:generate testkit bench -p go.thesmos.sh/core/crypto -o mac_bench.gen.go MAC
//go:generate testkit model -p go.thesmos.sh/core/crypto -o mac_model.gen.go MAC

// AEAD. No model layer: Seal draws a fresh nonce per call, so
// successive outputs are not comparable against a reference.
//
// No generated suite either. [go.thesmos.sh/core/crypto.AEAD] embeds
// [crypto/cipher.AEAD], whose Seal and Open are documented to panic
// on a nonce that is not NonceSize() bytes. The suite generator emits
// nil-safety subtests calling every method with all-nil arguments,
// which that contract cannot satisfy — and the methods belong to the
// standard library, so there is nowhere to put a directive
// suppressing them. AssertAEADContract is hand-written in
// aead_spec.go instead.
//go:generate testkit stub -p go.thesmos.sh/core/crypto -o aead_stub.gen.go AEAD
//go:generate testkit bench -p go.thesmos.sh/core/crypto -o aead_bench.gen.go AEAD

// Verifier
//go:generate testkit stub -p go.thesmos.sh/core/crypto/sign -o verifier_stub.gen.go Verifier
//go:generate testkit suite -p go.thesmos.sh/core/crypto/sign -o verifier_spec.gen.go Verifier
//go:generate testkit bench -p go.thesmos.sh/core/crypto/sign -o verifier_bench.gen.go Verifier

// Signer
//go:generate testkit stub -p go.thesmos.sh/core/crypto/sign -o signer_stub.gen.go Signer
//go:generate testkit suite -p go.thesmos.sh/core/crypto/sign -o signer_spec.gen.go Signer
//go:generate testkit bench -p go.thesmos.sh/core/crypto/sign -o signer_bench.gen.go Signer

// SignStream
//go:generate testkit stub -p go.thesmos.sh/core/crypto/sign -o signstream_stub.gen.go SignStream
//go:generate testkit suite -p go.thesmos.sh/core/crypto/sign -o signstream_spec.gen.go SignStream
//go:generate testkit bench -p go.thesmos.sh/core/crypto/sign -o signstream_bench.gen.go SignStream

// VerifyStream
//go:generate testkit stub -p go.thesmos.sh/core/crypto/sign -o verifystream_stub.gen.go VerifyStream
//go:generate testkit suite -p go.thesmos.sh/core/crypto/sign -o verifystream_spec.gen.go VerifyStream
//go:generate testkit bench -p go.thesmos.sh/core/crypto/sign -o verifystream_bench.gen.go VerifyStream
