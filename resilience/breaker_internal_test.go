// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
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
		{release, "Release"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tt.give.String(), tt.want, "String must name the event")
		})
	}
}

// TestBreakerProbe is in package resilience because admit and abandon,
// which number the probes, are unexported.
func TestBreakerProbe(t *testing.T) {
	t.Parallel()

	const name = "inventory"

	// halfOpen returns a Breaker whose circuit of name opened at its
	// second failure and whose open interval has elapsed, and the clock
	// that drives it. Its circuit closes after two probe successes.
	halfOpen := func(t *testing.T) (*Breaker, *fake.Clock) {
		t.Helper()

		c := fake.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		b, err := NewBreaker(BreakerConfig{
			Clock:            c,
			TripOn:           []errs.Class{errs.Transient},
			FailureThreshold: 2,
			SuccessThreshold: 2,
			OpenFor:          30 * time.Second,
		})
		testkit.NoError(t, err, "NewBreaker must accept a complete config")

		for range 2 {
			b.Allow(name)
			b.Record(name, true)
		}

		c.Advance(31 * time.Second)

		return b, c
	}

	t.Run("admit gives a call of a closed circuit no probe", func(t *testing.T) {
		t.Parallel()
		b, _ := halfOpen(t)
		b.Allow(name)
		b.Record(name, false)
		b.Allow(name)
		b.Record(name, false)
		testkit.Equal(t, b.State(name), Closed, "two probe successes must close the circuit")

		probe, ok := b.admit(name)
		testkit.True(t, ok, "a closed circuit must admit a call")
		testkit.Equal(t, probe, uint64(0), "a call of a closed circuit must get no probe")
	})

	t.Run("admit numbers each probe", func(t *testing.T) {
		t.Parallel()
		b, _ := halfOpen(t)

		first, ok := b.admit(name)
		testkit.True(t, ok, "the circuit must admit the first probe")
		b.Record(name, false)

		second, ok := b.admit(name)
		testkit.True(t, ok, "the circuit must admit the second probe")
		testkit.Equal(t, first, uint64(1), "the first probe must be number 1")
		testkit.Equal(t, second, uint64(2), "the second probe must be number 2")
	})

	t.Run("abandoning the current probe admits the next one", func(t *testing.T) {
		t.Parallel()
		b, _ := halfOpen(t)

		probe, _ := b.admit(name)
		b.abandon(name, probe)

		testkit.True(t, b.Allow(name), "the circuit must admit the next probe")
	})

	t.Run("abandoning an earlier probe leaves the current probe outstanding", func(t *testing.T) {
		t.Parallel()
		b, _ := halfOpen(t)

		first, _ := b.admit(name)
		b.Record(name, false)
		second, _ := b.admit(name)

		b.abandon(name, first)
		testkit.False(t, b.Allow(name), "the second probe must still be outstanding")

		b.abandon(name, second)
		testkit.True(t, b.Allow(name), "abandoning the second probe must admit the next one")
	})

	t.Run("abandoning a call without a probe changes nothing", func(t *testing.T) {
		t.Parallel()
		b, _ := halfOpen(t)

		_, _ = b.admit(name)
		b.abandon(name, 0)

		testkit.False(t, b.Allow(name), "the probe must still be outstanding")
	})

	t.Run("a probe number survives the circuit closing", func(t *testing.T) {
		t.Parallel()
		b, c := halfOpen(t)

		first, _ := b.admit(name)
		b.Record(name, false)
		b.Allow(name)
		b.Record(name, false)
		testkit.Equal(t, b.State(name), Closed, "two probe successes must close the circuit")

		b.Allow(name)
		b.Record(name, true)
		b.Allow(name)
		b.Record(name, true)
		c.Advance(31 * time.Second)

		third, _ := b.admit(name)
		testkit.Equal(t, third, uint64(3), "the numbers must continue after the circuit closed")

		b.abandon(name, first)
		testkit.False(t, b.Allow(name), "a probe from before the circuit closed must not release the third")
	})
}
