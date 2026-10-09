// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"log/slog"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/telemetry"
)

// The fixture values of the cases of Attr.
const (
	attrKey   = "k"
	attrValue = "v"
)

// payload is the value of the byte attributes of the cases. It is longer
// than one byte, because Go converts a slice of one byte to a static
// string without an allocation.
var payload = []byte("payload")

// slogAttrs are an attribute of each kind, for the allocation ceilings of
// SlogAttr: none for a primitive kind, and the copy of the bytes for
// AttrKindBytes, because slog has no value of bytes.
var slogAttrs = []struct {
	give telemetry.Attr
	name string
	want uint64
}{
	{name: "of a string", give: telemetry.AttrString(attrKey, attrValue)},
	{name: "of an int64", give: telemetry.AttrInt(attrKey, 7)},
	{name: "of a float64", give: telemetry.AttrFloat(attrKey, 0.5)},
	{name: "of a bool", give: telemetry.AttrBool(attrKey, true)},
	{name: "of AttrKindUnspecified", give: telemetry.Attr{}},
	{name: "of bytes", give: telemetry.AttrBytes(attrKey, payload), want: 1},
}

func TestAttr(t *testing.T) {
	t.Parallel()

	t.Run("AttrString", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an Attr of the string", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, telemetry.AttrString(attrKey, attrValue), telemetry.Attr{
				Key:   attrKey,
				Value: telemetry.Value{Kind: telemetry.AttrKindString, Str: attrValue},
			}, "AttrString must set the key, the kind and the string")
		})
	})

	t.Run("AttrInt", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an Attr of the int64", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, telemetry.AttrInt(attrKey, 7), telemetry.Attr{
				Key:   attrKey,
				Value: telemetry.Value{Kind: telemetry.AttrKindInt64, Int: 7},
			}, "AttrInt must set the key, the kind and the int64")
		})
	})

	t.Run("AttrFloat", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an Attr of the float64", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, telemetry.AttrFloat(attrKey, 0.42), telemetry.Attr{
				Key:   attrKey,
				Value: telemetry.Value{Kind: telemetry.AttrKindFloat64, Float: 0.42},
			}, "AttrFloat must set the key, the kind and the float64")
		})
	})

	t.Run("AttrBool", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an Attr of the bool", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, telemetry.AttrBool(attrKey, true), telemetry.Attr{
				Key:   attrKey,
				Value: telemetry.Value{Kind: telemetry.AttrKindBool, Bool: true},
			}, "AttrBool must set the key, the kind and the bool")
		})
	})

	t.Run("AttrBytes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an Attr of the bytes", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, telemetry.AttrBytes(attrKey, payload), telemetry.Attr{
				Key:   attrKey,
				Value: telemetry.Value{Kind: telemetry.AttrKindBytes, Bytes: payload},
			}, "AttrBytes must set the key, the kind and the bytes")
		})

		t.Run("shares the array of the slice", func(t *testing.T) {
			t.Parallel()
			a := telemetry.AttrBytes(attrKey, payload)
			assert.Equal(t, a.Value.Bytes, payload, "AttrBytes must not copy the bytes", assert.ByIdentity())
		})
	})

	t.Run("SlogAttr", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			want slog.Attr
			give telemetry.Attr
			name string
		}{
			{
				name: "returns a string attribute for a string",
				give: telemetry.AttrString(attrKey, attrValue),
				want: slog.String(attrKey, attrValue),
			},
			{
				name: "returns an int64 attribute for an int64",
				give: telemetry.AttrInt(attrKey, 42),
				want: slog.Int64(attrKey, 42),
			},
			{
				name: "returns a float64 attribute for a float64",
				give: telemetry.AttrFloat(attrKey, 1.5),
				want: slog.Float64(attrKey, 1.5),
			},
			{
				name: "returns a bool attribute for a bool",
				give: telemetry.AttrBool(attrKey, true),
				want: slog.Bool(attrKey, true),
			},
			{
				name: "returns a string attribute for bytes",
				give: telemetry.AttrBytes(attrKey, payload),
				want: slog.String(attrKey, string(payload)),
			},
			{
				name: "returns the empty attribute for AttrKindUnspecified",
				give: telemetry.Attr{Key: attrKey},
				want: slog.Attr{},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got := tt.give.SlogAttr()
				assert.True(t, got.Equal(tt.want), "SlogAttr must return "+tt.want.String()+", not "+got.String())
			})
		}
	})
}

// TestAttrAllocs checks the allocation contract of the constructors and of
// SlogAttr. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestAttrAllocs(t *testing.T) {
	var got telemetry.Attr

	t.Run("AttrString", func(t *testing.T) {
		expect.MaxAllocs(t, func() { got = telemetry.AttrString(attrKey, attrValue) }, 0,
			"AttrString must not allocate")
		assert.Equal(t, got.Value.Str, attrValue, "the test must measure a string attribute")
	})

	t.Run("AttrInt", func(t *testing.T) {
		expect.MaxAllocs(t, func() { got = telemetry.AttrInt(attrKey, 7) }, 0, "AttrInt must not allocate")
		assert.Equal(t, got.Value.Int, int64(7), "the test must measure an int64 attribute")
	})

	t.Run("AttrFloat", func(t *testing.T) {
		expect.MaxAllocs(t, func() { got = telemetry.AttrFloat(attrKey, 0.5) }, 0, "AttrFloat must not allocate")
		assert.Equal(t, got.Value.Float, 0.5, "the test must measure a float64 attribute")
	})

	t.Run("AttrBool", func(t *testing.T) {
		expect.MaxAllocs(t, func() { got = telemetry.AttrBool(attrKey, true) }, 0, "AttrBool must not allocate")
		assert.True(t, got.Value.Bool, "the test must measure a bool attribute")
	})

	t.Run("AttrBytes", func(t *testing.T) {
		expect.MaxAllocs(t, func() { got = telemetry.AttrBytes(attrKey, payload) }, 0, "AttrBytes must not allocate")
		assert.Equal(t, got.Value.Bytes, payload, "the test must measure a byte attribute")
	})

	t.Run("SlogAttr", func(t *testing.T) {
		for _, tt := range slogAttrs {
			t.Run(tt.name, func(t *testing.T) {
				var a slog.Attr
				expect.MaxAllocs(t, func() { a = tt.give.SlogAttr() }, tt.want,
					"SlogAttr must allocate as its contract states")
				assert.Equal(t, a.Key, tt.give.Key, "the test must measure a converted attribute")
			})
		}
	})
}

// BenchmarkAttr reports the cost of the constructors and of SlogAttr, and
// fails above the allocations that their contracts state.
func BenchmarkAttr(b *testing.B) {
	var got telemetry.Attr

	b.Run("AttrString", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = telemetry.AttrString(attrKey, attrValue)
		}

		assert.Equal(b, got.Value.Str, attrValue, "the benchmark must measure a string attribute")
	})

	b.Run("AttrInt", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = telemetry.AttrInt(attrKey, 7)
		}

		assert.Equal(b, got.Value.Int, int64(7), "the benchmark must measure an int64 attribute")
	})

	b.Run("AttrFloat", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = telemetry.AttrFloat(attrKey, 0.5)
		}

		assert.Equal(b, got.Value.Float, 0.5, "the benchmark must measure a float64 attribute")
	})

	b.Run("AttrBool", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = telemetry.AttrBool(attrKey, true)
		}

		assert.True(b, got.Value.Bool, "the benchmark must measure a bool attribute")
	})

	b.Run("AttrBytes", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = telemetry.AttrBytes(attrKey, payload)
		}

		assert.Equal(b, got.Value.Bytes, payload, "the benchmark must measure a byte attribute")
	})

	b.Run("SlogAttr", func(b *testing.B) {
		for _, tt := range slogAttrs {
			b.Run(tt.name, func(b *testing.B) {
				var a slog.Attr

				c := bench.Start(b).MaxAllocs(tt.want)
				defer c.End()

				for c.Loop() {
					a = tt.give.SlogAttr()
				}

				assert.Equal(b, a.Key, tt.give.Key, "the benchmark must measure a converted attribute")
			})
		}
	})
}
