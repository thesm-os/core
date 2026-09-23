// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"testing"

	"go.thesmos.sh/testkit"
)

// Internal rather than in the resilience_test package because the
// circuit's state machine is unexported: it is how Breaker works, not
// part of its API.
func TestCircuitSpec(t *testing.T) {
	t.Parallel()

	t.Run("the declaration builds", func(t *testing.T) {
		t.Parallel()
		testkit.NoError(t, errCircuitSpec, "the circuit's state machine must be a valid declaration")
	})

	t.Run("the declaration is the documented one", func(t *testing.T) {
		t.Parallel()
		// Every edge, in declaration order. A change to the machine
		// changes this diagram, so the change is reviewed here.
		want := "stateDiagram-v2\n" +
			"    [*] --> Closed\n" +
			"    Closed --> Closed : Allow\n" +
			"    Closed --> Closed : Success\n" +
			"    Closed --> Open : Failure [guarded]\n" +
			"    Closed --> Closed : Failure\n" +
			"    Open --> HalfOpen : Allow [guarded]\n" +
			"    Open --> Open : Success\n" +
			"    Open --> Open : Failure\n" +
			"    HalfOpen --> HalfOpen : Allow [guarded]\n" +
			"    HalfOpen --> Open : Failure [guarded]\n" +
			"    HalfOpen --> HalfOpen : Failure\n" +
			"    HalfOpen --> Closed : Success [guarded]\n" +
			"    HalfOpen --> HalfOpen : Success [guarded]\n" +
			"    HalfOpen --> HalfOpen : Success\n"
		testkit.Equal(t, circuitSpec.Mermaid(), want, "the circuit's state machine must match its diagram")
	})
}
