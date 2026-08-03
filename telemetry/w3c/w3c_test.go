// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package w3c_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/w3c"
)

const (
	sampledParent   = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	unsampledParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"

	traceID = telemetry.TraceID("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID  = telemetry.SpanID("00f067aa0ba902b7")
)

func TestExtract(t *testing.T) {
	t.Parallel()

	t.Run("a sampled traceparent", func(t *testing.T) {
		t.Parallel()
		sc, ok := w3c.Propagator{}.Extract(t.Context(),
			telemetry.MapCarrier{"traceparent": sampledParent})

		testkit.True(t, ok, "a well-formed traceparent must extract")
		testkit.Equal(t, sc.TraceID, traceID, "TraceID must come from the trace-id field")
		testkit.Equal(t, sc.SpanID, spanID, "SpanID must come from the parent-id field")
		testkit.True(t, sc.Sampled, "the sampled flag must be read from the flags byte")
	})

	t.Run("an unsampled traceparent", func(t *testing.T) {
		t.Parallel()
		sc, ok := w3c.Propagator{}.Extract(t.Context(),
			telemetry.MapCarrier{"traceparent": unsampledParent})

		testkit.True(t, ok, "a well-formed traceparent must extract")
		testkit.False(t, sc.Sampled, "an unset flags bit must not report sampled")
	})

	t.Run("tracestate is carried verbatim", func(t *testing.T) {
		t.Parallel()
		const state = "vendor1=opaque,vendor2=also-opaque"
		sc, ok := w3c.Propagator{}.Extract(t.Context(), telemetry.MapCarrier{
			"traceparent": sampledParent,
			"tracestate":  state,
		})

		testkit.True(t, ok, "extraction must succeed")
		testkit.Equal(t, sc.TraceState, state, "tracestate must be preserved exactly")
	})

	t.Run("tracestate without traceparent is ignored", func(t *testing.T) {
		t.Parallel()
		// tracestate alone carries no trace identity, so there is
		// nothing to continue.
		_, ok := w3c.Propagator{}.Extract(t.Context(),
			telemetry.MapCarrier{"tracestate": "vendor=value"})
		testkit.False(t, ok, "tracestate alone must not produce a span context")
	})

	// A malformed traceparent is treated as absent, not as an error:
	// the specification requires the receiver to start a new trace
	// rather than reject the request.
	malformed := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"too few fields", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7"},
		{"too many fields", sampledParent + "-extra"},
		{"short trace id", "00-4bf92f3577b34da6a3ce929-00f067aa0ba902b7-01"},
		{"short span id", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa-01"},
		{"non-hex trace id", "00-ZZf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
		{"non-hex span id", "00-4bf92f3577b34da6a3ce929d0e0e4736-ZZf067aa0ba902b7-01"},
		{"non-hex flags", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-ZZ"},
		{"uppercase trace id", "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01"},
		{"all-zero trace id", "00-00000000000000000000000000000000-00f067aa0ba902b7-01"},
		{"all-zero span id", "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01"},
		{"version ff is forbidden", "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
		{"non-hex version", "zz-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
		{"short version", "0-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
		// Correct length, wrong separators — these reach the
		// delimiter check rather than being caught by the length
		// test, which is the only way that branch is exercised.
		{"underscore after version", "00_4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
		{"underscore after trace id", "00-4bf92f3577b34da6a3ce929d0e0e4736_00f067aa0ba902b7-01"},
		{"underscore after span id", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7_01"},
		// A later version may append fields, but they must be
		// hyphen-separated; junk appended directly is malformed at
		// any version.
		{
			"later version with unseparated trailing data",
			"cc-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01junk",
		},
	}
	for _, tc := range malformed {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			t.Parallel()
			_, ok := w3c.Propagator{}.Extract(t.Context(),
				telemetry.MapCarrier{"traceparent": tc.value})
			testkit.False(t, ok, "a malformed traceparent must be treated as absent")
		})
	}

	// The sampled bit is bit 0 of the flags byte, so the whole byte
	// must be decoded rather than compared against "01". Values with
	// other bits set, and with hex letters, both occur in practice.
	flags := []struct {
		flags   string
		sampled bool
	}{
		{"03", true},
		{"09", true},
		{"0f", true},
		{"ff", true},
		{"02", false},
		{"0a", false},
		{"fe", false},
	}
	for _, tc := range flags {
		t.Run("reads the sampled bit from flags "+tc.flags, func(t *testing.T) {
			t.Parallel()
			sc, ok := w3c.Propagator{}.Extract(t.Context(), telemetry.MapCarrier{
				"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-" + tc.flags,
			})

			testkit.True(t, ok, "a well-formed traceparent must extract")
			testkit.Equal(t, sc.Sampled, tc.sampled, "only bit 0 of the flags byte is sampled")
		})
	}

	t.Run("a future version with extra fields is accepted", func(t *testing.T) {
		t.Parallel()
		// Forward compatibility: the specification requires a version
		// higher than the one understood to be parsed for the fields
		// that are understood, not rejected.
		sc, ok := w3c.Propagator{}.Extract(t.Context(), telemetry.MapCarrier{
			"traceparent": "cc-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-future",
		})

		testkit.True(t, ok, "a higher version must still yield the fields we understand")
		testkit.Equal(t, sc.TraceID, traceID, "TraceID must still be read")
		testkit.True(t, sc.Sampled, "the flags byte must still be read")
	})
}

func TestInject(t *testing.T) {
	t.Parallel()

	t.Run("writes a well-formed traceparent", func(t *testing.T) {
		t.Parallel()
		c := telemetry.MapCarrier{}
		w3c.Propagator{}.Inject(t.Context(), telemetry.SpanContext{
			TraceID: traceID,
			SpanID:  spanID,
			Sampled: true,
		}, c)

		testkit.Equal(t, c["traceparent"], sampledParent, "traceparent must match the grammar")
	})

	t.Run("an unsampled context sets the flags byte to zero", func(t *testing.T) {
		t.Parallel()
		c := telemetry.MapCarrier{}
		w3c.Propagator{}.Inject(t.Context(), telemetry.SpanContext{
			TraceID: traceID,
			SpanID:  spanID,
		}, c)

		testkit.Equal(t, c["traceparent"], unsampledParent, "an unsampled trace must write 00")
	})

	t.Run("tracestate is written when present", func(t *testing.T) {
		t.Parallel()
		c := telemetry.MapCarrier{}
		w3c.Propagator{}.Inject(t.Context(), telemetry.SpanContext{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceState: "vendor=opaque",
		}, c)

		testkit.Equal(t, c["tracestate"], "vendor=opaque", "tracestate must be written verbatim")
	})

	t.Run("an empty tracestate writes no header", func(t *testing.T) {
		t.Parallel()
		// An empty tracestate header is not the same as none, and
		// writing one would add a field the caller never had.
		c := telemetry.MapCarrier{}
		w3c.Propagator{}.Inject(t.Context(), telemetry.SpanContext{
			TraceID: traceID,
			SpanID:  spanID,
		}, c)

		_, present := c["tracestate"]
		testkit.False(t, present, "an empty tracestate must not be written")
	})

	invalid := []struct {
		name string
		sc   telemetry.SpanContext
	}{
		{"zero context", telemetry.SpanContext{}},
		{"missing span id", telemetry.SpanContext{TraceID: traceID}},
		{"missing trace id", telemetry.SpanContext{SpanID: spanID}},
		{"short trace id", telemetry.SpanContext{TraceID: "abcd", SpanID: spanID}},
		{"non-hex trace id", telemetry.SpanContext{
			TraceID: "ZZf92f3577b34da6a3ce929d0e0e4736", SpanID: spanID,
		}},
	}
	for _, tc := range invalid {
		t.Run("writes nothing for a "+tc.name, func(t *testing.T) {
			t.Parallel()
			// TraceID and SpanID are opaque in the seam, so a context
			// from a non-W3C tracer may not be representable. Writing
			// a malformed header would be worse than writing none:
			// the receiver would reject or restart the trace either
			// way, and a malformed one also corrupts the carrier.
			c := telemetry.MapCarrier{}
			w3c.Propagator{}.Inject(t.Context(), tc.sc, c)
			testkit.Equal(t, len(c), 0, "an unrepresentable context must write no headers")
		})
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	for _, sampled := range []bool{true, false} {
		name := "unsampled"
		if sampled {
			name = "sampled"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want := telemetry.SpanContext{
				TraceID:    traceID,
				SpanID:     spanID,
				Sampled:    sampled,
				TraceState: "vendor=opaque",
			}

			c := telemetry.MapCarrier{}
			w3c.Propagator{}.Inject(t.Context(), want, c)
			got, ok := w3c.Propagator{}.Extract(t.Context(), c)

			testkit.True(t, ok, "injected headers must extract")
			testkit.Equal(t, got, want, "the round trip must preserve every propagated field")
		})
	}

	t.Run("ParentID and Kind do not survive, by design", func(t *testing.T) {
		t.Parallel()
		// W3C carries neither. The extracted context describes the
		// remote parent, so its own ParentID and Kind are the
		// receiver's to decide.
		c := telemetry.MapCarrier{}
		w3c.Propagator{}.Inject(t.Context(), telemetry.SpanContext{
			TraceID:  traceID,
			SpanID:   spanID,
			ParentID: telemetry.SpanID("ffffffffffffffff"),
			Kind:     telemetry.SpanKindClient,
		}, c)

		got, ok := w3c.Propagator{}.Extract(t.Context(), c)
		testkit.True(t, ok, "extraction must succeed")
		testkit.Equal(t, got.ParentID, telemetry.SpanID(""), "ParentID is not propagated")
		testkit.Equal(t, got.Kind, telemetry.SpanKindUnspecified, "Kind is not propagated")
	})
}

func TestFields(t *testing.T) {
	t.Parallel()

	fields := w3c.Propagator{}.Fields()
	testkit.Equal(t, fields, []string{"traceparent", "tracestate"},
		"Fields must name every header Inject writes, so middleware can clear them")

	t.Run("the returned slice is not shared", func(t *testing.T) {
		t.Parallel()
		// Middleware iterates and may sort or truncate; a shared
		// backing array would corrupt every later caller.
		first := w3c.Propagator{}.Fields()
		first[0] = "mutated"
		testkit.Equal(t, w3c.Propagator{}.Fields()[0], "traceparent",
			"mutating the result must not affect later calls")
	})
}

func BenchmarkExtract(b *testing.B) {
	c := telemetry.MapCarrier{"traceparent": sampledParent}
	p := w3c.Propagator{}
	b.ReportAllocs()

	var sink telemetry.SpanContext
	for b.Loop() {
		sink, _ = p.Extract(b.Context(), c)
	}
	testkit.NotEqual(b, sink.TraceID, telemetry.TraceID(""), "Extract must produce a context")
}

func BenchmarkInject(b *testing.B) {
	sc := telemetry.SpanContext{TraceID: traceID, SpanID: spanID, Sampled: true}
	c := telemetry.MapCarrier{}
	p := w3c.Propagator{}
	b.ReportAllocs()

	for b.Loop() {
		p.Inject(b.Context(), sc, c)
	}
	testkit.NotEqual(b, c["traceparent"], "", "Inject must write a header")
}
