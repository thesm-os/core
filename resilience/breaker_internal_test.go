// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"testing"

	"go.thesmos.sh/testkit"
)

// TestCircuitSpec is in package resilience because circuitSpec and
// errCircuitSpec are unexported.
func TestCircuitSpec(t *testing.T) {
	t.Parallel()

	t.Run("Build accepts the declaration", func(t *testing.T) {
		t.Parallel()
		testkit.NoError(t, errCircuitSpec, "the circuit's state machine must be a valid declaration")
	})
}

// TestEventString is in package resilience because event is
// unexported.
func TestEventString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give event
		want string
	}{
		{allow, "Allow"},
		{success, "Success"},
		{failure, "Failure"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tt.give.String(), tt.want, "String must name the event")
		})
	}
}
