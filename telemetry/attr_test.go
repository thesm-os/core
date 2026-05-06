// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"bytes"
	"log/slog"
	"testing"

	"go.thesmos.sh/core/telemetry"
)

func TestAttrConstructors(t *testing.T) {
	t.Parallel()

	t.Run("AttrString sets Kind=String and Str", func(t *testing.T) {
		t.Parallel()
		a := telemetry.AttrString("key", "value")
		if a.Key != "key" {
			t.Fatalf("Key: got %q, want %q", a.Key, "key")
		}
		if a.Value.Kind != telemetry.AttrKindString {
			t.Fatalf("Kind: got %v, want %v", a.Value.Kind, telemetry.AttrKindString)
		}
		if a.Value.Str != "value" {
			t.Fatalf("Str: got %q, want %q", a.Value.Str, "value")
		}
	})

	t.Run("AttrInt sets Kind=Int64 and Int", func(t *testing.T) {
		t.Parallel()
		a := telemetry.AttrInt("retries", 7)
		if a.Key != "retries" {
			t.Fatalf("Key: got %q, want %q", a.Key, "retries")
		}
		if a.Value.Kind != telemetry.AttrKindInt64 {
			t.Fatalf("Kind: got %v, want %v", a.Value.Kind, telemetry.AttrKindInt64)
		}
		if a.Value.Int != 7 {
			t.Fatalf("Int: got %d, want 7", a.Value.Int)
		}
	})

	t.Run("AttrFloat sets Kind=Float64 and Float", func(t *testing.T) {
		t.Parallel()
		a := telemetry.AttrFloat("ratio", 0.42)
		if a.Key != "ratio" {
			t.Fatalf("Key: got %q, want %q", a.Key, "ratio")
		}
		if a.Value.Kind != telemetry.AttrKindFloat64 {
			t.Fatalf("Kind: got %v, want %v", a.Value.Kind, telemetry.AttrKindFloat64)
		}
		if a.Value.Float != 0.42 {
			t.Fatalf("Float: got %v, want 0.42", a.Value.Float)
		}
	})

	t.Run("AttrBool sets Kind=Bool and Bool", func(t *testing.T) {
		t.Parallel()
		a := telemetry.AttrBool("ok", true)
		if a.Key != "ok" {
			t.Fatalf("Key: got %q, want %q", a.Key, "ok")
		}
		if a.Value.Kind != telemetry.AttrKindBool {
			t.Fatalf("Kind: got %v, want %v", a.Value.Kind, telemetry.AttrKindBool)
		}
		if !a.Value.Bool {
			t.Fatal("Bool: got false, want true")
		}
	})

	t.Run("AttrBytes aliases the supplied slice", func(t *testing.T) {
		t.Parallel()
		payload := []byte{0x01, 0x02, 0x03}
		a := telemetry.AttrBytes("payload", payload)
		if a.Key != "payload" {
			t.Fatalf("Key: got %q, want %q", a.Key, "payload")
		}
		if a.Value.Kind != telemetry.AttrKindBytes {
			t.Fatalf("Kind: got %v, want %v", a.Value.Kind, telemetry.AttrKindBytes)
		}
		if !bytes.Equal(a.Value.Bytes, payload) {
			t.Fatalf("Bytes: got %v, want %v", a.Value.Bytes, payload)
		}
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
			got := tc.in.SlogAttr()
			if !got.Equal(tc.want) {
				t.Fatalf("SlogAttr: got %+v, want %+v", got, tc.want)
			}
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
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}
}

func BenchmarkAttrString(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = telemetry.AttrString("user_id", "abc123")
	}
}

func BenchmarkAttrInt(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = telemetry.AttrInt("retry", 42)
	}
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
