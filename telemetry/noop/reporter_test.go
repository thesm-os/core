// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
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
}
