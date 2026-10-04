// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package semconv_test

import (
	"net/http"
	"testing"

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/net/internal/semconv"
)

func TestSemconv(t *testing.T) {
	t.Parallel()

	t.Run("Method", func(t *testing.T) {
		t.Parallel()

		methods := []string{
			http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
			http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace,
		}
		for _, m := range methods {
			t.Run("returns "+m+" as it is", func(t *testing.T) {
				t.Parallel()

				testkit.Equal(t, semconv.Method(m), m, "Method")
			})
		}

		tests := []struct {
			name string
			give string
		}{
			{name: "returns _OTHER for a method in lower case", give: "get"},
			{name: "returns _OTHER for an extension method", give: "PROPFIND"},
			{name: "returns _OTHER for the empty method", give: ""},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				testkit.Equal(t, semconv.Method(tt.give), semconv.OtherMethod, "Method")
			})
		}
	})
}

func BenchmarkSemconv(b *testing.B) {
	b.Run("Method", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var got string
		for c.Loop() {
			got = semconv.Method("PROPFIND")
		}

		testkit.Equal(b, got, semconv.OtherMethod, "the benchmark must measure a normalised method")
	})
}
