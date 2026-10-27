// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package clocktest_test

import (
	"testing"

	"go.thesmos.sh/core/clock/hlc"
	"go.thesmos.sh/core/coretest/clocktest"
)

// TestClock checks that an hlc Clock passes the checks of the suite of
// clock.Clock.
func TestClock(t *testing.T) {
	t.Parallel()

	clocktest.RunClockSuite(t, func() *hlc.Clock { return hlc.New(1) })
}
