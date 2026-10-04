// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"testing"

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

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

				testkit.Equal(t, telemetry.ApplySpanOptions(tt.give), tt.want, "ApplySpanOptions")
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
				testkit.Equal(t, ok, tt.wantOK, "RemoteParent must report whether an option set a parent")
				testkit.Equal(t, got, tt.want, "RemoteParent")
			})
		}
	})
}

func BenchmarkSpanOption(b *testing.B) {
	opts := []telemetry.SpanOption{
		telemetry.WithSpanKind(telemetry.SpanKindServer),
		telemetry.WithRemoteParent(remote),
	}

	b.Run("ApplySpanOptions", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var kind telemetry.SpanKind
		for c.Loop() {
			kind = telemetry.ApplySpanOptions(opts)
		}

		testkit.Equal(b, kind, telemetry.SpanKindServer, "the benchmark must measure an option that sets a kind")
	})

	b.Run("RemoteParent", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var ok bool
		for c.Loop() {
			_, ok = telemetry.RemoteParent(opts)
		}

		testkit.True(b, ok, "the benchmark must measure an option that sets a parent")
	})
}
