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

// gaugeSpec is the specification of the gauges of the cases.
var gaugeSpec = telemetry.InstrumentSpec{Name: "n"}

func TestNoopGaugeContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertGaugeContract(t, func() telemetry.Gauge { return noop.New().Gauge(gaugeSpec) },
		telemetrytest.GaugeContractAssertions()...)
}

// TestGaugeAllocs checks that no method of the gauge allocates. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
//
//nolint:paralleltest // see above
func TestGaugeAllocs(t *testing.T) {
	ctx := t.Context()
	g := noop.New().Gauge(gaugeSpec)
	attrs := []telemetry.Attr{telemetry.AttrString("k", "v")}

	t.Run("Set", func(t *testing.T) {
		expect.MaxAllocs(t, func() { g.Set(ctx, 1) }, 0, "Set must not allocate")
	})

	t.Run("Add", func(t *testing.T) {
		expect.MaxAllocs(t, func() { g.Add(ctx, 0.1) }, 0, "Add must not allocate")
	})

	t.Run("With", func(t *testing.T) {
		var got telemetry.Gauge
		expect.MaxAllocs(t, func() { got = g.With(attrs) }, 0, "With must not allocate")
		assert.Equal(t, got, g, "With must return the receiver")
	})

	t.Run("Release", func(t *testing.T) {
		expect.MaxAllocs(t, func() { g.Release() }, 0, "Release must not allocate")
	})
}

func BenchmarkNoopGauge(b *testing.B) {
	telemetrytest.BenchmarkGaugeContract(b, func() telemetry.Gauge { return noop.New().Gauge(gaugeSpec) },
		telemetrytest.GaugeBenchOnSet(bench.MutatorAllocsWithin[telemetry.Gauge, float64](1.0, 0)),
		telemetrytest.GaugeBenchOnAdd(bench.MutatorAllocsWithin[telemetry.Gauge, float64](0.1, 0)),
		telemetrytest.GaugeBenchOnWith(bench.PureAllocsWithin[telemetry.Gauge, telemetry.Gauge](0)),
		telemetrytest.GaugeBenchOnRelease(telemetrytest.ReleaseAllocsWithin(
			func(g telemetry.Gauge) telemetry.Gauge { return g.With(nil) }, 0,
		)),
	)
}
