// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package clocktest_test

import (
	"testing"
	"time"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/coretest/clocktest"
)

func TestClockFixtures(t *testing.T) {
	t.Parallel()

	sample := clock.Instant{Wall: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano(), Node: 1}

	t.Run("NewInstantOrigin", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a builder of the zero Instant", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, clocktest.NewInstantOrigin().Build(), clock.Instant{},
				"NewInstantOrigin builds the zero Instant")
		})
	})

	t.Run("NewInstantSample", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a builder of the Instant at 2026-01-01 on node 1", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, clocktest.NewInstantSample().Build(), sample,
				"NewInstantSample builds the Instant at 2026-01-01T00:00:00Z on node 1")
		})
	})

	t.Run("NewInstantPeer", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a builder of the sample Instant on node 2", func(t *testing.T) {
			t.Parallel()
			peer := sample
			peer.Node = 2
			assert.Equal(t, clocktest.NewInstantPeer().Build(), peer,
				"NewInstantPeer builds the Instant of NewInstantSample on node 2")
		})
	})

	t.Run("NewInstantRangeSample", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a builder of the range from the zero Instant to the sample Instant", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, clocktest.NewInstantRangeSample().Build(), clock.InstantRange{Until: sample},
				"NewInstantRangeSample builds the range from the zero Instant to the Instant of NewInstantSample")
		})
	})
}
