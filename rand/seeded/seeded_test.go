// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package seeded_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
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

	t.Run("same seed produces identical byte streams", func(t *testing.T) {
		t.Parallel()
		a, b := seeded.New(rand.Seed(42)), seeded.New(rand.Seed(42))
		ba, bb := make([]byte, 256), make([]byte, 256)
		_, _ = a.Read(ba)
		_, _ = b.Read(bb)
		if !bytes.Equal(ba, bb) {
			t.Fatal("streams diverged for identical seeds")
		}
	})

	t.Run("same seed produces identical Uint64 streams", func(t *testing.T) {
		t.Parallel()
		a, b := seeded.New(rand.Seed(7)), seeded.New(rand.Seed(7))
		for i := range 100 {
			va, vb := a.Uint64(), b.Uint64()
			if va != vb {
				t.Fatalf("step %d: %d != %d", i, va, vb)
			}
		}
	})

	t.Run("different seeds produce different streams", func(t *testing.T) {
		t.Parallel()
		a, b := seeded.New(rand.Seed(1)), seeded.New(rand.Seed(2))
		ba, bb := make([]byte, 32), make([]byte, 32)
		_, _ = a.Read(ba)
		_, _ = b.Read(bb)
		if bytes.Equal(ba, bb) {
			t.Fatal("distinct seeds produced identical streams")
		}
	})

	t.Run("byte-seeded instances with equal input produce identical streams", func(t *testing.T) {
		t.Parallel()
		seed := []byte("test-seed-bytes-2026")
		a, b := seeded.NewFromBytes(seed), seeded.NewFromBytes(seed)
		ba, bb := make([]byte, 128), make([]byte, 128)
		_, _ = a.Read(ba)
		_, _ = b.Read(bb)
		if !bytes.Equal(ba, bb) {
			t.Fatal("byte-seeded streams diverged for identical seed bytes")
		}
	})
}

func TestSeed(t *testing.T) {
	t.Parallel()

	t.Run("Seed reports construction-time seed for int-seeded", func(t *testing.T) {
		t.Parallel()
		const s = rand.Seed(99)
		if got := seeded.New(s).Seed(); got != s {
			t.Fatalf("Seed: got %d, want %d", got, s)
		}
	})

	t.Run("Seed reports SeedUnspecified for byte-seeded", func(t *testing.T) {
		t.Parallel()
		got := seeded.NewFromBytes([]byte("anything")).Seed()
		if got != rand.SeedUnspecified {
			t.Fatalf("Seed: got %d, want SeedUnspecified", got)
		}
	})
}

func TestRead(t *testing.T) {
	t.Parallel()

	t.Run("split reads equal a single read of the same length", func(t *testing.T) {
		t.Parallel()
		// Behavioural contract: a 100-byte Read produces the
		// same bytes as ten 10-byte Reads. Exercises the
		// buffered-block carry-over between calls.
		single := make([]byte, 100)
		_, _ = seeded.New(rand.Seed(2)).Read(single)

		split := make([]byte, 100)
		r := seeded.New(rand.Seed(2))
		for i := 0; i < 100; i += 10 {
			_, _ = r.Read(split[i : i+10])
		}
		if !bytes.Equal(single, split) {
			t.Fatalf("split reads diverged from single Read:\n single=%x\n split =%x", single, split)
		}
	})

	t.Run("each HMAC-SHA-256 block is distinct", func(t *testing.T) {
		t.Parallel()
		// The block size is 32 bytes. Three consecutive blocks
		// must differ — a stalled counter would yield
		// b1 == b2 == b3.
		buf := make([]byte, 96)
		_, _ = seeded.New(rand.Seed(123)).Read(buf)
		b1, b2, b3 := buf[0:32], buf[32:64], buf[64:96]
		if bytes.Equal(b1, b2) || bytes.Equal(b2, b3) {
			t.Fatal("consecutive HMAC blocks were equal — counter did not advance")
		}
	})

	t.Run("seed 0xabcd produces a known 64-byte fixture", func(t *testing.T) {
		t.Parallel()
		// Golden bytes recorded from seeded.New(0xabcd).Read(64).
		// Provides a cross-version stability check; pinned because
		// HMAC-SHA-256 is deterministic and the construction
		// (key = big-endian seed, counter encoded big-endian) is
		// part of the public contract — see package godoc.
		want := mustDecodeHex(t,
			"c1dfde25ff46bc350cbd30fd529c74c4e4e820f2e2ea56dff62b4e1b60e75c56"+
				"df4a9b70aa92cd69a9a3d37de3ff9a5baa97ee15640101987fd736296aa2cfcc")
		got := make([]byte, len(want))
		_, _ = seeded.New(rand.Seed(0xabcd)).Read(got)
		if !bytes.Equal(got, want) {
			t.Fatalf("got  %x\nwant %x", got, want)
		}
	})
}

// TestZeroAlloc enforces the documented "Zero alloc" contracts on
// seeded.Rand's hot-path methods. testing.AllocsPerRun uses a
// process-global malloc counter, so this test does not call
// t.Parallel.
func TestZeroAlloc(t *testing.T) {
	r := seeded.New(rand.Seed(1))
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
