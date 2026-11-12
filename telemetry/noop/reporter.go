// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package noop

import (
	"context"

	"go.thesmos.sh/core/telemetry"
)

// Reporter is a [telemetry.Reporter] that discards every signal.
// The zero value is usable. Stateless and safe for concurrent use. It is
// also a [telemetry.Observer] that does not call a read function.
//
//dokimi:subject telemetry.Reporter new=New
type Reporter struct{}

// Compile-time interface checks.
var (
	_ telemetry.Reporter = Reporter{}
	_ telemetry.Observer = Reporter{}
)

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

// Observe returns a no-op [telemetry.Observation], and does not call read.
func (Reporter) Observe(
	telemetry.InstrumentSpec,
	[]telemetry.Attr,
	func(context.Context) float64,
) telemetry.Observation {
	return observation{}
}
