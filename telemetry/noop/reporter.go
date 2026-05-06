// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop

import "go.thesmos.sh/core/telemetry"

// Reporter is a [telemetry.Reporter] that discards every signal.
// The zero value is usable. Stateless and safe for concurrent use.
type Reporter struct{}

// Compile-time interface check.
var _ telemetry.Reporter = Reporter{}

// New returns a [Reporter]. Equivalent to the zero-value [Reporter];
// offered as a constructor for use sites that prefer one.
func New() Reporter { return Reporter{} }

// Counter returns a no-op [telemetry.Counter] that discards every
// emission and ignores attribute pre-binding.
func (Reporter) Counter(telemetry.InstrumentSpec) telemetry.Counter {
	return counter{}
}

// Gauge returns a no-op [telemetry.Gauge] that discards every
// emission and ignores attribute pre-binding.
func (Reporter) Gauge(telemetry.InstrumentSpec) telemetry.Gauge {
	return gauge{}
}

// Histogram returns a no-op [telemetry.Histogram] that discards
// every emission and ignores attribute pre-binding.
func (Reporter) Histogram(telemetry.InstrumentSpec) telemetry.Histogram {
	return histogram{}
}

// Tracer returns a no-op [telemetry.Tracer] that returns a no-op
// span on every [telemetry.Tracer.Start] call.
func (Reporter) Tracer(telemetry.InstrumentName) telemetry.Tracer {
	return tracer{}
}
