// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pcg_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/pcg"
)

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("invalid hex fixture: %v", err)
	}
	return b
}

func TestDeterminism(t *testing.T) {
	t.Parallel()

	t.Run("same seed produces identical Uint64 streams", func(t *testing.T) {
		t.Parallel()
		a, b := pcg.New(rand.Seed(42)), pcg.New(rand.Seed(42))
		for i := range 1000 {
			va, vb := a.Uint64(), b.Uint64()
			if va != vb {
				t.Fatalf("step %d: %d != %d", i, va, vb)
			}
		}
	})

	t.Run("same seed produces identical byte streams via Read", func(t *testing.T) {
		t.Parallel()
		a, b := pcg.New(rand.Seed(99)), pcg.New(rand.Seed(99))
		ba, bb := make([]byte, 256), make([]byte, 256)
		_, _ = a.Read(ba)
		_, _ = b.Read(bb)
		if !bytes.Equal(ba, bb) {
			t.Fatal("streams diverged for identical seeds")
		}
	})

	t.Run("different seeds produce different streams", func(t *testing.T) {
		t.Parallel()
		a, b := pcg.New(rand.Seed(1)), pcg.New(rand.Seed(2))
		for range 4 {
			if a.Uint64() != b.Uint64() {
				return
			}
		}
		t.Fatal("different seeds produced four identical Uint64 draws (statistically impossible)")
	})
}

func TestSeed(t *testing.T) {
	t.Parallel()

	t.Run("returns the construction-time seed", func(t *testing.T) {
		t.Parallel()
		const seed = rand.Seed(123)
		if got := pcg.New(seed).Seed(); got != seed {
			t.Fatalf("Seed: got %d, want %d", got, seed)
		}
	})

	t.Run("is invariant as the generator advances", func(t *testing.T) {
		t.Parallel()
		const seed = rand.Seed(456)
		r := pcg.New(seed)
		for range 100 {
			r.Uint64()
		}
		if got := r.Seed(); got != seed {
			t.Fatalf("Seed after advance: got %d, want %d", got, seed)
		}
	})
}

func TestRead(t *testing.T) {
	t.Parallel()

	t.Run("fills the entire slice and returns no error", func(t *testing.T) {
		t.Parallel()
		buf := make([]byte, 100)
		n, err := pcg.New(rand.Seed(1)).Read(buf)
		if err != nil {
			t.Fatalf("Read: unexpected error %v", err)
		}
		if n != 100 {
			t.Fatalf("Read: filled %d, want 100", n)
		}
	})

	t.Run("zero-length slice is a no-op", func(t *testing.T) {
		t.Parallel()
		n, err := pcg.New(rand.Seed(1)).Read(nil)
		if err != nil {
			t.Fatalf("Read(nil): unexpected error %v", err)
		}
		if n != 0 {
			t.Fatalf("Read(nil): filled %d, want 0", n)
		}
	})

	t.Run("non-aligned length still fills correctly", func(t *testing.T) {
		t.Parallel()
		buf := make([]byte, 13)
		n, err := pcg.New(rand.Seed(1)).Read(buf)
		if err != nil {
			t.Fatalf("Read: unexpected error %v", err)
		}
		if n != 13 {
			t.Fatalf("Read: filled %d, want 13", n)
		}
	})

	t.Run("split reads equal a single read of the same length", func(t *testing.T) {
		t.Parallel()
		// Behavioural contract: callers can substitute one Read(n)
		// with multiple Read calls totalling n bytes — the byte
		// stream is the same. Kills mutations that draw extra
		// Uint64s on the aligned-tail branch.
		single := make([]byte, 32)
		_, _ = pcg.New(rand.Seed(2)).Read(single)

		split := make([]byte, 32)
		r := pcg.New(rand.Seed(2))
		_, _ = r.Read(split[:8])
		_, _ = r.Read(split[8:24])
		_, _ = r.Read(split[24:])
		if !bytes.Equal(single, split) {
			t.Fatalf("split reads diverged from single Read:\n single=%x\n split =%x", single, split)
		}
	})

	t.Run("seed 42 produces a known 32-byte fixture (aligned)", func(t *testing.T) {
		t.Parallel()
		// Golden bytes recorded from pcg.New(42).Read(make([]byte, 32)).
		// Pinned to detect drift; covers the aligned (8-byte
		// multiple) path of Read.
		want := mustDecodeHex(t,
			"9fa987db721b88dbe49fb0762d6df1f5c73270890a25e4227161b5e80282a816")
		got := make([]byte, len(want))
		_, _ = pcg.New(rand.Seed(42)).Read(got)
		if !bytes.Equal(got, want) {
			t.Fatalf("got  %x\nwant %x", got, want)
		}
	})

	t.Run("seed 1 produces a known 17-byte fixture (non-aligned tail)", func(t *testing.T) {
		t.Parallel()
		// Golden bytes recorded from pcg.New(1).Read(make([]byte, 17)).
		// 17 = 2*8 + 1; exercises the tail-byte branch where the
		// final Uint64 only contributes one byte to the output.
		want := mustDecodeHex(t,
			"0329edab29a127995653604a8c07d016f2")
		got := make([]byte, len(want))
		_, _ = pcg.New(rand.Seed(1)).Read(got)
		if !bytes.Equal(got, want) {
			t.Fatalf("got  %x\nwant %x", got, want)
		}
	})
}

// TestZeroAlloc enforces the documented "Zero alloc" contracts on
// pcg.Rand's hot-path methods. testing.AllocsPerRun uses a
// process-global malloc counter, so this test does not call
// t.Parallel.
func TestZeroAlloc(t *testing.T) {
	r := pcg.New(rand.Seed(1))
	buf := make([]byte, 64)

	cases := []struct {
		name string
		fn   func()
	}{
		{"Uint64", func() { _ = r.Uint64() }},
		{"Read", func() { _, _ = r.Read(buf) }},
		{"Seed", func() { _ = r.Seed() }},
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
	r := pcg.New(rand.Seed(1))
	b.ReportAllocs()
	b.SetBytes(8)
	for b.Loop() {
		_ = r.Uint64()
	}
}

func BenchmarkRead(b *testing.B) {
	r := pcg.New(rand.Seed(1))
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"8B", 8},
		{"64B", 64},
		{"4K", 4096},
		{"64K", 65536},
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
