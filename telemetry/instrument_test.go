// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
)

func TestGaugeAggregation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		give telemetry.GaugeAggregation
		want uint8
	}{
		{name: "GaugeAggregationUnspecified is 0", give: telemetry.GaugeAggregationUnspecified, want: 0},
		{name: "GaugeAggregationSum is 1", give: telemetry.GaugeAggregationSum, want: 1},
		{name: "GaugeAggregationMax is 2", give: telemetry.GaugeAggregationMax, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, uint8(tt.give), tt.want, "the constant must keep its value")
		})
	}
}

func TestInstrumentSpec(t *testing.T) {
	t.Parallel()

	t.Run("Aggregation is GaugeAggregationUnspecified in the zero value", func(t *testing.T) {
		t.Parallel()
		var spec telemetry.InstrumentSpec
		testkit.Equal(t, spec.Aggregation, telemetry.GaugeAggregationUnspecified,
			"an InstrumentSpec that leaves Aggregation unset must mean GaugeAggregationUnspecified")
	})
}
