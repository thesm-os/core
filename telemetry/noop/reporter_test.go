// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"testing"

	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

func TestReporterFactoryReturnsNonNil(t *testing.T) {
	t.Parallel()

	r := noop.New()
	spec := telemetry.InstrumentSpec{Name: "test.metric"}

	if got := r.Counter(spec); got == nil {
		t.Fatal("Counter returned nil")
	}
	if got := r.Gauge(spec); got == nil {
		t.Fatal("Gauge returned nil")
	}
	if got := r.Histogram(spec); got == nil {
		t.Fatal("Histogram returned nil")
	}
	if got := r.Tracer("test.lib"); got == nil {
		t.Fatal("Tracer returned nil")
	}
}

func TestReporterZeroValueEqualsNew(t *testing.T) {
	t.Parallel()

	var z noop.Reporter
	n := noop.New()
	if z != n {
		t.Fatalf("zero-value Reporter differs from New(): got %v, want %v", z, n)
	}
}
