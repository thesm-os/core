// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package clocktest holds testkit-generated test infrastructure
// for the [go.thesmos.sh/core/clock] package: stub, conformance
// suite, builder, bench, model for [clock.Clock]; stub +
// conformance suite for [clock.Timer]; plus hand-rolled
// [ClockContractAssertions] / [TimerContractAssertions] bundles
// and canonical [Instant] / [InstantRange] fixtures.
//
// Generated artefacts have a `.gen.go` suffix; hand-rolled
// assertion / fixture files do not.
package clocktest

// Clock
//go:generate testkit builder -p go.thesmos.sh/core/clock -o clock_fixtures.gen.go Instant InstantRange
//go:generate testkit stub -p go.thesmos.sh/core/clock -o clock_stub.gen.go Clock
//go:generate testkit suite -p go.thesmos.sh/core/clock -o clock_spec.gen.go Clock
//go:generate testkit bench -p go.thesmos.sh/core/clock -o clock_bench.gen.go Clock
//go:generate testkit model -p go.thesmos.sh/core/clock -o clock_model.gen.go Clock

// Timer
//go:generate testkit stub -p go.thesmos.sh/core/clock -o timer_stub.gen.go Timer
//go:generate testkit suite -p go.thesmos.sh/core/clock -o timer_spec.gen.go Timer
