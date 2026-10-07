// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// counterSpec is the specification of the counters of the cases.
var counterSpec = telemetry.InstrumentSpec{Name: "n"}

func TestNoopCounterContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertCounterContract(t, func() telemetry.Counter { return noop.New().Counter(counterSpec) },
		telemetrytest.CounterContractAssertions()...)
}

// TestCounterAllocs checks that no method of the counter allocates.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestCounterAllocs(t *testing.T) {
	ctx := t.Context()
	c := noop.New().Counter(counterSpec)
	attrs := []telemetry.Attr{telemetry.AttrString("k", "v")}

	t.Run("Add", func(t *testing.T) {
		expect.MaxAllocs(t, func() { c.Add(ctx, 1) }, 0, "Add must not allocate")
	})

	t.Run("With", func(t *testing.T) {
		var got telemetry.Counter
		expect.MaxAllocs(t, func() { got = c.With(attrs) }, 0, "With must not allocate")
		assert.Equal(t, got, c, "With must return the receiver")
	})

	t.Run("Release", func(t *testing.T) {
		expect.MaxAllocs(t, func() { c.Release() }, 0, "Release must not allocate")
	})
}

func BenchmarkNoopCounter(b *testing.B) {
	telemetrytest.BenchmarkCounterContract(b, func() telemetry.Counter { return noop.New().Counter(counterSpec) },
		telemetrytest.CounterBenchOnAdd(bench.MutatorAllocsWithin[telemetry.Counter, int64](1, 0)),
		telemetrytest.CounterBenchOnWith(bench.PureAllocsWithin[telemetry.Counter, telemetry.Counter](0)),
		telemetrytest.CounterBenchOnRelease(telemetrytest.ReleaseAllocsWithin(
			func(c telemetry.Counter) telemetry.Counter { return c.With(nil) }, 0,
		)),
	)
}
