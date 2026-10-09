// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
)

// probed is the target of the internal cases of the Breaker.
const probed = "inventory"

// TestCircuitSpec is in package resilience because circuitSpec and
// errCircuitSpec are unexported.
func TestCircuitSpec(t *testing.T) {
	t.Parallel()

	t.Run("builds without an error", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, errCircuitSpec, "the state machine of a circuit must be a valid declaration")
	})
}

// TestEvent is in package resilience because event is unexported.
func TestEvent(t *testing.T) {
	t.Parallel()

	t.Run("String", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give event
			want string
		}{
			{name: "returns Allow for allow", give: allow, want: "Allow"},
			{name: "returns Success for success", give: success, want: "Success"},
			{name: "returns Failure for failure", give: failure, want: "Failure"},
			{name: "returns Release for release", give: release, want: "Release"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.String(), tt.want, "String must name the event")
			})
		}
	})
}

// TestBreakerInternal is in package resilience because admit and abandon,
// which number the probes, are unexported.
func TestBreakerInternal(t *testing.T) {
	t.Parallel()

	t.Run("admit", func(t *testing.T) {
		t.Parallel()

		t.Run("gives a call of a closed circuit no probe", func(t *testing.T) {
			t.Parallel()
			b, _ := halfOpen(t)
			for range 2 {
				b.Allow(probed)
				b.Record(probed, false)
			}
			assert.Equal(t, b.State(probed), Closed, "two probe successes must close the circuit")

			probe, ok := b.admit(probed)
			expect.True(t, ok, "a closed circuit must admit a call")
			expect.Equal(t, probe, uint64(0), "a call of a closed circuit must get no probe")
		})

		t.Run("numbers each probe", func(t *testing.T) {
			t.Parallel()
			b, _ := halfOpen(t)

			first, ok := b.admit(probed)
			assert.True(t, ok, "the circuit must admit the first probe")
			b.Record(probed, false)

			second, ok := b.admit(probed)
			assert.True(t, ok, "the circuit must admit the second probe")
			expect.Equal(t, first, uint64(1), "the first probe must be number 1")
			expect.Equal(t, second, uint64(2), "the second probe must be number 2")
		})

		t.Run("continues the numbers after the circuit closed", func(t *testing.T) {
			t.Parallel()
			b, c := halfOpen(t)
			reopenAfterClose(t, b, c)

			third, ok := b.admit(probed)
			assert.True(t, ok, "the circuit must admit the third probe")
			assert.Equal(t, third, uint64(3), "the numbers must continue after the circuit closed")
		})
	})

	t.Run("abandon", func(t *testing.T) {
		t.Parallel()

		t.Run("admits the next probe for the current probe", func(t *testing.T) {
			t.Parallel()
			b, _ := halfOpen(t)

			probe, _ := b.admit(probed)
			b.abandon(probed, probe)
			assert.True(t, b.Allow(probed), "the circuit must admit the next probe")
		})

		t.Run("keeps the current probe outstanding for an earlier probe", func(t *testing.T) {
			t.Parallel()
			b, _ := halfOpen(t)

			first, _ := b.admit(probed)
			b.Record(probed, false)
			second, _ := b.admit(probed)

			b.abandon(probed, first)
			assert.False(t, b.Allow(probed), "the second probe must still be outstanding")

			b.abandon(probed, second)
			assert.True(t, b.Allow(probed), "an abandon of the second probe must admit the next one")
		})

		t.Run("changes nothing for a call without a probe", func(t *testing.T) {
			t.Parallel()
			b, _ := halfOpen(t)

			_, _ = b.admit(probed)
			b.abandon(probed, 0)
			assert.False(t, b.Allow(probed), "the probe must still be outstanding")
		})

		t.Run("changes nothing for a probe from before the circuit closed", func(t *testing.T) {
			t.Parallel()
			b, c := halfOpen(t)
			first, _ := b.admit(probed)
			b.Record(probed, false)
			reopenAfterClose(t, b, c)

			_, _ = b.admit(probed)
			b.abandon(probed, first)
			assert.False(t, b.Allow(probed), "a probe from before the circuit closed must not release the third")
		})
	})
}

// halfOpen returns a Breaker whose circuit of probed opened at its second
// failure and whose open interval has elapsed, and the clock that drives
// it. Its circuit closes after two probe successes.
func halfOpen(t *testing.T) (*Breaker, *fake.Clock) {
	t.Helper()

	c := fake.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b, err := NewBreaker(BreakerConfig{
		Clock:            c,
		TripOn:           []errs.Class{errs.Transient},
		FailureThreshold: 2,
		SuccessThreshold: 2,
		OpenFor:          30 * time.Second,
	})
	assert.NoError(t, err, "NewBreaker must accept a complete config")

	for range 2 {
		b.Allow(probed)
		b.Record(probed, true)
	}

	c.Advance(31 * time.Second)

	return b, c
}

// reopenAfterClose closes the half-open circuit of probed of b with two
// probe successes, opens it again with two failures, and lets its open
// interval elapse on c, and fails t when the circuit does not close.
func reopenAfterClose(t *testing.T, b *Breaker, c *fake.Clock) {
	t.Helper()

	for range 2 {
		b.Allow(probed)
		b.Record(probed, false)
	}
	assert.Equal(t, b.State(probed), Closed, "two probe successes must close the circuit")

	for range 2 {
		b.Allow(probed)
		b.Record(probed, true)
	}
	c.Advance(31 * time.Second)
}
