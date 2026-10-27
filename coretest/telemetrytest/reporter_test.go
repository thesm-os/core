// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest_test

import (
	"testing"

	"go.thesmos.sh/core/coretest/telemetrytest"
)

// TestReporter checks that a Recorder passes the checks of the suite of
// telemetry.Reporter.
func TestReporter(t *testing.T) {
	t.Parallel()

	telemetrytest.RunReporterSuite(t, telemetrytest.NewRecorder)
}
