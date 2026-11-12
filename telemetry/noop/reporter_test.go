// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"context"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/telemetry/noop"
)

func TestReporter(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the zero Reporter", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, noop.New(), noop.Reporter{}, "New must return the zero Reporter")
		})
	})

	t.Run("Observe", func(t *testing.T) {
		t.Parallel()

		t.Run("does not call the read function", func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			obs := noop.New().Observe(gaugeSpec, nil, func(context.Context) float64 {
				calls.Add(1)

				return 1
			})
			obs.Release()
			obs.Release()
			assert.Equal(t, calls.Load(), 0, "the no-op Reporter must not read the gauge")
		})
	})
}
