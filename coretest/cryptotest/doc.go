// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package cryptotest holds testkit-generated test infrastructure
// for the crypto package's interface seams ([go.thesmos.sh/core/crypto.Hasher],
// [go.thesmos.sh/core/crypto.MAC]) and consumer-facing assertion
// bundles. Generated artefacts have a `.gen.go` suffix; hand-
// rolled assertion / fixture files do not.
package cryptotest

// Hasher
//go:generate testkit suite -p go.thesmos.sh/core/crypto -o hasher_spec.gen.go Hasher
