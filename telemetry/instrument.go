// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

// InstrumentName is the name of a metric instrument. A package that
// emits a metric declares its names as InstrumentName constants, so the
// compiler rejects a misspelt name.
type InstrumentName string

// GaugeAggregation states how the values of a gauge's attribute sets
// combine, in the overflow series of an adapter that caps the attribute
// sets of an instrument, and in any total across sets. [Gauge]
// describes each value.
type GaugeAggregation uint8

const (
	// GaugeAggregationUnspecified defines no combination: the values of
	// different attribute sets are non-additive, and an overflow series
	// has no defined value. It is the zero value, so it applies to every
	// gauge whose InstrumentSpec leaves Aggregation unset.
	GaugeAggregationUnspecified GaugeAggregation = 0

	// GaugeAggregationSum adds the values, as for a depth or a count.
	// The OpenTelemetry API calls an instrument of additive values an
	// UpDownCounter.
	GaugeAggregationSum GaugeAggregation = 1

	// GaugeAggregationMax takes the largest value recorded in an export
	// interval, as for a lag or an age.
	GaugeAggregationMax GaugeAggregation = 2
)

// InstrumentSpec describes a metric instrument when a [Reporter]
// constructs it. [Reporter.Counter], [Reporter.Gauge] and
// [Reporter.Histogram] take the same type. Name, Description and Unit
// apply to every instrument, [InstrumentSpec.Bounds] only to a
// histogram, and [InstrumentSpec.Aggregation] only to a gauge.
//
// It collapses the option functions that OpenTelemetry uses for
// instrument metadata into one value type, so a caller learns one type
// instead of three chains of options.
//
// # Allocation contract
//
// InstrumentSpec is a value type. The [InstrumentSpec.Bounds] slice
// header aliases the caller's buffer. Constructing an instrument is a
// cold path, and the methods that emit, Add, Set and Record, are
// zero-alloc.
type InstrumentSpec struct {
	// Name identifies the instrument, and is required. A second
	// construction with the same Name on the same Reporter returns the
	// same instrument.
	Name InstrumentName

	// Description documents the instrument for observability backends,
	// as the HELP text of Prometheus or the Description of an OTLP
	// instrument. It is optional.
	Description string

	// Unit is the UCUM unit of measure, such as "ms", "By" or
	// "{request}", which becomes the unit suffix of Prometheus and the
	// Unit of an OTLP instrument. It is optional.
	Unit string

	// Bounds is the set of explicit bucket boundaries of a histogram.
	// [Reporter.Counter] and [Reporter.Gauge] ignore it. When Bounds is
	// nil, a [Histogram] uses its default buckets.
	Bounds []float64

	// Aggregation states how the values of a gauge's attribute sets
	// combine. [Reporter.Counter] and [Reporter.Histogram] ignore it. A
	// value above [GaugeAggregationMax] means
	// [GaugeAggregationUnspecified].
	Aggregation GaugeAggregation
}
