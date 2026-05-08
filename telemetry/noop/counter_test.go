// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"testing"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// newCounter is the SUT factory for the testkit-driven Counter
// contract suite.
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
	)
}

// --- noop-specific tests ---

func TestCounter(t *testing.T) {
	t.Parallel()
	c := newCounter()

	t.Run("Add discards negative values rather than panicking", func(t *testing.T) {
		// Locks the documented noop contract: noop discards
		// monotonic-precondition violations because it produces
		// no observable signal regardless. Production-grade
		// implementations panic; noop deliberately does not.
		t.Parallel()
		testkit.AssertNilSafe(t, func() {
			c.Add(t.Context(), -1)
			c.Add(t.Context(), -1_000_000)
		})
	})
}
