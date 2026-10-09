// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package w3c_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/w3c"
)

// The fixture values of the cases.
const (
	// prefix is the version, the trace-id and the parent-id of a
	// traceparent of traceID and spanID, without the flags.
	prefix = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-"

	// sampledParent and unsampledParent are the traceparents of traceID and
	// spanID with the sampled flag set and clear.
	sampledParent   = prefix + "01"
	unsampledParent = prefix + "00"

	traceID = telemetry.TraceID("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID  = telemetry.SpanID("00f067aa0ba902b7")

	// state is a tracestate of two vendors.
	state = "vendor1=opaque,vendor2=also-opaque"
)

// errNotExtracted is returned by the inverse of the round trip for a
// carrier from which Extract reads no span context.
var errNotExtracted = errors.New("the carrier contains no span context")

// The generators of the properties.
var (
	// traceIDs generates trace IDs of the W3C form: 32 lowercase hex
	// digits, not all of them zero.
	traceIDs = prop.StringMatching(`[0-9a-f]{32}`).Filter(func(s string) bool { return strings.Trim(s, "0") != "" })

	// spanIDs generates span IDs of the W3C form: 16 lowercase hex digits,
	// not all of them zero.
	spanIDs = prop.StringMatching(`[0-9a-f]{16}`).Filter(func(s string) bool { return strings.Trim(s, "0") != "" })

	// contexts generates the span contexts that Trace Context propagates:
	// an identity of the W3C form, the sampled flag and any tracestate.
	contexts = prop.Composite(func(c *prop.Case) telemetry.SpanContext {
		return telemetry.SpanContext{
			TraceID:    telemetry.TraceID(c.Draw(traceIDs, "trace ID")),
			SpanID:     telemetry.SpanID(c.Draw(spanIDs, "span ID")),
			Sampled:    c.Draw(prop.Boolean(), "sampled"),
			TraceState: c.Draw(prop.String(), "trace state"),
		}
	})
)

func TestPropagator(t *testing.T) {
	t.Parallel()

	t.Run("Inject", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			want telemetry.MapCarrier
			name string
			give telemetry.SpanContext
		}{
			{
				name: "writes the traceparent of a sampled context",
				give: telemetry.SpanContext{TraceID: traceID, SpanID: spanID, Sampled: true},
				want: telemetry.MapCarrier{w3c.TraceParentHeader: sampledParent},
			},
			{
				name: "writes the flags 00 for a context that is not sampled",
				give: telemetry.SpanContext{TraceID: traceID, SpanID: spanID},
				want: telemetry.MapCarrier{w3c.TraceParentHeader: unsampledParent},
			},
			{
				name: "writes the tracestate of the context",
				give: telemetry.SpanContext{TraceID: traceID, SpanID: spanID, TraceState: state},
				want: telemetry.MapCarrier{w3c.TraceParentHeader: unsampledParent, w3c.TraceStateHeader: state},
			},
			{
				name: "writes no ParentID",
				give: telemetry.SpanContext{TraceID: traceID, SpanID: spanID, ParentID: "ffffffffffffffff"},
				want: telemetry.MapCarrier{w3c.TraceParentHeader: unsampledParent},
			},
			{
				name: "writes no Kind",
				give: telemetry.SpanContext{TraceID: traceID, SpanID: spanID, Kind: telemetry.SpanKindClient},
				want: telemetry.MapCarrier{w3c.TraceParentHeader: unsampledParent},
			},
			{
				name: "writes nothing for the zero context",
				want: telemetry.MapCarrier{},
			},
			{
				name: "writes nothing for a short trace id",
				give: telemetry.SpanContext{TraceID: "4bf92f3577b34da6", SpanID: spanID},
				want: telemetry.MapCarrier{},
			},
			{
				name: "writes nothing for a trace id outside hex",
				give: telemetry.SpanContext{TraceID: "ZZf92f3577b34da6a3ce929d0e0e4736", SpanID: spanID},
				want: telemetry.MapCarrier{},
			},
			{
				name: "writes nothing for an all-zero trace id",
				give: telemetry.SpanContext{TraceID: "00000000000000000000000000000000", SpanID: spanID},
				want: telemetry.MapCarrier{},
			},
			{
				name: "writes nothing for a context without a span id",
				give: telemetry.SpanContext{TraceID: traceID},
				want: telemetry.MapCarrier{},
			},
			{
				name: "writes nothing for a short span id",
				give: telemetry.SpanContext{TraceID: traceID, SpanID: "00f067aa"},
				want: telemetry.MapCarrier{},
			},
			{
				name: "writes nothing for a span id outside hex",
				give: telemetry.SpanContext{TraceID: traceID, SpanID: "ZZf067aa0ba902b7"},
				want: telemetry.MapCarrier{},
			},
			{
				name: "writes nothing for an all-zero span id",
				give: telemetry.SpanContext{TraceID: traceID, SpanID: "0000000000000000"},
				want: telemetry.MapCarrier{},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := telemetry.MapCarrier{}
				w3c.Propagator{}.Inject(t.Context(), tt.give, c)
				assert.Equal(t, c, tt.want, "Inject must write the headers of the context")
			})
		}
	})

	t.Run("Extract", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the span context of a traceparent", func(t *testing.T) {
			t.Parallel()
			sc, ok := w3c.Propagator{}.Extract(t.Context(), telemetry.MapCarrier{w3c.TraceParentHeader: sampledParent})
			assert.True(t, ok, "Extract must read a traceparent of the grammar")
			assert.Equal(t, sc, telemetry.SpanContext{TraceID: traceID, SpanID: spanID, Sampled: true},
				"Extract must return the fields of the traceparent")
		})

		t.Run("reads the sampled flag from bit 0 of the flags byte", func(t *testing.T) {
			t.Parallel()
			for flags := range 256 {
				header := fmt.Sprintf("%s%02x", prefix, flags)
				sc, ok := w3c.Propagator{}.Extract(t.Context(), telemetry.MapCarrier{w3c.TraceParentHeader: header})
				expect.True(t, ok, "Extract must read the traceparent "+header)
				expect.Equal(t, sc.Sampled, flags&1 == 1, "Extract must read bit 0 of the flags of "+header)
			}
		})

		t.Run("returns the tracestate of a valid traceparent", func(t *testing.T) {
			t.Parallel()
			sc, ok := w3c.Propagator{}.Extract(t.Context(), telemetry.MapCarrier{
				w3c.TraceParentHeader: sampledParent,
				w3c.TraceStateHeader:  state,
			})
			assert.True(t, ok, "Extract must read a traceparent of the grammar")
			assert.Equal(t, sc.TraceState, state, "Extract must return the tracestate unchanged")
		})

		t.Run("reports false for a tracestate without a traceparent", func(t *testing.T) {
			t.Parallel()
			_, ok := w3c.Propagator{}.Extract(t.Context(), telemetry.MapCarrier{w3c.TraceStateHeader: state})
			assert.False(t, ok, "a tracestate alone must not produce a span context")
		})

		t.Run("returns the fields of version 00 of a later version", func(t *testing.T) {
			t.Parallel()
			sc, ok := w3c.Propagator{}.Extract(t.Context(), telemetry.MapCarrier{
				w3c.TraceParentHeader: "cc-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-future",
			})
			assert.True(t, ok, "Extract must read a later version that appends a field")
			assert.Equal(t, sc, telemetry.SpanContext{TraceID: traceID, SpanID: spanID, Sampled: true},
				"Extract must return the fields of version 00")
		})

		t.Run("returns the context that Inject writes", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(sc telemetry.SpanContext) (telemetry.MapCarrier, error) {
				c := telemetry.MapCarrier{}
				w3c.Propagator{}.Inject(t.Context(), sc, c)

				return c, nil
			}, func(c telemetry.MapCarrier) (telemetry.SpanContext, error) {
				sc, ok := w3c.Propagator{}.Extract(t.Context(), c)
				if !ok {
					return sc, errNotExtracted
				}

				return sc, nil
			}, "Extract must return every field that Inject writes", prop.Using(contexts))
		})

		malformed := []struct {
			name string
			give string
		}{
			{name: "reports false for an empty traceparent"},
			{
				name: "reports false for a traceparent without flags",
				give: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7",
			},
			{name: "reports false for version 00 with a field after the flags", give: sampledParent + "-extra"},
			{name: "reports false for a short trace id", give: "00-4bf92f3577b34da6a3ce929-00f067aa0ba902b7-01"},
			{name: "reports false for a short span id", give: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa-01"},
			{
				name: "reports false for a trace id outside hex",
				give: "00-ZZf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			},
			{
				name: "reports false for a trace id with a byte below 0",
				give: "00-4bf92f3577b34da6a3ce929d0e0e47.6-00f067aa0ba902b7-01",
			},
			{
				name: "reports false for a trace id in upper case",
				give: "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01",
			},
			{
				name: "reports false for an all-zero trace id",
				give: "00-00000000000000000000000000000000-00f067aa0ba902b7-01",
			},
			{
				name: "reports false for a span id outside hex",
				give: "00-4bf92f3577b34da6a3ce929d0e0e4736-ZZf067aa0ba902b7-01",
			},
			{
				name: "reports false for an all-zero span id",
				give: "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
			},
			{name: "reports false for flags outside hex", give: prefix + "ZZ"},
			{name: "reports false for version ff", give: "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
			{
				name: "reports false for a version outside hex",
				give: "zz-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			},
			{name: "reports false for a short version", give: "0-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
			{
				name: "reports false for an underscore after the version",
				give: "00_4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			},
			{
				name: "reports false for an underscore after the trace id",
				give: "00-4bf92f3577b34da6a3ce929d0e0e4736_00f067aa0ba902b7-01",
			},
			{
				name: "reports false for an underscore after the span id",
				give: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7_01",
			},
			{
				name: "reports false for a later version with data after the flags without a hyphen",
				give: "cc-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01junk",
			},
		}
		for _, tt := range malformed {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := w3c.Propagator{}.Extract(t.Context(), telemetry.MapCarrier{w3c.TraceParentHeader: tt.give})
				assert.False(t, ok, "Extract must treat a malformed traceparent as absent")
			})
		}
	})

	t.Run("Fields", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the headers that Inject writes", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, w3c.Propagator{}.Fields(), []string{w3c.TraceParentHeader, w3c.TraceStateHeader},
				"Fields must name every header that Inject writes")
		})

		t.Run("returns a slice that a later call does not share", func(t *testing.T) {
			t.Parallel()
			first := w3c.Propagator{}.Fields()
			first[0] = state
			assert.Equal(t, w3c.Propagator{}.Fields()[0], w3c.TraceParentHeader,
				"a change of one result must not reach a later call")
		})
	})
}

// TestPropagatorAllocs checks the allocation contract of each method.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestPropagatorAllocs(t *testing.T) {
	ctx := t.Context()

	t.Run("Inject", func(t *testing.T) {
		sc := telemetry.SpanContext{TraceID: traceID, SpanID: spanID, Sampled: true}
		c := telemetry.MapCarrier{}
		expect.MaxAllocs(t, func() { w3c.Propagator{}.Inject(ctx, sc, c) }, 1,
			"Inject must allocate the traceparent value alone")
		assert.Equal(t, c[w3c.TraceParentHeader], sampledParent, "the test must measure a written traceparent")
	})

	t.Run("Extract", func(t *testing.T) {
		c := telemetry.MapCarrier{w3c.TraceParentHeader: sampledParent}

		var got telemetry.SpanContext
		expect.MaxAllocs(t, func() { got, _ = w3c.Propagator{}.Extract(ctx, c) }, 0, "Extract must not allocate")
		assert.Equal(t, got.TraceID, traceID, "the test must measure an extracted context")
	})

	t.Run("Fields", func(t *testing.T) {
		var got []string
		expect.MaxAllocs(t, func() { got = w3c.Propagator{}.Fields() }, 1, "Fields must allocate its slice alone")
		assert.Length(t, got, 2, "the test must measure the two headers")
	})
}

// BenchmarkPropagator reports the cost of each method, and fails above the
// allocations that their contracts state.
func BenchmarkPropagator(b *testing.B) {
	ctx := b.Context()

	b.Run("Inject", func(b *testing.B) {
		sc := telemetry.SpanContext{TraceID: traceID, SpanID: spanID, Sampled: true}
		c := telemetry.MapCarrier{}

		bc := bench.Start(b).MaxAllocs(1)
		defer bc.End()

		for bc.Loop() {
			w3c.Propagator{}.Inject(ctx, sc, c)
		}

		assert.Equal(b, c[w3c.TraceParentHeader], sampledParent, "the benchmark must measure a written traceparent")
	})

	b.Run("Extract", func(b *testing.B) {
		c := telemetry.MapCarrier{w3c.TraceParentHeader: sampledParent}

		var got telemetry.SpanContext

		bc := bench.Start(b).MaxAllocs(0)
		defer bc.End()

		for bc.Loop() {
			got, _ = w3c.Propagator{}.Extract(ctx, c)
		}

		assert.Equal(b, got.TraceID, traceID, "the benchmark must measure an extracted context")
	})

	b.Run("Fields", func(b *testing.B) {
		var got []string

		bc := bench.Start(b).MaxAllocs(1)
		defer bc.End()

		for bc.Loop() {
			got = w3c.Propagator{}.Fields()
		}

		assert.Length(b, got, 2, "the benchmark must measure the two headers")
	})
}
