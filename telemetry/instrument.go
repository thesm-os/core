// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

// InstrumentName is a typed string naming a metric instrument.
// Use named [InstrumentName] constants in the package that emits
// the metric instead of inline string literals — typo'd metric
// names are a deployment-time discovery rather than a compile-time
// failure, so the type discipline matters.
type InstrumentName string

// InstrumentSpec carries the parameters that describe a metric
// instrument at construction time. The same value type is consumed
// by [Reporter.Counter], [Reporter.Gauge], and [Reporter.Histogram]
// — instrument metadata (Name, Description, Unit) is the same
// vocabulary across the three primitives, and the
// [InstrumentSpec.Bounds] field is histogram-only by intent.
//
// Mirrors OpenTelemetry's option-pattern for instrument metadata
// collapsed into a single value-type so consumers don't have to
// learn three parallel option chains.
//
// # Allocation contract
//
// Value type. The [InstrumentSpec.Bounds] slice header aliases the
// caller's buffer. Instrument creation is a cold path; the returned
// instrument's hot-path methods (Add / Set / Record) are zero-alloc.
type InstrumentSpec struct {
	// Name is the instrument's identifier. Required. Subsequent
	// constructions with the same Name on the same Reporter must
	// return the same underlying instrument.
	Name InstrumentName

	// Description is human-prose documentation surfaced by
	// observability backends (Prometheus HELP text, OTLP
	// Instrument.Description). Optional.
	Description string

	// Unit is the UCUM-encoded unit of measure (for example "ms",
	// "By", "{request}"). Surfaced as the Prometheus _unit suffix
	// and OTLP Instrument.Unit. Optional.
	Unit string

	// Bounds is the explicit bucket boundary set for histogram
	// instruments. Ignored by [Reporter.Counter] and
	// [Reporter.Gauge]. When nil, [Histogram] implementations apply
	// their default bucket schema.
	Bounds []float64
}
