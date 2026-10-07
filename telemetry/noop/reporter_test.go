// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

func TestNoopReporterContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertReporterContract(t, func() telemetry.Reporter { return noop.New() },
		telemetrytest.ReporterContractAssertions()...)
}

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

func BenchmarkNoopReporter(b *testing.B) {
	telemetrytest.BenchmarkReporterContract(b, func() telemetry.Reporter { return noop.New() })
}
