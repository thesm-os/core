// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package clocktest_test

import (
	"testing"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/hlc"
	"go.thesmos.sh/core/coretest/clocktest"
)

// TestTimer checks that a pending Timer of an hlc Clock passes the checks of
// the suite of clock.Timer.
func TestTimer(t *testing.T) {
	t.Parallel()

	clocktest.RunTimerSuite(t, func() clock.Timer { return hlc.New(1).NewTimer(time.Hour) })
}
