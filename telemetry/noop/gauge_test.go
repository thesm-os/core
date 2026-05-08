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

// newGauge is the SUT factory for the testkit-driven Gauge
// contract suite.
func newGauge() telemetry.Gauge {
	return noop.New().Gauge(telemetry.InstrumentSpec{Name: "n"})
}

// --- testkit-driven contract layer ---

func TestNoopGaugeContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertGaugeContract(t, newGauge,
		telemetrytest.GaugeContractAssertions()...,
	)
}

func BenchmarkNoopGauge(b *testing.B) {
	telemetrytest.BenchmarkGaugeContract(b, newGauge,
		telemetrytest.GaugeBenchOnSet(bench.MutatorAllocsWithin[telemetry.Gauge, float64](1.0, 0)),
		telemetrytest.GaugeBenchOnAdd(bench.MutatorAllocsWithin[telemetry.Gauge, float64](0.1, 0)),
		telemetrytest.GaugeBenchOnWith(bench.PureAllocsWithin[telemetry.Gauge, telemetry.Gauge](0)),
	)
}
