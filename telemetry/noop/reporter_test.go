// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// newReporter is the SUT factory for the testkit-driven Reporter
// contract suite.
func newReporter() telemetry.Reporter { return noop.New() }

// --- testkit-driven contract layer ---

func TestNoopReporterContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertReporterContract(t, newReporter,
		telemetrytest.ReporterContractAssertions()...,
	)
}

func BenchmarkNoopReporter(b *testing.B) {
	telemetrytest.BenchmarkReporterContract(b, newReporter)
}

// --- noop-specific tests ---

func TestReporterZeroValueEqualsNew(t *testing.T) {
	t.Parallel()
	testkit.Equal(t, noop.Reporter{}, noop.New(),
		"zero-value Reporter must equal New() — both are stateless empty structs")
}
