// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/telemetry"
)

func TestMapCarrier(t *testing.T) {
	t.Parallel()

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the empty string for an absent key", func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, telemetry.MapCarrier{}.Get(attrKey), "an absent key must read as the empty string")
		})

		t.Run("returns the value of a key", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, telemetry.MapCarrier{attrKey: attrValue}.Get(attrKey), attrValue,
				"Get must return the stored value")
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		t.Run("replaces the value of a key", func(t *testing.T) {
			t.Parallel()
			c := telemetry.MapCarrier{attrKey: "old"}
			c.Set(attrKey, attrValue)
			assert.Equal(t, c, telemetry.MapCarrier{attrKey: attrValue}, "Set must replace the value of the key")
		})

		t.Run("stores a value that Get returns", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Get must return the value that Set stored", func(c *prop.Case) {
				key, value := c.Draw(prop.String(), "key"), c.Draw(prop.String(), "value")
				carrier := telemetry.MapCarrier{}
				carrier.Set(key, value)
				assert.Equal(c, carrier.Get(key), value, "Get must return the value that Set stored")
			})
		})
	})

	t.Run("Keys", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key of every entry", func(t *testing.T) {
			t.Parallel()
			assert.Permutation(t, telemetry.MapCarrier{"a": "1", "b": "2"}.Keys(), []string{"a", "b"},
				"Keys must return the key of every entry")
		})

		t.Run("returns nil for an empty carrier", func(t *testing.T) {
			t.Parallel()
			assert.Nil(t, telemetry.MapCarrier{}.Keys(), "Keys must return nil for an empty carrier")
		})
	})
}

// TestMapCarrierAllocs checks the allocation contract of each method.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestMapCarrierAllocs(t *testing.T) {
	carrier := telemetry.MapCarrier{attrKey: attrValue, "a": "1", "b": "2"}

	t.Run("Get", func(t *testing.T) {
		var got string
		expect.MaxAllocs(t, func() { got = carrier.Get(attrKey) }, 0, "Get must not allocate")
		assert.Equal(t, got, attrValue, "the test must measure a stored value")
	})

	t.Run("Set", func(t *testing.T) {
		expect.MaxAllocs(t, func() { carrier.Set(attrKey, attrValue) }, 0, "Set of a stored key must not allocate")
		assert.Equal(t, carrier.Get(attrKey), attrValue, "the test must measure a stored value")
	})

	t.Run("Keys", func(t *testing.T) {
		var got []string
		expect.MaxAllocs(t, func() { got = carrier.Keys() }, 1, "Keys must allocate its slice once")
		assert.Length(t, got, len(carrier), "the test must measure a carrier of several keys")
	})
}

// BenchmarkMapCarrier reports the cost of each method, and fails above the
// allocations that their contracts state.
func BenchmarkMapCarrier(b *testing.B) {
	carrier := telemetry.MapCarrier{attrKey: attrValue, "a": "1", "b": "2"}

	b.Run("Get", func(b *testing.B) {
		var got string

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = carrier.Get(attrKey)
		}

		assert.Equal(b, got, attrValue, "the benchmark must measure a stored value")
	})

	b.Run("Set", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			carrier.Set(attrKey, attrValue)
		}

		assert.Equal(b, carrier.Get(attrKey), attrValue, "the benchmark must measure a stored value")
	})

	b.Run("Keys", func(b *testing.B) {
		var got []string

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got = carrier.Keys()
		}

		assert.Length(b, got, len(carrier), "the benchmark must measure a carrier of several keys")
	})
}
