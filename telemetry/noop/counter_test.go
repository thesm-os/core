// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"testing"

	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// newCounter returns a Counter of a new no-op Reporter, for the contract
// assertions and the benchmark.
func newCounter() telemetry.Counter {
	return noop.New().Counter(telemetry.InstrumentSpec{Name: "n"})
}

// --- testkit-driven contract layer ---

func TestNoopCounterContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertCounterContract(t, newCounter,
		telemetrytest.CounterContractAssertions()...,
	)
}

func BenchmarkNoopCounter(b *testing.B) {
	telemetrytest.BenchmarkCounterContract(b, newCounter,
		telemetrytest.CounterBenchOnAdd(bench.MutatorAllocsWithin[telemetry.Counter, int64](1, 0)),
		telemetrytest.CounterBenchOnWith(bench.PureAllocsWithin[telemetry.Counter, telemetry.Counter](0)),
		telemetrytest.CounterBenchOnRelease(telemetrytest.ReleaseAllocsWithin(
			func(c telemetry.Counter) telemetry.Counter { return c.With(nil) }, 0)),
	)
}
