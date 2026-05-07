// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package cryptotest holds testkit-generated test infrastructure
// for the crypto package's interface seams ([go.thesmos.sh/core/crypto.Hasher],
// [go.thesmos.sh/core/crypto.MAC]) and consumer-facing assertion
// bundles. Generated artefacts have a `.gen.go` suffix; hand-
// rolled assertion / fixture files do not.
package cryptotest

// Digest is opaque (unexported fields by design — size-
// discriminated via [crypto.NewDigest256] / [NewDigest384] /
// [NewDigest512]) so testkit builder is not applicable.
// Canonical Digest fixtures are hand-rolled via the public
// constructors; see crypto_fixtures.go.

// Hasher
//go:generate testkit stub -p go.thesmos.sh/core/crypto -o hasher_stub.gen.go Hasher
//go:generate testkit suite -p go.thesmos.sh/core/crypto -o hasher_spec.gen.go Hasher
//go:generate testkit bench -p go.thesmos.sh/core/crypto -o hasher_bench.gen.go Hasher
//go:generate testkit model -p go.thesmos.sh/core/crypto -o hasher_model.gen.go Hasher

// Stream
//go:generate testkit stub -p go.thesmos.sh/core/crypto -o stream_stub.gen.go Stream
//go:generate testkit suite -p go.thesmos.sh/core/crypto -o stream_spec.gen.go Stream
