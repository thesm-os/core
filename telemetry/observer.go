// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import "context"

// Observer is a [Reporter] that reads the value of a gauge when it collects
// the gauge. A library registers a function that reads the value, such as an
// atomic count of a budget. The reporter calls the function at each
// collection, so the hot path that changes the value does not call an
// instrument. The metrics API of OpenTelemetry calls such an instrument
// asynchronous, and Prometheus calls it a GaugeFunc.
//
// A Reporter does not have to implement Observer. A library asserts it on its
// Reporter:
//
//	if o, ok := r.(telemetry.Observer); ok {
//	    obs := o.Observe(spec, attrs, func(context.Context) float64 {
//	        return float64(budget.Load())
//	    })
//	    defer obs.Release()
//	}
//
// # Concurrency
//
// Implementations must be safe for concurrent use. A reporter can call a
// read function on any goroutine, so the function reads its value with an
// operation that is safe for concurrent use. An atomic load is such an
// operation.
type Observer interface {
	// Observe registers read for the series of the gauge of spec with the
	// attribute set attrs. The reporter calls read with the context of each
	// collection of the gauge until [Observation.Release].
	// [InstrumentSpec.Aggregation] states how the values of the
	// registrations of one attribute set combine, as it does for the
	// gauges of [Gauge.With].
	Observe(spec InstrumentSpec, attrs []Attr, read func(ctx context.Context) float64) Observation
}

// Observation is one registration of [Observer.Observe].
type Observation interface {
	// Release ends the calls of the read function of the registration. A
	// second Release does nothing. A collection that runs while Release
	// runs can call the function once more.
	Release()
}
