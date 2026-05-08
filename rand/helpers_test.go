// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package rand_test

import (
	"encoding/binary"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/fixed"
	"go.thesmos.sh/core/rand/pcg"
)

// scriptedRand returns a pre-set sequence of Uint64 values, looping
// once exhausted. Used to force specific algorithmic branches
// (e.g. Lemire's rejection loop) that probabilistic inputs cannot
// reliably trigger.
type scriptedRand struct {
	values []uint64
	pos    int
}

func (s *scriptedRand) Uint64() uint64 {
	v := s.values[s.pos%len(s.values)]
	s.pos++
	return v
}

func (s *scriptedRand) Read(p []byte) (int, error) {
	var chunk [8]byte
	written := 0
	for written < len(p) {
		binary.LittleEndian.PutUint64(chunk[:], s.Uint64())
		written += copy(p[written:], chunk[:])
	}
	return len(p), nil
}

func TestFloat64(t *testing.T) {
	t.Parallel()

	t.Run("returns 0.0 when Uint64 returns 0", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, rand.Float64(fixed.New(0)), 0.0,
			"Float64(0) must return 0.0")
	})

	t.Run("stays in [0.0, 1.0)", func(t *testing.T) {
		t.Parallel()
		r := pcg.New(rand.Seed(42))
		for range 1000 {
			got := rand.Float64(r)
			testkit.True(t, got >= 0.0 && got < 1.0,
				"Float64 must stay in [0.0, 1.0)")
		}
	})
}

func TestShuffle(t *testing.T) {
	t.Parallel()

	t.Run("no-op for n <= 1", func(t *testing.T) {
		t.Parallel()
		// Construct a fixed Rand that would deterministically swap
		// indices if Shuffle ran the loop body. Verify swap is
		// never called.
		called := false
		swap := func(_, _ int) { called = true }
		rand.Shuffle(fixed.New(0), 0, swap)
		rand.Shuffle(fixed.New(0), 1, swap)
		testkit.False(t, called, "Shuffle must not invoke swap for n <= 1")
	})

	t.Run("permutes the input", func(t *testing.T) {
		t.Parallel()
		const n = 100
		out := make([]int, n)
		for i := range n {
			out[i] = i
		}
		r := pcg.New(rand.Seed(42))
		rand.Shuffle(r, n, func(i, j int) { out[i], out[j] = out[j], out[i] })

		// Result must contain every index in [0, n) exactly once.
		seen := make(map[int]bool, n)
		for _, v := range out {
			testkit.False(t, seen[v], "Shuffle must not produce duplicate index")
			seen[v] = true
		}
		testkit.Equal(t, len(seen), n, "Shuffle must preserve cardinality")
	})

	t.Run("deterministic for the same seed", func(t *testing.T) {
		t.Parallel()
		const n = 50
		runs := make([][]int, 2)
		for run := range runs {
			runs[run] = make([]int, n)
			for i := range n {
				runs[run][i] = i
			}
			r := pcg.New(rand.Seed(123))
			rand.Shuffle(r, n, func(i, j int) {
				runs[run][i], runs[run][j] = runs[run][j], runs[run][i]
			})
		}
		testkit.Equal(t, runs[0], runs[1],
			"Shuffle must produce identical permutations for the same seed")
	})
}

func TestUint64N(t *testing.T) {
	t.Parallel()

	t.Run("returns 0 when n is 0", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, rand.Uint64N(fixed.New(0xFFFFFFFFFFFFFFFF), 0), uint64(0),
			"Uint64N(_, 0) must return 0")
	})

	t.Run("returns 0 when n is 1", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, rand.Uint64N(fixed.New(0xFFFFFFFFFFFFFFFF), 1), uint64(0),
			"Uint64N(_, 1) must return 0")
	})

	t.Run("result is strictly less than n", func(t *testing.T) {
		t.Parallel()
		r := pcg.New(rand.Seed(7))
		const n uint64 = 100
		for range 10_000 {
			got := rand.Uint64N(r, n)
			testkit.True(t, got < n, "Uint64N(_, n) must return < n")
		}
	})

	t.Run("rejection loop triggers when first draw is biased", func(t *testing.T) {
		t.Parallel()
		// Lemire's algorithm enters the rejection check when
		// lo < n, and re-draws while lo < thresh = -n % n.
		// For n = 7, thresh = 2. A first draw of 0 yields
		// lo = hi = 0, satisfying both conditions. The second
		// draw must produce lo >= 2 to exit the loop.
		const n uint64 = 7
		// Draw 1: 0 × 7 → lo=0, hi=0 → lo<n and lo<thresh,
		//         loop iterates.
		// Draw 2: 0x1000000000000000 × 7 = 0x7000000000000000
		//         → lo huge, hi=0 → loop exits with hi=0.
		r := &scriptedRand{values: []uint64{0, 0x1000000000000000}}
		testkit.Equal(t, rand.Uint64N(r, n), uint64(0), "Uint64N must return 0")
		testkit.Equal(t, r.pos, 2,
			"rejection loop must consume exactly 2 draws")
	})

	t.Run("non-rejected first draw exits before redrawing", func(t *testing.T) {
		t.Parallel()
		// Construct a draw whose lo straddles the loop boundary
		// `lo < thresh` and `lo == thresh`. The original code
		// uses `<` (exits at lo == thresh); a CONDITIONALS_BOUNDARY
		// mutation to `<=` would re-iterate. Choose a draw that
		// produces lo == thresh exactly so the mutation is
		// observable.
		//
		// For n = 3, thresh = 1. Find d such that d*3 mod 2^64
		// equals 1. The modular inverse of 3 in mod 2^64 is
		// 12297829382473034411 (verified: 3 * x = 2*2^64 + 1).
		const n uint64 = 3
		// Draw 1: d × 3 = 2 × 2^64 + 1 → lo=1, hi=2.
		// Original: lo < thresh (1 < 1) false → exit, return 2.
		// Mutated `<=`: lo <= thresh (1 <= 1) true → redraw.
		// Set draw 2 to 4: 4×3 = 12 → lo=12, hi=0 → exit, return 0.
		r := &scriptedRand{values: []uint64{12297829382473034411, 4}}
		testkit.Equal(t, rand.Uint64N(r, n), uint64(2),
			"rejection loop must use < not <= (returns hi=2 on first draw)")
		testkit.Equal(t, r.pos, 1,
			"rejection loop must not redraw on lo == thresh")
	})
}

func TestShuffleDeterministic(t *testing.T) {
	t.Parallel()

	// Hardcoded golden permutation recorded from
	// pcg.New(rand.Seed(1)) on Go 1.26.x. Any drift in Shuffle's
	// index arithmetic (the `i+1` upper bound, the swap order)
	// produces a different permutation. The fixture is regenerated
	// only if the upstream math/rand/v2 PCG output changes.
	want := []int{5, 3, 1, 2, 6, 7, 0, 4}

	out := make([]int, len(want))
	for i := range out {
		out[i] = i
	}
	rand.Shuffle(pcg.New(rand.Seed(1)), len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})

	testkit.Equal(t, out, want,
		"Shuffle output must match the golden permutation")
}

// TestZeroAlloc enforces the documented "Zero alloc" contracts on
// the rand package's helpers. testing.AllocsPerRun uses a
// process-global malloc counter, so this test does not call
// t.Parallel.
func TestZeroAlloc(t *testing.T) {
	r := pcg.New(rand.Seed(1))
	noopSwap := func(_, _ int) {}

	cases := []struct {
		name string
		fn   func()
	}{
		{"Float64", func() { _ = rand.Float64(r) }},
		{"Uint64N", func() { _ = rand.Uint64N(r, 100) }},
		// pcg, not fixed.New(0): Shuffle uses Lemire's rejection
		// sampling, and a constant-zero source would hit the
		// rejection band on every draw and loop forever for
		// n >= 3.
		{"Shuffle", func() { rand.Shuffle(r, 16, noopSwap) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(100, tc.fn),
				float64(0), tc.name+" must be zero-alloc")
		})
	}
}

func BenchmarkFloat64(b *testing.B) {
	r := pcg.New(rand.Seed(1))
	b.ReportAllocs()
	for b.Loop() {
		_ = rand.Float64(r)
	}
}

func BenchmarkUint64N(b *testing.B) {
	r := pcg.New(rand.Seed(1))
	b.ReportAllocs()
	for b.Loop() {
		_ = rand.Uint64N(r, 1024)
	}
}

func BenchmarkShuffle(b *testing.B) {
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"16", 16},
		{"256", 256},
		{"4K", 4096},
	} {
		b.Run(sz.name, func(b *testing.B) {
			r := pcg.New(rand.Seed(1))
			swap := func(_, _ int) {}
			b.ReportAllocs()
			for b.Loop() {
				rand.Shuffle(r, sz.n, swap)
			}
		})
	}
}
