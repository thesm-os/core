// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// gaugeSpec is the specification of the gauges of the cases.
var gaugeSpec = telemetry.InstrumentSpec{Name: "n"}

// TestGaugeConformsToGauge applies the checks of the suite of telemetry.Gauge
// to the gauge of a Reporter.
func TestGaugeConformsToGauge(t *testing.T) {
	t.Parallel()

	telemetrytest.RunGaugeSuite(t, func() telemetry.Gauge { return noop.New().Gauge(gaugeSpec) })
}

// TestGaugeAllocs checks that no method of the gauge allocates. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
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
