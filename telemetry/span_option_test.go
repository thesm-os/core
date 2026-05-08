// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
)

func TestApplySpanOptions(t *testing.T) {
	t.Parallel()

	t.Run("no options yields default SpanKindInternal", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, telemetry.ApplySpanOptions(nil), telemetry.SpanKindInternal,
			"ApplySpanOptions(nil) must yield SpanKindInternal")
	})

	t.Run("explicit SpanKindUnspecified falls through to default", func(t *testing.T) {
		t.Parallel()
		got := telemetry.ApplySpanOptions([]telemetry.SpanOption{
			telemetry.WithSpanKind(telemetry.SpanKindUnspecified),
		})
		testkit.Equal(t, got, telemetry.SpanKindInternal,
			"explicit Unspecified must fall through to SpanKindInternal")
	})

	cases := map[string]telemetry.SpanKind{
		"internal": telemetry.SpanKindInternal,
		"server":   telemetry.SpanKindServer,
		"client":   telemetry.SpanKindClient,
		"producer": telemetry.SpanKindProducer,
		"consumer": telemetry.SpanKindConsumer,
	}
	for name, kind := range cases {
		t.Run("WithSpanKind/"+name, func(t *testing.T) {
			t.Parallel()
			got := telemetry.ApplySpanOptions([]telemetry.SpanOption{
				telemetry.WithSpanKind(kind),
			})
			testkit.Equal(t, got, kind, "ApplySpanOptions must reflect the configured SpanKind")
		})
	}

	t.Run("last WithSpanKind wins", func(t *testing.T) {
		t.Parallel()
		got := telemetry.ApplySpanOptions([]telemetry.SpanOption{
			telemetry.WithSpanKind(telemetry.SpanKindClient),
			telemetry.WithSpanKind(telemetry.SpanKindServer),
		})
		testkit.Equal(t, got, telemetry.SpanKindServer,
			"last WithSpanKind must override prior options")
	})
}

func BenchmarkApplySpanOptions(b *testing.B) {
	opts := []telemetry.SpanOption{telemetry.WithSpanKind(telemetry.SpanKindServer)}
	b.ReportAllocs()
	for b.Loop() {
		_ = telemetry.ApplySpanOptions(opts)
	}
}
