// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"bytes"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/arena"
	"go.thesmos.sh/core/errs"
)

const (
	// slabSize is the size of the slabs of most cases: room for two slices
	// of the class 512.
	slabSize = 1024

	// mark is the byte that a case writes into a slice to find the slab
	// that contains it.
	mark = 0xa5

	// benchRuns is the number of calls over which a benchmark averages the
	// allocations that it checks.
	benchRuns = 100
)

// Sinks keep the results of the benchmarks alive.
var (
	sinkBytes []byte
	sinkSize  int
	errSink   error
)

// slabLens returns the length of every slab of s, in the order of All.
func slabLens(s *arena.Slabs) []int {
	var lens []int
	for slab := range s.All() {
		lens = append(lens, len(slab))
	}

	return lens
}

// slabsWith returns the positions, in the order of All, of the slabs of s
// that contain the byte v.
func slabsWith(s *arena.Slabs, v byte) []int {
	var at []int

	i := 0
	for slab := range s.All() {
		if bytes.IndexByte(slab, v) != -1 {
			at = append(at, i)
		}

		i++
	}

	return at
}

// first returns the address of the first byte of the memory of b, a slice
// of capacity 1 or more.
func first(b []byte) *byte {
	return &b[:1][0]
}

// zero reports whether every byte of b up to its capacity is zero.
func zero(b []byte) bool {
	return bytes.Count(b[:cap(b)], []byte{0}) == cap(b)
}

func TestSlabs(t *testing.T) {
	t.Parallel()

	t.Run("NewSlabs", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Slabs whose slabs are slabSize bytes", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			s.Alloc(1)
			testkit.Equal(t, slabLens(s), []int{slabSize}, "the slab must have slabSize bytes")
		})

		t.Run("returns a Slabs whose slabs are MinClass bytes for a smaller slabSize", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(arena.MinClass - 1)
			s.Alloc(1)
			testkit.Equal(t, slabLens(s), []int{arena.MinClass}, "the slab must have MinClass bytes")
		})

		t.Run("returns a Slabs without a slab", func(t *testing.T) {
			t.Parallel()
			testkit.Len(t, slabLens(arena.NewSlabs(slabSize)), 0, "NewSlabs must allocate no slab")
		})
	})

	t.Run("Alloc", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			give  int
			class int
		}{
			{name: "returns 1 zeroed byte of the class 64", give: 1, class: 64},
			{name: "returns 63 zeroed bytes of the class 64", give: 63, class: 64},
			{name: "returns 64 zeroed bytes of the class 64", give: 64, class: 64},
			{name: "returns 65 zeroed bytes of the class 128", give: 65, class: 128},
			{name: "returns 512 zeroed bytes of the class 512", give: 512, class: 512},
			{name: "returns 513 zeroed bytes of the class 1024", give: 513, class: 1024},
			{name: "returns 1025 zeroed bytes of the class 2048", give: 1025, class: 2048},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				b := arena.NewSlabs(slabSize).Alloc(tt.give)
				testkit.Len(t, b, tt.give, "Alloc must return n bytes")
				testkit.Equal(t, cap(b), tt.class, "the capacity must be the size class of n")
				testkit.True(t, zero(b), "the bytes of the class must be zero")
			})
		}

		t.Run("returns nil for no bytes", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			testkit.True(t, s.Alloc(0) == nil, "Alloc must return nil for 0 bytes")
			testkit.Len(t, slabLens(s), 0, "Alloc must allocate no slab for 0 bytes")
		})

		t.Run("returns nil for a negative length", func(t *testing.T) {
			t.Parallel()
			testkit.True(t, arena.NewSlabs(slabSize).Alloc(-1) == nil, "Alloc must return nil for -1 bytes")
		})

		t.Run("returns slices of one slab until the slab has no room", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			s.Alloc(512)
			s.Alloc(512)
			testkit.Equal(t, slabLens(s), []int{slabSize}, "two slices of 512 bytes must fill one slab")
			s.Alloc(1)
			testkit.Equal(t, slabLens(s), []int{slabSize, slabSize}, "a third slice must take a new slab")
		})

		t.Run("returns slices whose classes do not overlap", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			b, c := s.Alloc(10), s.Alloc(10)
			b = b[:cap(b)]
			for i := range b {
				b[i] = mark
			}
			testkit.True(t, zero(c), "a write to the class of one slice must not reach another slice")
		})

		t.Run("returns a slice of a slab of its own class for a class larger than a slab", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			b := s.Alloc(1500)
			testkit.Equal(t, cap(b), 2048, "the capacity must be the class 2048")
			testkit.Equal(t, slabLens(s), []int{2048}, "the slab must have the size of the class")
			s.Alloc(1)
			testkit.Equal(t, slabLens(s), []int{2048, slabSize}, "the next slice must take a slab of slabSize")
		})

		t.Run("returns the rest of a slab without room for a class to a smaller class", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			for _, n := range []int{512, 256, 128, 64} {
				s.Alloc(n)
			}
			s.Alloc(slabSize)
			b := s.Alloc(arena.MinClass)
			b[0] = mark
			testkit.Equal(t, slabLens(s), []int{slabSize, slabSize}, "the last 64 bytes of the first slab must serve")
			testkit.Equal(t, slabsWith(s, mark), []int{0}, "the slice must be in the first slab")
		})

		t.Run("returns the rest of a slab in the largest classes that fit", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			s.Alloc(64)
			s.Alloc(slabSize)
			for _, n := range []int{512, 256, 128, 64} {
				b := s.Alloc(n)
				b[0] = mark
			}
			testkit.Equal(t, slabLens(s), []int{slabSize, slabSize}, "the rest of the first slab must serve 4 classes")
			testkit.Equal(t, slabsWith(s, mark), []int{0}, "every slice must be in the first slab")
		})

		t.Run("returns the zero Slabs's slices from slabs of DefaultSlabSize bytes", func(t *testing.T) {
			t.Parallel()
			var s arena.Slabs
			s.Alloc(1)
			testkit.Equal(t, slabLens(&s), []int{arena.DefaultSlabSize}, "the slab must have DefaultSlabSize bytes")
		})
	})

	t.Run("Free", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps a slice for the next Alloc of its class", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			b := s.Alloc(100)
			testkit.NoError(t, s.Free(b), "Free must take the slice back")
			c := s.Alloc(120)
			testkit.True(t, first(c) == first(b), "Alloc must return the memory of the freed slice")
			testkit.Equal(t, cap(c), 128, "the slice must keep the capacity of its class")
			testkit.Len(t, slabLens(s), 1, "Alloc must take no new slab")
		})

		t.Run("keeps each slice for one Alloc", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			freed := map[*byte]bool{}
			for range 3 {
				b := s.Alloc(arena.MinClass)
				freed[first(b)] = true
				testkit.NoError(t, s.Free(b), "Free must take the slice back")
			}
			testkit.Len(t, freed, 1, "each Alloc must take the slice of the Free before it")
			for range 3 {
				b := s.Alloc(arena.MinClass)
				freed[first(b)] = true
			}
			testkit.Len(t, freed, 3, "Alloc must return a freed slice once")
		})

		t.Run("keeps the slices of one class apart", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			held := make([][]byte, 5)
			for i := range held {
				held[i] = s.Alloc(arena.MinClass)
			}
			for _, b := range held[:4] {
				testkit.NoError(t, s.Free(b), "Free must take the slice back")
			}
			got := map[*byte]bool{first(held[4]): true}
			for range 5 {
				got[first(s.Alloc(arena.MinClass))] = true
			}
			testkit.Len(t, got, 6, "no two slices in use may share memory")
		})

		t.Run("zeroes the whole class of the slice", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			b := s.Alloc(10)
			b = b[:cap(b)]
			for i := range b {
				b[i] = mark
			}
			testkit.NoError(t, s.Free(b[:10]), "Free must take the slice back")
			testkit.Len(t, slabsWith(s, mark), 0, "no slab may contain a byte of the freed slice")
			testkit.True(t, zero(s.Alloc(64)), "Alloc must return zeroed bytes")
		})

		t.Run("returns nil for a slice of capacity 0", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			s.Alloc(1)
			testkit.NoError(t, s.Free(nil), "Free must accept nil")
			testkit.Equal(t, s.InUse(), arena.MinClass, "Free of nil must change nothing")
		})

		refused := []struct {
			give func(s *arena.Slabs) []byte
			name string
		}{
			{
				name: "returns ErrSizeClass for a slice resliced to a smaller capacity",
				give: func(s *arena.Slabs) []byte { return s.Alloc(100)[:10:100] },
			},
			{
				name: "returns ErrSizeClass for a power of two below MinClass",
				give: func(*arena.Slabs) []byte { return make([]byte, arena.MinClass/2) },
			},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s := arena.NewSlabs(slabSize)
				b := tt.give(s)
				before := s.InUse()
				err := s.Free(b)
				testkit.ErrorIs(t, err, arena.ErrSizeClass, "Free must refuse the slice")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.Equal(t, s.InUse(), before, "Free must change nothing with an error")
			})
		}
	})

	t.Run("InUse", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the sum of the classes that Alloc returned and Free has not taken back", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			b := s.Alloc(1)
			s.Alloc(100)
			testkit.Equal(t, s.InUse(), 64+128, "InUse must sum the classes in use")
			testkit.NoError(t, s.Free(b), "Free must take the slice back")
			testkit.Equal(t, s.InUse(), 128, "InUse must drop the class that Free took back")
			s.Alloc(10)
			testkit.Equal(t, s.InUse(), 64+128, "InUse must count a reused slice")
		})

		t.Run("returns 0 for the zero Slabs", func(t *testing.T) {
			t.Parallel()
			var s arena.Slabs
			testkit.Equal(t, s.InUse(), 0, "the zero Slabs has nothing in use")
		})
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every slab in the order of its allocation", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			s.Alloc(slabSize)
			s.Alloc(4000)
			s.Alloc(1)
			testkit.Equal(t, slabLens(s), []int{slabSize, 4096, slabSize}, "All must yield each slab once, in order")
		})

		t.Run("yields slabs whose capacity is their length", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			s.Alloc(1)
			for slab := range s.All() {
				testkit.Equal(t, cap(slab), len(slab), "an append to a slab must not write into the Slabs")
			}
		})

		t.Run("yields nothing for the zero Slabs", func(t *testing.T) {
			t.Parallel()
			var s arena.Slabs
			testkit.Len(t, slabLens(&s), 0, "the zero Slabs has no slab")
		})
	})
}

func BenchmarkSlabs(b *testing.B) {
	b.Run("Alloc and Free", func(b *testing.B) {
		s := arena.NewSlabs(slabSize)
		testkit.NoError(b, s.Free(s.Alloc(100)), "Free must take the slice back")
		benchAllocs(b, 0, func() {
			sinkBytes = s.Alloc(100)
			errSink = s.Free(sinkBytes)
		})
	})

	b.Run("Alloc of a value of 1 MiB and Free", func(b *testing.B) {
		s := arena.NewSlabs(arena.DefaultSlabSize)
		testkit.NoError(b, s.Free(s.Alloc(arena.DefaultSlabSize)), "Free must take the slice back")
		benchAllocs(b, 0, func() {
			sinkBytes = s.Alloc(arena.DefaultSlabSize)
			errSink = s.Free(sinkBytes)
		})
	})

	b.Run("Alloc from a new slab", func(b *testing.B) {
		benchAllocs(b, 2, func() {
			s := arena.NewSlabs(slabSize)
			sinkBytes = s.Alloc(1)
		})
	})

	b.Run("InUse", func(b *testing.B) {
		s := arena.NewSlabs(slabSize)
		s.Alloc(1)
		benchAllocs(b, 0, func() { sinkSize = s.InUse() })
	})

	b.Run("All", func(b *testing.B) {
		s := arena.NewSlabs(slabSize)
		s.Alloc(1)
		benchAllocs(b, 0, func() {
			for slab := range s.All() {
				sinkSize = len(slab)
			}
		})
	})
}

// benchAllocs reports the cost of call, and fails when call does not
// allocate want times per call.
func benchAllocs(b *testing.B, want float64, call func()) {
	b.Helper()

	if allocs := testing.AllocsPerRun(benchRuns, call); allocs != want {
		b.Fatalf("allocates %v times per call, want %v", allocs, want)
	}

	b.ReportAllocs()
	for b.Loop() {
		call()
	}
}
