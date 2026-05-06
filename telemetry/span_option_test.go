// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"testing"

	"go.thesmos.sh/core/telemetry"
)

func TestApplySpanOptions(t *testing.T) {
	t.Parallel()

	t.Run("no options yields default SpanKindInternal", func(t *testing.T) {
		t.Parallel()
		got := telemetry.ApplySpanOptions(nil)
		if got != telemetry.SpanKindInternal {
			t.Fatalf("ApplySpanOptions(nil): got %v, want %v",
				got, telemetry.SpanKindInternal)
		}
	})

	t.Run("explicit SpanKindUnspecified falls through to default", func(t *testing.T) {
		t.Parallel()
		got := telemetry.ApplySpanOptions([]telemetry.SpanOption{
			telemetry.WithSpanKind(telemetry.SpanKindUnspecified),
		})
		if got != telemetry.SpanKindInternal {
			t.Fatalf("ApplySpanOptions(Unspecified): got %v, want %v",
				got, telemetry.SpanKindInternal)
		}
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
			if got != kind {
				t.Fatalf("ApplySpanOptions(%v): got %v, want %v",
					kind, got, kind)
			}
		})
	}

	t.Run("last WithSpanKind wins", func(t *testing.T) {
		t.Parallel()
		got := telemetry.ApplySpanOptions([]telemetry.SpanOption{
			telemetry.WithSpanKind(telemetry.SpanKindClient),
			telemetry.WithSpanKind(telemetry.SpanKindServer),
		})
		if got != telemetry.SpanKindServer {
			t.Fatalf("ApplySpanOptions(client, server): got %v, want %v",
				got, telemetry.SpanKindServer)
		}
	})
}
