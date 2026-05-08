// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package randtest holds testkit-generated test infrastructure
// for the [go.thesmos.sh/core/rand.Rand] interface seam plus
// hand-rolled assertion bundles and stub helpers. Generated
// artefacts have a `.gen.go` suffix; hand-rolled spec / stub /
// helper files do not.
package randtest

// Rand
//go:generate testkit stub -p go.thesmos.sh/core/rand -o rand_stub.gen.go Rand
//go:generate testkit suite -p go.thesmos.sh/core/rand -o rand_spec.gen.go Rand
//go:generate testkit bench -p go.thesmos.sh/core/rand -o rand_bench.gen.go Rand
//go:generate testkit model -p go.thesmos.sh/core/rand -o rand_model.gen.go Rand
