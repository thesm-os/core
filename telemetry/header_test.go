// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"net/http"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
)

// headerValue is the value of every header that the cases store.
const headerValue = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// longKey is a key longer than the buffer in which HeaderCarrier builds a
// canonical key on its stack.
var longKey = "x-" + strings.Repeat("ab", 40)

func TestHeaderCarrier(t *testing.T) {
	t.Parallel()

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give telemetry.HeaderCarrier
			key  string
			want string
		}{
			{
				name: "returns the value of a header whose key differs in case",
				give: telemetry.HeaderCarrier{"Traceparent": {headerValue}},
				key:  "traceparent",
				want: headerValue,
			},
			{
				name: "returns the value of a canonical key",
				give: telemetry.HeaderCarrier{"Traceparent": {headerValue}},
				key:  "Traceparent",
				want: headerValue,
			},
			{
				name: "returns the first of several values",
				give: telemetry.HeaderCarrier{"Tracestate": {"a=1", "b=2"}},
				key:  "tracestate",
				want: "a=1",
			},
			{
				name: "returns the empty string for an absent header",
				give: telemetry.HeaderCarrier{"Traceparent": {headerValue}},
				key:  "tracestate",
			},
			{
				name: "returns the empty string for a header without values",
				give: telemetry.HeaderCarrier{"Traceparent": {}},
				key:  "traceparent",
			},
			{
				name: "returns the empty string from a nil carrier",
				key:  "traceparent",
			},
			{
				name: "returns the value of a key with a space as it is",
				give: telemetry.HeaderCarrier{"trace parent": {headerValue}},
				key:  "trace parent",
				want: headerValue,
			},
			{
				name: "returns the value of a key longer than the stack buffer",
				give: telemetry.HeaderCarrier{textproto.CanonicalMIMEHeaderKey(longKey): {headerValue}},
				key:  longKey,
				want: headerValue,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				testkit.Equal(t, tt.give.Get(tt.key), tt.want, "Get")
			})
		}

		t.Run("returns the value that net/http's Header.Set wrote", func(t *testing.T) {
			t.Parallel()

			h := http.Header{}
			h.Set("traceparent", headerValue)
			testkit.Equal(t, telemetry.HeaderCarrier(h).Get("traceparent"), headerValue, "Get")
		})

		t.Run("returns the value under every key that textproto canonicalises", func(t *testing.T) {
			t.Parallel()

			for _, key := range probeKeys() {
				c := telemetry.HeaderCarrier{textproto.CanonicalMIMEHeaderKey(key): {headerValue}}
				testkit.Equal(t, c.Get(key), headerValue, "Get of "+strconv.Quote(key))
			}
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give telemetry.HeaderCarrier
			key  string
			want telemetry.HeaderCarrier
		}{
			{
				name: "stores the value under the canonical form of the key",
				give: telemetry.HeaderCarrier{},
				key:  "traceparent",
				want: telemetry.HeaderCarrier{"Traceparent": {headerValue}},
			},
			{
				name: "replaces every value of the header",
				give: telemetry.HeaderCarrier{"Traceparent": {"a", "b"}},
				key:  "TRACEPARENT",
				want: telemetry.HeaderCarrier{"Traceparent": {headerValue}},
			},
			{
				name: "stores a key with a space as it is",
				give: telemetry.HeaderCarrier{},
				key:  "trace parent",
				want: telemetry.HeaderCarrier{"trace parent": {headerValue}},
			},
			{
				name: "stores a key longer than the stack buffer in canonical form",
				give: telemetry.HeaderCarrier{},
				key:  longKey,
				want: telemetry.HeaderCarrier{textproto.CanonicalMIMEHeaderKey(longKey): {headerValue}},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				tt.give.Set(tt.key, headerValue)
				testkit.Equal(t, tt.give, tt.want, "Set")
			})
		}

		t.Run("writes the header that net/http's Header.Get reads", func(t *testing.T) {
			t.Parallel()

			h := http.Header{}
			telemetry.HeaderCarrier(h).Set("traceparent", headerValue)
			testkit.Equal(t, h.Get("Traceparent"), headerValue, "Header.Get")
		})

		t.Run("stores every key under the key that textproto canonicalises", func(t *testing.T) {
			t.Parallel()

			for _, key := range probeKeys() {
				c := telemetry.HeaderCarrier{}
				c.Set(key, headerValue)
				want := telemetry.HeaderCarrier{textproto.CanonicalMIMEHeaderKey(key): {headerValue}}
				testkit.Equal(t, c, want, "Set of "+strconv.Quote(key))
			}
		})
	})

	t.Run("Keys", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key of every header", func(t *testing.T) {
			t.Parallel()

			c := telemetry.HeaderCarrier{"Traceparent": {headerValue}, "Tracestate": {"a=1"}}
			keys := c.Keys()
			slices.Sort(keys)
			testkit.Equal(t, keys, []string{"Traceparent", "Tracestate"}, "Keys")
		})

		t.Run("returns nil for a carrier without headers", func(t *testing.T) {
			t.Parallel()

			testkit.Equal(t, telemetry.HeaderCarrier{}.Keys(), []string(nil), "Keys")
		})
	})
}

func FuzzHeaderCarrier(f *testing.F) {
	for _, key := range []string{"traceparent", "Tracestate", "x-b3-traceid", "trace parent", "", "-", "a--b", longKey} {
		f.Add(key)
	}

	f.Fuzz(func(t *testing.T, key string) {
		want := textproto.CanonicalMIMEHeaderKey(key)

		c := telemetry.HeaderCarrier{}
		c.Set(key, headerValue)
		testkit.Equal(t, c, telemetry.HeaderCarrier{want: {headerValue}}, "Set must store under the textproto key")
		testkit.Equal(t, c.Get(key), headerValue, "Get must read what Set stored")
	})
}

func BenchmarkHeaderCarrier(b *testing.B) {
	carrier := telemetry.HeaderCarrier{"Traceparent": {headerValue}}

	b.Run("Get of a key that is not canonical", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var got string
		for c.Loop() {
			got = carrier.Get("traceparent")
		}

		testkit.Equal(b, got, headerValue, "the benchmark must measure a header that is present")
	})

	b.Run("Get of a canonical key", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var got string
		for c.Loop() {
			got = carrier.Get("Traceparent")
		}

		testkit.Equal(b, got, headerValue, "the benchmark must measure a header that is present")
	})

	b.Run("Set of an interned key that is not canonical", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			carrier.Set("traceparent", headerValue)
		}
	})

	b.Run("Set of a key that is not canonical", func(b *testing.B) {
		other := telemetry.HeaderCarrier{"X-Request-Id": {headerValue}}

		c := bench.Start(b).MaxAllocs(2)
		defer c.End()

		for c.Loop() {
			other.Set("x-request-id", headerValue)
		}
	})

	b.Run("Set of a canonical key", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			carrier.Set("Traceparent", headerValue)
		}
	})

	b.Run("Keys", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		var keys []string
		for c.Loop() {
			keys = carrier.Keys()
		}

		testkit.Equal(b, keys, []string{"Traceparent"}, "the benchmark must measure a carrier with a header")
	})
}

// probeKeys returns keys that place every byte value at the start of a key,
// inside a key, and before and after a hyphen, so that a comparison against
// textproto covers every branch of canonicalisation.
func probeKeys() []string {
	fixed := []string{"", "-", "--a", "a--b", "A-b", "Ab-c", "X-Y-z", "ABC", "aBC", "Abc-DEF", longKey}

	keys := make([]string, 0, len(fixed)+5*256)
	keys = append(keys, fixed...)
	for c := range 256 {
		s := string([]byte{byte(c)})
		keys = append(keys, s, "a"+s+"b", "A"+s+"B", "x-"+s+"y", s+"-x")
	}

	return keys
}
