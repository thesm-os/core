// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package idtest holds testkit-generated test infrastructure for
// the [go.thesmos.sh/core/id.Generator] interface seam plus
// hand-rolled assertion bundles. Generated artefacts have a
// `.gen.go` suffix; hand-rolled spec files do not.
package idtest

// Generator
//go:generate testkit stub -p go.thesmos.sh/core/id -o generator_stub.gen.go Generator
//go:generate testkit suite -p go.thesmos.sh/core/id -o generator_spec.gen.go Generator
//go:generate testkit bench -p go.thesmos.sh/core/id -o generator_bench.gen.go Generator
//go:generate testkit model -p go.thesmos.sh/core/id -o generator_model.gen.go Generator
