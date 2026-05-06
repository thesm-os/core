// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed_test

import (
	"encoding/binary"
	"testing"

	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/fixed"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("Uint64 returns the configured value on every call", func(t *testing.T) {
		t.Parallel()
		const v uint64 = 0xDEADBEEFCAFEBABE
		r := fixed.New(v)
		for range 5 {
			if got := r.Uint64(); got != v {
				t.Fatalf("Uint64: got %x, want %x", got, v)
			}
		}
	})

	t.Run("zero value returns zero", func(t *testing.T) {
		t.Parallel()
		var r fixed.Rand
		if got := r.Uint64(); got != 0 {
			t.Fatalf("zero-value Uint64: got %d, want 0", got)
		}
	})
}

func TestRead(t *testing.T) {
	t.Parallel()

	t.Run("fills with little-endian repetition of the configured value", func(t *testing.T) {
		t.Parallel()
		const v uint64 = 0x0102030405060708
		buf := make([]byte, 16)
		n, err := fixed.New(v).Read(buf)
		if err != nil {
			t.Fatalf("Read: unexpected error %v", err)
		}
		if n != 16 {
			t.Fatalf("Read: filled %d, want 16", n)
		}
		if got := binary.LittleEndian.Uint64(buf[0:8]); got != v {
			t.Fatalf("first half: got %x, want %x", got, v)
		}
		if got := binary.LittleEndian.Uint64(buf[8:16]); got != v {
			t.Fatalf("second half: got %x, want %x", got, v)
		}
	})

	t.Run("non-aligned length fills the prefix correctly", func(t *testing.T) {
		t.Parallel()
		buf := make([]byte, 11)
		n, _ := fixed.New(0x42).Read(buf)
		if n != 11 {
			t.Fatalf("Read: filled %d, want 11", n)
		}
		if buf[0] != 0x42 {
			t.Fatalf("buf[0]: got %x, want 0x42", buf[0])
		}
	})
}

func TestFromFloat64(t *testing.T) {
	t.Parallel()

	t.Run("Float64 round-trips for in-range values", func(t *testing.T) {
		t.Parallel()
		// FromFloat64 inverts the (uint64 >> 11) / 2^53
		// construction; values exactly representable on the
		// 53-bit mantissa round-trip exactly.
		for _, v := range []float64{0.0, 0.25, 0.5, 0.75} {
			if got := rand.Float64(fixed.FromFloat64(v)); got != v {
				t.Fatalf("Float64(FromFloat64(%v)): got %v, want %v", v, got, v)
			}
		}
	})

	t.Run("clamps negative inputs to 0.0", func(t *testing.T) {
		t.Parallel()
		if got := rand.Float64(fixed.FromFloat64(-1.0)); got != 0.0 {
			t.Fatalf("FromFloat64(-1.0): got %v, want 0.0", got)
		}
	})

	t.Run("clamps inputs >= 1.0 to 1 - 2^-53", func(t *testing.T) {
		t.Parallel()
		const want = 1.0 - 1.0/(1<<53)
		for _, v := range []float64{1.0, 1.5, 2.0, 1e10} {
			got := rand.Float64(fixed.FromFloat64(v))
			if got != want {
				t.Fatalf("FromFloat64(%v): got %v, want %v", v, got, want)
			}
			// Strict invariant: the clamped value must remain
			// strictly less than 1.0.
			if got >= 1.0 {
				t.Fatalf("FromFloat64(%v) = %v, must be < 1.0", v, got)
			}
		}
	})
}

// TestZeroAlloc enforces the documented "Zero alloc" contracts on
// fixed.Rand's hot-path methods. testing.AllocsPerRun uses a
// process-global malloc counter, so this test does not call
// t.Parallel.
func TestZeroAlloc(t *testing.T) {
	r := fixed.New(0xDEADBEEF)
	buf := make([]byte, 64)

	cases := []struct {
		name string
		fn   func()
	}{
		{"Uint64", func() { _ = r.Uint64() }},
		{"Read", func() { _, _ = r.Read(buf) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}
}

func BenchmarkUint64(b *testing.B) {
	r := fixed.New(0xdeadbeef)
	b.ReportAllocs()
	b.SetBytes(8)
	for b.Loop() {
		_ = r.Uint64()
	}
}

func BenchmarkRead(b *testing.B) {
	r := fixed.New(0xdeadbeef)
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"8B", 8},
		{"64B", 64},
		{"4K", 4096},
	} {
		b.Run(sz.name, func(b *testing.B) {
			buf := make([]byte, sz.n)
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				_, _ = r.Read(buf)
			}
		})
	}
}
