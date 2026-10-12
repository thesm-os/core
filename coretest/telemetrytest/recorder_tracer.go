// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"slices"

	"go.thesmos.sh/core/telemetry"
)

// Span is a span that ended in a [Recorder], with what its Start and its
// methods received. A caller of [Recorder.Spans] does not change its
// slices.
type Span struct {
	// Err is the error that the first End of the span received.
	Err error

	// Name is the name that Start received.
	Name telemetry.SpanName

	// Context is the identity that SpanContext returned. A span without a
	// parent starts a new trace. Every span has a new SpanID.
	Context telemetry.SpanContext

	// Parent is the identity of the parent of the span: the remote parent
	// that [telemetry.WithRemoteParent] set, or else the span of the
	// context that Start received. It is zero for a span without a parent.
	Parent telemetry.SpanContext

	// Attributes are the attributes of every SetAttributes before End, in
	// order.
	Attributes []telemetry.Attr

	// Events are the events of every AddEvent before End, in order.
	Events []Event

	// Kind is the kind that the options of Start set, and
	// [telemetry.SpanKindInternal] when no option sets one.
	Kind telemetry.SpanKind
}

// Event is an event of a [Span]: the name and the attributes that AddEvent
// received.
type Event struct {
	Name       telemetry.EventName
	Attributes []telemetry.Attr
}

// tracer is the [telemetry.Tracer] of a [Recorder].
type tracer struct {
	r *Recorder
}

var _ telemetry.Tracer = tracer{}

// recordingSpan is a span that a tracer of a [Recorder] started. Its
// methods are for one goroutine, as the contract of [telemetry.Span]
// states.
type recordingSpan struct {
	r     *Recorder
	span  Span
	ended bool
}

var _ telemetry.Span = (*recordingSpan)(nil)

// spanKey is the key of the span in the context that Start returns.
type spanKey struct{}

// Tracer returns a tracer whose spans r keeps once they end. The name of
// the tracer does not change its spans.
//
// # Allocation contract
//
// Zero alloc.
func (r *Recorder) Tracer(telemetry.InstrumentName) telemetry.Tracer {
	return tracer{r: r}
}

// Start starts a span of name, and returns a context with the span. The
// span has the kind that opts set, and a parent: the remote parent that
// opts set, or else the span of ctx. The span continues the trace of its
// parent, and a span without a parent starts a new trace. The identity of
// the span derives from the number of the span in the Recorder, so the
// identities of a test do not change from one run to the next. Start
// returns a nil ctx unchanged.
//
// # Allocation contract
//
// Start allocates the span, its identity and the context that it returns.
func (t tracer) Start(
	ctx context.Context,
	name telemetry.SpanName,
	opts ...telemetry.SpanOption,
) (context.Context, telemetry.Span) {
	parent, remote := telemetry.RemoteParent(opts)
	if !remote && ctx != nil {
		if p, ok := ctx.Value(spanKey{}).(*recordingSpan); ok {
			parent = p.span.Context
		}
	}

	t.r.mu.Lock()
	t.r.spanIDs++
	n := t.r.spanIDs
	t.r.mu.Unlock()

	// The number of the span gives the low 8 bytes of a new trace ID and
	// the 8 bytes of the span ID.
	var id [16]byte
	binary.BigEndian.PutUint64(id[8:], n)

	traceID := parent.TraceID
	if traceID == "" {
		traceID = telemetry.TraceID(hex.EncodeToString(id[:]))
	}

	kind := telemetry.ApplySpanOptions(opts)
	s := &recordingSpan{r: t.r, span: Span{
		Name:   name,
		Kind:   kind,
		Parent: parent,
		Context: telemetry.SpanContext{
			TraceID:    traceID,
			SpanID:     telemetry.SpanID(hex.EncodeToString(id[8:])),
			ParentID:   parent.SpanID,
			TraceState: parent.TraceState,
			Kind:       kind,
			Sampled:    true,
		},
	}}

	if ctx == nil {
		return nil, s
	}

	return context.WithValue(ctx, spanKey{}, s), s
}

// End ends s with err, and gives s to the Recorder. A second End does
// nothing.
func (s *recordingSpan) End(err error) {
	if s.ended {
		return
	}

	s.ended = true
	s.span.Err = err

	s.r.mu.Lock()
	defer s.r.mu.Unlock()

	s.r.spans = append(s.r.spans, s.span)
}

// SetAttributes adds attrs to the attributes of s, until End.
func (s *recordingSpan) SetAttributes(attrs []telemetry.Attr) {
	if !s.ended {
		s.span.Attributes = append(s.span.Attributes, attrs...)
	}
}

// AddEvent adds an event of name with a copy of attrs to s, until End.
func (s *recordingSpan) AddEvent(name telemetry.EventName, attrs []telemetry.Attr) {
	if !s.ended {
		s.span.Events = append(s.span.Events, Event{Name: name, Attributes: slices.Clone(attrs)})
	}
}

// SpanContext returns the identity of s.
func (s *recordingSpan) SpanContext() telemetry.SpanContext {
	return s.span.Context
}
