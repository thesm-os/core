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

// counterSpec is the specification of the counters of the cases.
var counterSpec = telemetry.InstrumentSpec{Name: "n"}

// TestCounterConformsToCounter applies the checks of the suite of
// telemetry.Counter to the counter of a Reporter.
func TestCounterConformsToCounter(t *testing.T) {
	t.Parallel()

	telemetrytest.RunCounterSuite(t, func() telemetry.Counter { return noop.New().Counter(counterSpec) })
}

// TestCounterAllocs checks that no method of the counter allocates.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
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
