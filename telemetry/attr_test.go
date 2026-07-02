// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"log/slog"
	"runtime"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
)

func TestAttrConstructors(t *testing.T) {
	t.Parallel()

	t.Run("AttrString sets Kind=String and Str", func(t *testing.T) {
		t.Parallel()
		a := telemetry.AttrString("key", "value")
		testkit.Equal(t, a.Key, "key", "AttrString Key must round-trip")
		testkit.Equal(t, a.Value.Kind, telemetry.AttrKindString, "AttrString Kind must be String")
		testkit.Equal(t, a.Value.Str, "value", "AttrString Str must round-trip")
	})

	t.Run("AttrInt sets Kind=Int64 and Int", func(t *testing.T) {
		t.Parallel()
		a := telemetry.AttrInt("retries", 7)
		testkit.Equal(t, a.Key, "retries", "AttrInt Key must round-trip")
		testkit.Equal(t, a.Value.Kind, telemetry.AttrKindInt64, "AttrInt Kind must be Int64")
		testkit.Equal(t, a.Value.Int, int64(7), "AttrInt Int must round-trip")
	})

	t.Run("AttrFloat sets Kind=Float64 and Float", func(t *testing.T) {
		t.Parallel()
		a := telemetry.AttrFloat("ratio", 0.42)
		testkit.Equal(t, a.Key, "ratio", "AttrFloat Key must round-trip")
		testkit.Equal(t, a.Value.Kind, telemetry.AttrKindFloat64, "AttrFloat Kind must be Float64")
		testkit.Equal(t, a.Value.Float, 0.42, "AttrFloat Float must round-trip")
	})

	t.Run("AttrBool sets Kind=Bool and Bool", func(t *testing.T) {
		t.Parallel()
		a := telemetry.AttrBool("ok", true)
		testkit.Equal(t, a.Key, "ok", "AttrBool Key must round-trip")
		testkit.Equal(t, a.Value.Kind, telemetry.AttrKindBool, "AttrBool Kind must be Bool")
		testkit.True(t, a.Value.Bool, "AttrBool Bool must be true")
	})

	t.Run("AttrBytes aliases the supplied slice", func(t *testing.T) {
		t.Parallel()
		payload := []byte{0x01, 0x02, 0x03}
		a := telemetry.AttrBytes("payload", payload)
		testkit.Equal(t, a.Key, "payload", "AttrBytes Key must round-trip")
		testkit.Equal(t, a.Value.Kind, telemetry.AttrKindBytes, "AttrBytes Kind must be Bytes")
		testkit.Equal(t, a.Value.Bytes, payload, "AttrBytes Bytes must round-trip")
	})
}

func TestSlogAttr(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		want slog.Attr
		in   telemetry.Attr
	}{
		"string":                    {slog.String("k", "v"), telemetry.AttrString("k", "v")},
		"int64":                     {slog.Int64("k", 42), telemetry.AttrInt("k", 42)},
		"float64":                   {slog.Float64("k", 1.5), telemetry.AttrFloat("k", 1.5)},
		"bool":                      {slog.Bool("k", true), telemetry.AttrBool("k", true)},
		"bytes maps to string":      {slog.String("k", "hello"), telemetry.AttrBytes("k", []byte("hello"))},
		"unspecified maps to empty": {slog.Attr{}, telemetry.Attr{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			testkit.True(t, tc.in.SlogAttr().Equal(tc.want),
				"SlogAttr must equal expected slog.Attr")
		})
	}
}

// TestZeroAlloc cannot run in parallel — testing.AllocsPerRun
// panics if any other test is running concurrently. The lint
// suppression below is the deliberate counterpart to that
// constraint.
//
//nolint:paralleltest // see comment above
func TestZeroAlloc(t *testing.T) {
	payload := []byte{0xff, 0xee}
	stringAttr := telemetry.AttrString("k", "v")
	intAttr := telemetry.AttrInt("k", 7)
	floatAttr := telemetry.AttrFloat("k", 0.5)
	boolAttr := telemetry.AttrBool("k", true)

	cases := []struct {
		fn   func()
		name string
	}{
		{func() { _ = telemetry.AttrString("k", "v") }, "AttrString"},
		{func() { _ = telemetry.AttrInt("k", 1) }, "AttrInt"},
		{func() { _ = telemetry.AttrFloat("k", 1.0) }, "AttrFloat"},
		{func() { _ = telemetry.AttrBool("k", true) }, "AttrBool"},
		{func() { _ = telemetry.AttrBytes("k", payload) }, "AttrBytes"},
		{func() { _ = stringAttr.SlogAttr() }, "SlogAttr/string"},
		{func() { _ = intAttr.SlogAttr() }, "SlogAttr/int"},
		{func() { _ = floatAttr.SlogAttr() }, "SlogAttr/float"},
		{func() { _ = boolAttr.SlogAttr() }, "SlogAttr/bool"},
		{func() { _ = (telemetry.Attr{}).SlogAttr() }, "SlogAttr/unspecified"},
	}
	// SlogAttr/bytes intentionally excluded — bytes→slog allocates by
	// design (slog has no zero-copy bytes value); see SlogAttr doc.

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(100, tc.fn),
				float64(0), tc.name+" must be zero-alloc")
		})
	}
}

func BenchmarkAttrString(b *testing.B) {
	b.ReportAllocs()
	var sink telemetry.Attr
	for b.Loop() {
		sink = telemetry.AttrString("user_id", "abc123")
	}
	runtime.KeepAlive(sink)
}

func BenchmarkAttrInt(b *testing.B) {
	b.ReportAllocs()
	var sink telemetry.Attr
	for b.Loop() {
		sink = telemetry.AttrInt("retry", 42)
	}
	runtime.KeepAlive(sink)
}

func BenchmarkSlogAttr(b *testing.B) {
	b.Run("string", func(b *testing.B) {
		a := telemetry.AttrString("k", "v")
		b.ReportAllocs()
		for b.Loop() {
			_ = a.SlogAttr()
		}
	})
	b.Run("int", func(b *testing.B) {
		a := telemetry.AttrInt("k", 1)
		b.ReportAllocs()
		for b.Loop() {
			_ = a.SlogAttr()
		}
	})
	b.Run("bytes", func(b *testing.B) {
		a := telemetry.AttrBytes("k", []byte("payload"))
		b.ReportAllocs()
		for b.Loop() {
			_ = a.SlogAttr()
		}
	})
}
