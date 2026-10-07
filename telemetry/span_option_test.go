// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/telemetry"
)

// remote and other are span contexts that a propagator extracted from
// other processes.
var (
	remote = telemetry.SpanContext{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "00f067aa0ba902b7",
		Sampled: true,
	}
	other = telemetry.SpanContext{
		TraceID: "0af7651916cd43dd8448eb211c80319c",
		SpanID:  "b7ad6b7169203331",
	}
)

// options are the options of the allocation ceilings: a kind and a remote
// parent.
var options = []telemetry.SpanOption{
	telemetry.WithSpanKind(telemetry.SpanKindServer),
	telemetry.WithRemoteParent(remote),
}

func TestSpanOption(t *testing.T) {
	t.Parallel()

	t.Run("ApplySpanOptions", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give []telemetry.SpanOption
			want telemetry.SpanKind
		}{
			{
				name: "returns SpanKindInternal for no options",
				want: telemetry.SpanKindInternal,
			},
			{
				name: "returns SpanKindInternal for an option of SpanKindUnspecified",
				give: []telemetry.SpanOption{telemetry.WithSpanKind(telemetry.SpanKindUnspecified)},
				want: telemetry.SpanKindInternal,
			},
			{
				name: "returns the kind of WithSpanKind",
				give: []telemetry.SpanOption{telemetry.WithSpanKind(telemetry.SpanKindConsumer)},
				want: telemetry.SpanKindConsumer,
			},
			{
				name: "returns the kind of the last option that sets one",
				give: []telemetry.SpanOption{
					telemetry.WithSpanKind(telemetry.SpanKindClient),
					telemetry.WithSpanKind(telemetry.SpanKindServer),
					telemetry.WithSpanKind(telemetry.SpanKindUnspecified),
				},
				want: telemetry.SpanKindServer,
			},
			{
				name: "returns the kind before WithRemoteParent, which sets none",
				give: []telemetry.SpanOption{
					telemetry.WithSpanKind(telemetry.SpanKindServer),
					telemetry.WithRemoteParent(remote),
				},
				want: telemetry.SpanKindServer,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, telemetry.ApplySpanOptions(tt.give), tt.want,
					"ApplySpanOptions must return the kind that the options set")
			})
		}
	})

	t.Run("RemoteParent", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			give   []telemetry.SpanOption
			want   telemetry.SpanContext
			wantOK bool
		}{
			{
				name: "reports false for no options",
			},
			{
				name: "reports false for WithSpanKind, which sets no parent",
				give: []telemetry.SpanOption{telemetry.WithSpanKind(telemetry.SpanKindServer)},
			},
			{
				name: "reports false for a parent with an empty TraceID",
				give: []telemetry.SpanOption{telemetry.WithRemoteParent(telemetry.SpanContext{SpanID: remote.SpanID})},
			},
			{
				name:   "returns the parent of WithRemoteParent",
				give:   []telemetry.SpanOption{telemetry.WithRemoteParent(remote)},
				want:   remote,
				wantOK: true,
			},
			{
				name: "returns the parent of the last option that sets one",
				give: []telemetry.SpanOption{
					telemetry.WithRemoteParent(other),
					telemetry.WithRemoteParent(remote),
					telemetry.WithRemoteParent(telemetry.SpanContext{}),
				},
				want:   remote,
				wantOK: true,
			},
			{
				name: "returns the parent before WithSpanKind, which sets none",
				give: []telemetry.SpanOption{
					telemetry.WithRemoteParent(remote),
					telemetry.WithSpanKind(telemetry.SpanKindServer),
				},
				want:   remote,
				wantOK: true,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := telemetry.RemoteParent(tt.give)
				expect.Equal(t, ok, tt.wantOK, "RemoteParent must report whether an option set a parent")
				expect.Equal(t, got, tt.want, "RemoteParent must return the parent that the options set")
			})
		}
	})
}

// TestSpanOptionAllocs checks the allocation contract of the options and
// of the functions that read them. MaxAllocs counts the allocations of the
// whole process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestSpanOptionAllocs(t *testing.T) {
	var opt telemetry.SpanOption

	t.Run("WithSpanKind", func(t *testing.T) {
		expect.MaxAllocs(t, func() { opt = telemetry.WithSpanKind(telemetry.SpanKindServer) }, 0,
			"WithSpanKind must not allocate")
		assert.Equal(t, telemetry.ApplySpanOptions([]telemetry.SpanOption{opt}), telemetry.SpanKindServer,
			"the test must measure an option of a kind")
	})

	t.Run("WithRemoteParent", func(t *testing.T) {
		expect.MaxAllocs(t, func() { opt = telemetry.WithRemoteParent(remote) }, 0,
			"WithRemoteParent must not allocate")
		_, ok := telemetry.RemoteParent([]telemetry.SpanOption{opt})
		assert.True(t, ok, "the test must measure an option of a parent")
	})

	t.Run("ApplySpanOptions", func(t *testing.T) {
		var kind telemetry.SpanKind
		expect.MaxAllocs(t, func() { kind = telemetry.ApplySpanOptions(options) }, 0,
			"ApplySpanOptions must not allocate")
		assert.Equal(t, kind, telemetry.SpanKindServer, "the test must measure an option that sets a kind")
	})

	t.Run("RemoteParent", func(t *testing.T) {
		var ok bool
		expect.MaxAllocs(t, func() { _, ok = telemetry.RemoteParent(options) }, 0, "RemoteParent must not allocate")
		assert.True(t, ok, "the test must measure an option that sets a parent")
	})
}

// BenchmarkSpanOption reports the cost of the options and of the functions
// that read them, and fails when one allocates.
func BenchmarkSpanOption(b *testing.B) {
	var opt telemetry.SpanOption

	b.Run("WithSpanKind", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			opt = telemetry.WithSpanKind(telemetry.SpanKindServer)
		}

		assert.Equal(b, telemetry.ApplySpanOptions([]telemetry.SpanOption{opt}), telemetry.SpanKindServer,
			"the benchmark must measure an option of a kind")
	})

	b.Run("WithRemoteParent", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			opt = telemetry.WithRemoteParent(remote)
		}

		_, ok := telemetry.RemoteParent([]telemetry.SpanOption{opt})
		assert.True(b, ok, "the benchmark must measure an option of a parent")
	})

	b.Run("ApplySpanOptions", func(b *testing.B) {
		var kind telemetry.SpanKind

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			kind = telemetry.ApplySpanOptions(options)
		}

		assert.Equal(b, kind, telemetry.SpanKindServer, "the benchmark must measure an option that sets a kind")
	})

	b.Run("RemoteParent", func(b *testing.B) {
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, ok = telemetry.RemoteParent(options)
		}

		assert.True(b, ok, "the benchmark must measure an option that sets a parent")
	})
}
