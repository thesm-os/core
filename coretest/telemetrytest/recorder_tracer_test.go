// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest_test

import (
	"context"
	"errors"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/telemetry"
)

// The names of the spans of the cases.
const (
	tracerName telemetry.InstrumentName = "test.library"
	spanName   telemetry.SpanName       = "test.operation"
	childName  telemetry.SpanName       = "test.child"
	eventName  telemetry.EventName      = "test.event"
)

// remote is a remote parent of the cases, as a propagator extracts it.
var remote = telemetry.SpanContext{
	TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
	SpanID:     "00f067aa0ba902b7",
	TraceState: "vendor=1",
	Sampled:    true,
}

func TestRecorderTracerContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertTracerContract(t, func() telemetry.Tracer {
		return telemetrytest.NewRecorder().Tracer(tracerName)
	}, telemetrytest.TracerContractAssertions()...)
}

func TestRecorderSpanContract(t *testing.T) {
	t.Parallel()
	telemetrytest.AssertSpanContract(t, func() telemetry.Span {
		_, s := telemetrytest.NewRecorder().Tracer(tracerName).Start(t.Context(), spanName)

		return s
	}, telemetrytest.SpanContractAssertions()...)
}

func TestRecorderTracer(t *testing.T) {
	t.Parallel()

	t.Run("Start", func(t *testing.T) {
		t.Parallel()

		t.Run("starts a span that the Recorder keeps with its name and its error once it ends", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			_, s := r.Tracer(tracerName).Start(t.Context(), spanName)
			fault := errors.New("the operation failed")
			assert.Empty(t, r.Spans(), "the Recorder must keep no span before it ends")
			s.End(fault)
			spans := r.Spans()
			assert.Length(t, spans, 1, "the Recorder must keep the span that ended")
			expect.Equal(t, spans[0].Name, spanName, "the span must have the name of Start")
			expect.Equal(t, spans[0].Err, fault, "the span must have the error of End", assert.ByIdentity())
		})

		t.Run("starts a span of the kind that its options set", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			_, s := r.Tracer(tracerName).Start(t.Context(), spanName, telemetry.WithSpanKind(telemetry.SpanKindServer))
			s.End(nil)
			expect.Equal(t, r.Spans()[0].Kind, telemetry.SpanKindServer, "the span must have the kind of its option")
			expect.Equal(t, s.SpanContext().Kind, telemetry.SpanKindServer,
				"the identity must have the kind of the span")
		})

		t.Run("starts a span of SpanKindInternal without a kind option", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			_, s := r.Tracer(tracerName).Start(t.Context(), spanName)
			s.End(nil)
			assert.Equal(t, r.Spans()[0].Kind, telemetry.SpanKindInternal,
				"a span without a kind option must be internal")
		})

		t.Run("starts a span of a new trace without a parent", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			_, a := r.Tracer(tracerName).Start(t.Context(), spanName)
			_, b := r.Tracer(tracerName).Start(t.Context(), spanName)
			expect.NotEqual(t, a.SpanContext().TraceID, b.SpanContext().TraceID, "each root span must start a trace")
			expect.NotEqual(t, a.SpanContext().SpanID, b.SpanContext().SpanID,
				"each span must have a span ID of its own")
			expect.Equal(t, a.SpanContext().ParentID, telemetry.SpanID(""), "a root span must have no parent")
			expect.True(t, a.SpanContext().Sampled, "a recorded span must be sampled")
		})

		t.Run("starts a span under the span of its context", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			ctx, parent := r.Tracer(tracerName).Start(t.Context(), spanName)
			_, child := r.Tracer(tracerName).Start(ctx, childName)
			child.End(nil)
			parent.End(nil)
			spans := r.Spans()
			assert.Length(t, spans, 2, "the Recorder must keep both spans")
			expect.Equal(t, spans[0].Parent, parent.SpanContext(),
				"the child must have the span of its context as parent")
			expect.Equal(t, spans[0].Context.TraceID, parent.SpanContext().TraceID,
				"the child must continue the trace of its parent")
			expect.Equal(t, spans[0].Context.ParentID, parent.SpanContext().SpanID,
				"the identity of the child must name its parent")
		})

		t.Run("starts a span under the remote parent of its options", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			ctx, local := r.Tracer(tracerName).Start(t.Context(), spanName)
			_, s := r.Tracer(tracerName).Start(ctx, childName, telemetry.WithRemoteParent(remote))
			s.End(nil)
			local.End(nil)
			span := r.Spans()[0]
			expect.Equal(t, span.Parent, remote, "the span must have the remote parent of its option")
			expect.Equal(t, span.Context.TraceID, remote.TraceID, "the span must continue the remote trace")
			expect.Equal(t, span.Context.TraceState, remote.TraceState, "the span must carry the trace state")
		})

		t.Run("returns a nil context unchanged", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			//nolint:staticcheck // the case passes a nil context, which the contract of a Tracer tolerates
			ctx, s := r.Tracer(tracerName).Start(nil, spanName)
			s.End(nil)
			expect.Nil(t, ctx, "Start must return a nil context unchanged")
			expect.Length(t, r.Spans(), 1, "the Recorder must keep the span of a nil context")
		})

		t.Run("returns a context with the span", func(t *testing.T) {
			t.Parallel()
			type key struct{}
			r := telemetrytest.NewRecorder()
			ctx, s := r.Tracer(tracerName).Start(context.WithValue(t.Context(), key{}, "value"), spanName)
			s.End(nil)
			assert.Equal(t, ctx.Value(key{}), any("value"), "the context must derive from the context of Start")
		})
	})

	t.Run("End", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the error of the first End", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			_, s := r.Tracer(tracerName).Start(t.Context(), spanName)
			s.End(nil)
			s.End(errors.New("a second end"))
			spans := r.Spans()
			assert.Length(t, spans, 1, "a second End must not keep the span again")
			assert.Nil(t, spans[0].Err, "the span must keep the error of the first End")
		})
	})

	t.Run("SetAttributes", func(t *testing.T) {
		t.Parallel()

		t.Run("adds the attributes of each call before End in order", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			_, s := r.Tracer(tracerName).Start(t.Context(), spanName)
			s.SetAttributes([]telemetry.Attr{region})
			s.SetAttributes([]telemetry.Attr{tier})
			s.End(nil)
			s.SetAttributes([]telemetry.Attr{telemetry.AttrBool("late", true)})
			assert.Equal(t, r.Spans()[0].Attributes, []telemetry.Attr{region, tier},
				"the span must keep the attributes before End")
		})
	})

	t.Run("AddEvent", func(t *testing.T) {
		t.Parallel()

		t.Run("adds each event before End in order", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			_, s := r.Tracer(tracerName).Start(t.Context(), spanName)
			s.AddEvent(eventName, []telemetry.Attr{region})
			s.AddEvent(eventName, nil)
			s.End(nil)
			s.AddEvent("late", nil)
			assert.Equal(t, r.Spans()[0].Events, []telemetrytest.Event{
				{Name: eventName, Attributes: []telemetry.Attr{region}},
				{Name: eventName},
			}, "the span must keep the events before End")
		})
	})

	t.Run("SpanContext", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the identity that the Recorder keeps", func(t *testing.T) {
			t.Parallel()
			r := telemetrytest.NewRecorder()
			_, s := r.Tracer(tracerName).Start(t.Context(), spanName)
			s.End(nil)
			assert.Equal(t, s.SpanContext(), r.Spans()[0].Context, "the span must keep the identity of SpanContext")
		})
	})
}
