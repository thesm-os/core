// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"bytes"
	"cmp"
	"context"
	"math"
	"slices"
	"sync"

	"go.thesmos.sh/core/telemetry"
)

// Recorder is a [telemetry.Reporter] that keeps what a package under test
// emits: the spec of each instrument, every value of each series of an
// instrument, and every span that ends. A test passes a Recorder as the
// Reporter of the package, and reads the signals with [Recorder.Specs],
// [Recorder.Values], [Recorder.Series] and [Recorder.Spans].
//
// # Instruments and series
//
// A Recorder keeps one instrument per [telemetry.InstrumentName]. The first
// [telemetry.InstrumentSpec] of a name defines the instrument, and a later
// Counter, Gauge or Histogram of the name returns an instrument of the same
// series. Each attribute set that With binds is a series of the instrument.
// Two attribute sets with the same attributes in another order are one
// series. The instrument that Counter, Gauge or Histogram returns emits
// into the series of the empty attribute set.
//
// A series keeps one value for each emit:
//
//   - [telemetry.Counter.Add] keeps its value. It discards a negative value,
//     as the contract of a Counter states.
//   - [telemetry.Gauge.Set] and [telemetry.Gauge.Add] keep the value of the
//     Gauge after the call. Set replaces the value, and Add changes it. Each
//     Gauge that With returns has a value of its own, which starts at 0.
//   - [telemetry.Histogram.Record] keeps its value.
//
// An instrument keeps no value after its Release.
//
// # Spans
//
// A [telemetry.Tracer] of a Recorder starts spans that record their name,
// kind, identity, parent, attributes, events and error. [Recorder.Spans]
// returns each span once it has ended.
//
// # Concurrency
//
// A Recorder and its instruments are safe for concurrent use. A span is
// for one goroutine, as the contract of [telemetry.Span] states.
//
// # Allocation contract
//
// An emit allocates when the slice of the values of its series grows.
// The zero Recorder is ready for use.
type Recorder struct {
	// instruments are the instruments in the order of their first
	// construction.
	instruments []*instrument

	// spans are the spans that have ended, in the order of their End.
	spans []Span

	// spanIDs is the number of the last span that a Tracer started. The
	// identity of a span derives from its number.
	spanIDs uint64

	mu sync.Mutex
}

var _ telemetry.Reporter = (*Recorder)(nil)

// instrument is one instrument of a [Recorder]: its spec, and its series
// in the order of their first binding.
type instrument struct {
	series []*series
	spec   telemetry.InstrumentSpec
}

// series is one attribute set of an instrument and the values that its
// emits kept.
type series struct {
	// attrs is a copy of the attribute set, sorted by key.
	attrs  []telemetry.Attr
	values []float64
}

// bound is the state of one instrument value that a [Recorder] returns:
// its instrument, its series, its value as a gauge, and whether Release
// ended it. A Recorder method returns a bound without a series, which
// finds its series at its first emit and which Release does not end. The
// fields after r are guarded by the mutex of r.
type bound struct {
	r  *Recorder
	in *instrument

	// series is the series of the attribute set that With bound, and nil
	// until the first emit of an instrument that a Recorder method
	// returned.
	series *series

	// value is the value of a gauge.
	value float64

	// fromWith reports whether With returned the instrument, and released
	// whether Release ended it.
	fromWith, released bool
}

// counter, gauge and histogram are the instruments of a [Recorder].
type (
	counter   struct{ b *bound }
	gauge     struct{ b *bound }
	histogram struct{ b *bound }
)

var (
	_ telemetry.Counter   = counter{}
	_ telemetry.Gauge     = gauge{}
	_ telemetry.Histogram = histogram{}
)

// NewRecorder returns an empty Recorder.
//
// # Allocation contract
//
// NewRecorder allocates the Recorder.
func NewRecorder() *Recorder {
	return &Recorder{}
}

// Counter returns the counter of spec.Name, which emits into the series
// of the empty attribute set.
//
// # Allocation contract
//
// Counter allocates the state of the counter, and the instrument for a
// new name.
func (r *Recorder) Counter(spec telemetry.InstrumentSpec) telemetry.Counter {
	return counter{r.unbound(spec)}
}

// Gauge returns the gauge of spec.Name, which emits into the series of
// the empty attribute set.
//
// # Allocation contract
//
// As [Recorder.Counter].
func (r *Recorder) Gauge(spec telemetry.InstrumentSpec) telemetry.Gauge {
	return gauge{r.unbound(spec)}
}

// Histogram returns the histogram of spec.Name, which emits into the
// series of the empty attribute set.
//
// # Allocation contract
//
// As [Recorder.Counter].
func (r *Recorder) Histogram(spec telemetry.InstrumentSpec) telemetry.Histogram {
	return histogram{r.unbound(spec)}
}

// Specs returns the spec of each instrument, in the order of the first
// construction of its name.
//
// # Allocation contract
//
// Specs allocates the slice that it returns.
func (r *Recorder) Specs() []telemetry.InstrumentSpec {
	r.mu.Lock()
	defer r.mu.Unlock()

	specs := make([]telemetry.InstrumentSpec, 0, len(r.instruments))
	for _, in := range r.instruments {
		specs = append(specs, in.spec)
	}

	return specs
}

// Values returns the values that the series of the instrument name with
// the attribute set attrs kept, in order. The order of attrs does not
// matter. Values returns nil for a name or an attribute set without a
// series.
//
// # Allocation contract
//
// Values allocates the slice that it returns, and a sorted copy of attrs.
func (r *Recorder) Values(name telemetry.InstrumentName, attrs ...telemetry.Attr) []float64 {
	want := sortedAttrs(attrs)

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, in := range r.instruments {
		if in.spec.Name == name {
			for _, s := range in.series {
				if slices.EqualFunc(s.attrs, want, sameAttr) {
					return slices.Clone(s.values)
				}
			}
		}
	}

	return nil
}

// Series returns the attribute set of each series of the instrument name,
// in the order of its first binding, a released series included. Each
// attribute set is sorted by key. Series returns nil for a name without an
// instrument.
//
// # Allocation contract
//
// Series allocates the slices that it returns.
func (r *Recorder) Series(name telemetry.InstrumentName) [][]telemetry.Attr {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, in := range r.instruments {
		if in.spec.Name == name {
			sets := make([][]telemetry.Attr, 0, len(in.series))
			for _, s := range in.series {
				sets = append(sets, slices.Clone(s.attrs))
			}

			return sets
		}
	}

	return nil
}

// Spans returns every span that has ended, in the order of its End.
//
// # Allocation contract
//
// Spans allocates the slice that it returns.
func (r *Recorder) Spans() []Span {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.spans)
}

// unbound returns the state of an instrument that a Recorder method
// returns for spec: the instrument of spec.Name, which unbound creates for
// a new name.
func (r *Recorder) unbound(spec telemetry.InstrumentSpec) *bound {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, in := range r.instruments {
		if in.spec.Name == spec.Name {
			return &bound{r: r, in: in}
		}
	}

	in := &instrument{spec: spec}
	r.instruments = append(r.instruments, in)

	return &bound{r: r, in: in}
}

// Add keeps value in the series of c. It discards a negative value.
func (c counter) Add(_ context.Context, value int64) {
	if value < 0 {
		return
	}

	c.b.emit(float64(value), false)
}

// With returns a counter of the series of attrs.
func (c counter) With(attrs []telemetry.Attr) telemetry.Counter {
	return counter{c.b.with(attrs)}
}

// Release ends c when With returned it.
func (c counter) Release() {
	c.b.release()
}

// Set replaces the value of g with value, and keeps it in the series of g.
func (g gauge) Set(_ context.Context, value float64) {
	g.b.emit(value, false)
}

// Add changes the value of g by delta, and keeps the new value in the
// series of g.
func (g gauge) Add(_ context.Context, delta float64) {
	g.b.emit(delta, true)
}

// With returns a gauge of the series of attrs, whose value starts at 0.
func (g gauge) With(attrs []telemetry.Attr) telemetry.Gauge {
	return gauge{g.b.with(attrs)}
}

// Release ends g when With returned it.
func (g gauge) Release() {
	g.b.release()
}

// Record keeps value in the series of h.
func (h histogram) Record(_ context.Context, value float64) {
	h.b.emit(value, false)
}

// With returns a histogram of the series of attrs.
func (h histogram) With(attrs []telemetry.Attr) telemetry.Histogram {
	return histogram{h.b.with(attrs)}
}

// Release ends h when With returned it.
func (h histogram) Release() {
	h.b.release()
}

// with returns the state of an instrument of the series of attrs: the
// series of the instrument of b with the same attributes, which with
// creates for a new attribute set.
func (b *bound) with(attrs []telemetry.Attr) *bound {
	sorted := sortedAttrs(attrs)

	b.r.mu.Lock()
	defer b.r.mu.Unlock()

	return &bound{r: b.r, in: b.in, series: b.in.seriesOf(sorted), fromWith: true}
}

// emit keeps a value in the series of b, unless Release ended b. The
// value of b becomes v, or the old value plus v when add is set, and the
// series keeps the new value of b.
func (b *bound) emit(v float64, add bool) {
	b.r.mu.Lock()
	defer b.r.mu.Unlock()

	if b.released {
		return
	}

	if add {
		v += b.value
	}

	b.value = v

	if b.series == nil {
		b.series = b.in.seriesOf(nil)
	}

	b.series.values = append(b.series.values, v)
}

// release ends b when With returned it, so b keeps no value after it.
func (b *bound) release() {
	b.r.mu.Lock()
	defer b.r.mu.Unlock()

	b.released = b.fromWith
}

// seriesOf returns the series of the sorted attribute set attrs, which it
// creates for a new set. The caller holds the mutex of the recorder.
func (in *instrument) seriesOf(attrs []telemetry.Attr) *series {
	for _, s := range in.series {
		if slices.EqualFunc(s.attrs, attrs, sameAttr) {
			return s
		}
	}

	s := &series{attrs: attrs}
	in.series = append(in.series, s)

	return s
}

// sortedAttrs returns a copy of attrs that is sorted by key. Attributes of
// one key keep their order.
func sortedAttrs(attrs []telemetry.Attr) []telemetry.Attr {
	sorted := slices.Clone(attrs)
	slices.SortStableFunc(sorted, func(a, b telemetry.Attr) int { return cmp.Compare(a.Key, b.Key) })

	return sorted
}

// sameAttr reports whether a and b have one key and one value. Two float
// values are the same when they have the same bits, so NaN is the same as
// a NaN of the same bits.
func sameAttr(a, b telemetry.Attr) bool {
	return a.Key == b.Key && a.Value.Kind == b.Value.Kind && a.Value.Str == b.Value.Str &&
		a.Value.Int == b.Value.Int && math.Float64bits(a.Value.Float) == math.Float64bits(b.Value.Float) &&
		a.Value.Bool == b.Value.Bool && bytes.Equal(a.Value.Bytes, b.Value.Bytes)
}
