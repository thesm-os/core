// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package semconv_test

import (
	"net/http"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/net/internal/semconv"
)

// methods lists the methods of RFC 9110 and PATCH of RFC 5789, which
// Method returns as they are.
var methods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
	http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace,
}

func TestSemconv(t *testing.T) {
	t.Parallel()

	t.Run("Method", func(t *testing.T) {
		t.Parallel()

		for _, m := range methods {
			t.Run("returns "+m+" as it is", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, semconv.Method(m), m, "Method must return a method of the conventions as it is")
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
				assert.Equal(t, semconv.Method(tt.give), semconv.OtherMethod, "Method must normalise the method")
			})
		}

		t.Run("returns _OTHER for every other string", func(t *testing.T) {
			t.Parallel()
			others := prop.String().Filter(func(m string) bool { return !slices.Contains(methods, m) })
			prop.Equal(t, semconv.Method, func(string) string { return semconv.OtherMethod },
				"Method must bound the values of the attribute to the methods of the conventions", prop.Using(others))
		})
	})
}

// TestSemconvAllocs checks the allocation contract of Method. MaxAllocs
// counts the allocations of the whole process, so the test does not run
// in parallel.
//
//nolint:paralleltest // see above
func TestSemconvAllocs(t *testing.T) {
	t.Run("Method", func(t *testing.T) {
		var got string
		expect.MaxAllocs(t, func() { got = semconv.Method("PROPFIND") }, 0, "Method must not allocate")
		assert.Equal(t, got, semconv.OtherMethod, "the test must measure a normalised method")
	})
}

// BenchmarkSemconv reports the cost of Method, and fails when it
// allocates.
func BenchmarkSemconv(b *testing.B) {
	b.Run("Method", func(b *testing.B) {
		var got string

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = semconv.Method("PROPFIND")
		}

		assert.Equal(b, got, semconv.OtherMethod, "the benchmark must measure a normalised method")
	})
}
