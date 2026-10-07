// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/telemetry"
)

// The fixture values of the cases of HeaderCarrier.
const (
	// headerValue is the value of every header that the cases store.
	headerValue = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	// storesCanonically is the contract of the property and of the fuzz
	// target of Set and Get.
	storesCanonically = "Set must store a value under the key that textproto canonicalises"
)

// longKey is a key longer than the buffer in which HeaderCarrier builds a
// canonical key on its stack.
var longKey = "x-" + strings.Repeat("ab", 40)

// The keys of the allocation ceilings of Get and Set, with the allocations
// that the contract of HeaderCarrier states for each.
var (
	gets = []struct {
		name string
		key  string
		want uint64
	}{
		{name: "of a key that is not canonical", key: "traceparent"},
		{name: "of a canonical key", key: "Traceparent"},
		{name: "of a key longer than the stack buffer", key: longKey, want: 1},
	}

	sets = []struct {
		name string
		key  string
		want uint64
	}{
		{name: "of an interned key that is not canonical", key: "traceparent", want: 1},
		{name: "of a key that is not canonical", key: "x-request-id", want: 2},
		{name: "of a canonical key", key: "Traceparent", want: 1},
	}
)

func TestHeaderCarrier(t *testing.T) {
	t.Parallel()

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			give telemetry.HeaderCarrier
			name string
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
				assert.Equal(t, tt.give.Get(tt.key), tt.want, "Get must return the first value of the header")
			})
		}

		t.Run("returns the value that net/http's Header.Set wrote", func(t *testing.T) {
			t.Parallel()
			h := http.Header{}
			h.Set("traceparent", headerValue)
			assert.Equal(t, telemetry.HeaderCarrier(h).Get("traceparent"), headerValue,
				"Get must read the key of net/http")
		})

		t.Run("returns the value under every key that textproto canonicalises", func(t *testing.T) {
			t.Parallel()
			for _, key := range probeKeys() {
				c := telemetry.HeaderCarrier{textproto.CanonicalMIMEHeaderKey(key): {headerValue}}
				expect.Equal(t, c.Get(key), headerValue, "Get must read the textproto key of "+strconv.Quote(key))
			}
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			give telemetry.HeaderCarrier
			want telemetry.HeaderCarrier
			name string
			key  string
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
				assert.Equal(t, tt.give, tt.want, "Set must store the value under the canonical key")
			})
		}

		t.Run("writes the header that net/http's Header.Get reads", func(t *testing.T) {
			t.Parallel()
			h := http.Header{}
			telemetry.HeaderCarrier(h).Set("traceparent", headerValue)
			assert.Equal(t, h.Get("Traceparent"), headerValue, "Set must write the key of net/http")
		})

		t.Run("stores every key under the key that textproto canonicalises", func(t *testing.T) {
			t.Parallel()
			for _, key := range probeKeys() {
				c := telemetry.HeaderCarrier{}
				c.Set(key, headerValue)
				expect.Equal(t, c, telemetry.HeaderCarrier{textproto.CanonicalMIMEHeaderKey(key): {headerValue}},
					"Set must store under the textproto key of "+strconv.Quote(key))
			}
		})

		t.Run("stores any key under the key that textproto canonicalises", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, storesCanonically, storesUnderTextprotoKey)
		})
	})

	t.Run("Keys", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key of every header", func(t *testing.T) {
			t.Parallel()
			c := telemetry.HeaderCarrier{"Traceparent": {headerValue}, "Tracestate": {"a=1"}}
			assert.Permutation(t, c.Keys(), []string{"Traceparent", "Tracestate"},
				"Keys must return the key of every header")
		})

		t.Run("returns nil for a carrier without headers", func(t *testing.T) {
			t.Parallel()
			assert.Nil(t, telemetry.HeaderCarrier{}.Keys(), "Keys must return nil for a carrier without headers")
		})
	})
}

// TestHeaderCarrierAllocs checks the allocation contract of each method.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestHeaderCarrierAllocs(t *testing.T) {
	carrier := telemetry.HeaderCarrier{
		"Traceparent": {headerValue},
		textproto.CanonicalMIMEHeaderKey(longKey): {headerValue},
	}

	t.Run("Get", func(t *testing.T) {
		for _, tt := range gets {
			t.Run(tt.name, func(t *testing.T) {
				var got string
				expect.MaxAllocs(t, func() { got = carrier.Get(tt.key) }, tt.want,
					"Get must allocate as its contract states")
				assert.Equal(t, got, headerValue, "the test must measure a header that is present")
			})
		}
	})

	t.Run("Set", func(t *testing.T) {
		for _, tt := range sets {
			t.Run(tt.name, func(t *testing.T) {
				expect.MaxAllocs(t, func() { carrier.Set(tt.key, headerValue) }, tt.want,
					"Set must allocate as its contract states")
				assert.Equal(t, carrier.Get(tt.key), headerValue, "the test must measure a stored header")
			})
		}
	})

	t.Run("Keys", func(t *testing.T) {
		var got []string
		expect.MaxAllocs(t, func() { got = carrier.Keys() }, 1, "Keys must allocate its slice alone")
		assert.NotEmpty(t, got, "the test must measure a carrier with headers")
	})
}

// BenchmarkHeaderCarrier reports the cost of each method, and fails above
// the allocations that their contracts state.
func BenchmarkHeaderCarrier(b *testing.B) {
	carrier := telemetry.HeaderCarrier{
		"Traceparent": {headerValue},
		textproto.CanonicalMIMEHeaderKey(longKey): {headerValue},
	}

	b.Run("Get", func(b *testing.B) {
		for _, tt := range gets {
			b.Run(tt.name, func(b *testing.B) {
				var got string

				c := bench.Start(b).MaxAllocs(tt.want)
				defer c.End()

				for c.Loop() {
					got = carrier.Get(tt.key)
				}

				assert.Equal(b, got, headerValue, "the benchmark must measure a header that is present")
			})
		}
	})

	b.Run("Set", func(b *testing.B) {
		for _, tt := range sets {
			b.Run(tt.name, func(b *testing.B) {
				c := bench.Start(b).MaxAllocs(tt.want)
				defer c.End()

				for c.Loop() {
					carrier.Set(tt.key, headerValue)
				}

				assert.Equal(b, carrier.Get(tt.key), headerValue, "the benchmark must measure a stored header")
			})
		}
	})

	b.Run("Keys", func(b *testing.B) {
		var got []string

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got = carrier.Keys()
		}

		assert.NotEmpty(b, got, "the benchmark must measure a carrier with headers")
	})
}

// FuzzHeaderCarrier checks storesUnderTextprotoKey for the keys that the
// fuzzer finds.
func FuzzHeaderCarrier(f *testing.F) {
	prop.Fuzz(f, storesCanonically, storesUnderTextprotoKey)
}

// storesUnderTextprotoKey checks that Set stores a value under the key that
// textproto canonicalises for a drawn key, and that Get reads the value
// back with the same key.
func storesUnderTextprotoKey(c *prop.Case) {
	key := c.Draw(prop.String(), "key")
	carrier := telemetry.HeaderCarrier{}
	carrier.Set(key, headerValue)

	assert.Equal(c, carrier, telemetry.HeaderCarrier{textproto.CanonicalMIMEHeaderKey(key): {headerValue}},
		"Set must store the value under the textproto key")
	assert.Equal(c, carrier.Get(key), headerValue, "Get must read the value that Set stored")
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
