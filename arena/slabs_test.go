// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"bytes"
	"math/bits"
	"reflect"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/arena"
	"go.thesmos.sh/core/errs"
)

// slabSize is the size of the slabs of most cases: room for two slices of
// the class 512.
const slabSize = 1024

// slabsContract is the contract of slabsSteps, which TestSlabs and
// FuzzSlabs check.
const slabsContract = "every slice in use must keep its own bytes, and InUse must sum their classes"

// allocated is what a case of Alloc observes of the slice that it returns.
type allocated struct {
	len, cap int
	zero     bool
}

// held is a slice in use in slabsSteps, and the byte that fills its class.
type held struct {
	b []byte
	v byte
}

func TestSlabs(t *testing.T) {
	t.Parallel()

	t.Run("NewSlabs", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Slabs whose slabs are slabSize bytes", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			s.Alloc(1)
			assert.Equal(t, slabLens(s), []int{slabSize}, "the slab must have slabSize bytes")
		})

		t.Run("returns a Slabs whose slabs are MinClass bytes for a smaller slabSize", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(arena.MinClass - 1)
			s.Alloc(1)
			assert.Equal(t, slabLens(s), []int{arena.MinClass}, "the slab must have MinClass bytes")
		})

		t.Run("returns a Slabs without a slab", func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, slabLens(arena.NewSlabs(slabSize)), "NewSlabs must allocate no slab")
		})
	})

	t.Run("Alloc", func(t *testing.T) {
		t.Parallel()

		t.Run("returns n zero bytes of the size class of n", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(n int) allocated {
				b := arena.NewSlabs(slabSize).Alloc(n)

				return allocated{len: len(b), cap: cap(b), zero: bytes.Count(b[:cap(b)], []byte{0}) == cap(b)}
			}, func(n int) allocated {
				return allocated{len: n, cap: max(arena.MinClass, 1<<bits.Len(uint(n-1))), zero: true}
			}, "Alloc must return n zero bytes whose capacity is the smallest power of two of at least MinClass",
				prop.Using(prop.Integer(1, 4*slabSize)), prop.Example(1), prop.Example(63), prop.Example(64),
				prop.Example(65), prop.Example(512), prop.Example(513), prop.Example(1025))
		})

		t.Run("returns nil for no bytes", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			prop.Nil(t, s.Alloc, "Alloc must return nil for a length of zero or less",
				prop.Using(prop.Integer(-64, 0)), prop.Example(0), prop.Example(-1))
			assert.Empty(t, slabLens(s), "Alloc must allocate no slab for no bytes")
		})

		t.Run("returns slices of one slab until the slab has no room", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			s.Alloc(512)
			s.Alloc(512)
			assert.Equal(t, slabLens(s), []int{slabSize}, "two slices of 512 bytes must fill one slab")
			s.Alloc(1)
			assert.Equal(t, slabLens(s), []int{slabSize, slabSize}, "a third slice must take a new slab")
		})

		t.Run("returns slices whose classes do not overlap", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			b, c := s.Alloc(10), s.Alloc(10)
			copy(b[:cap(b)], bytes.Repeat([]byte{fill}, cap(b)))
			assert.Equal(t, c[:cap(c)], make([]byte, cap(c)),
				"a write to the class of one slice must not reach another")
		})

		t.Run("returns a slice of a slab of its own class for a class larger than a slab", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			b := s.Alloc(1500)
			assert.Equal(t, cap(b), 2048, "the capacity must be the class 2048")
			assert.Equal(t, slabLens(s), []int{2048}, "the slab must have the size of the class")
			s.Alloc(1)
			assert.Equal(t, slabLens(s), []int{2048, slabSize}, "the next slice must take a slab of slabSize")
		})

		t.Run("returns the rest of a slab without room for a class to a smaller class", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			for _, n := range []int{512, 256, 128, 64} {
				s.Alloc(n)
			}
			s.Alloc(slabSize)
			b := s.Alloc(arena.MinClass)
			b[0] = fill
			assert.Equal(t, slabLens(s), []int{slabSize, slabSize}, "the last 64 bytes of the first slab must serve")
			assert.Equal(t, slabsWith(s, fill), []int{0}, "the slice must be in the first slab")
		})

		t.Run("returns the rest of a slab in the largest classes that fit", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			s.Alloc(64)
			s.Alloc(slabSize)
			for _, n := range []int{512, 256, 128, 64} {
				b := s.Alloc(n)
				b[0] = fill
			}
			assert.Equal(t, slabLens(s), []int{slabSize, slabSize}, "the rest of the first slab must serve 4 classes")
			assert.Equal(t, slabsWith(s, fill), []int{0}, "every slice must be in the first slab")
		})

		t.Run("returns the zero Slabs's slices from slabs of DefaultSlabSize bytes", func(t *testing.T) {
			t.Parallel()
			var s arena.Slabs
			s.Alloc(1)
			assert.Equal(t, slabLens(&s), []int{arena.DefaultSlabSize}, "the slab must have DefaultSlabSize bytes")
		})
	})

	t.Run("Free", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps a slice for the next Alloc of its class", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			b := s.Alloc(100)
			assert.NoError(t, s.Free(b), "Free must take the slice back")
			c := s.Alloc(120)
			assert.Equal(t, reflect.ValueOf(c).Pointer(), reflect.ValueOf(b).Pointer(),
				"Alloc must return the memory of the freed slice")
			assert.Equal(t, cap(c), 128, "the slice must keep the capacity of its class")
			assert.Length(t, slabLens(s), 1, "Alloc must take no new slab")
		})

		t.Run("keeps each slice for one Alloc", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			b := s.Alloc(arena.MinClass)
			assert.NoError(t, s.Free(b), "Free must take the slice back")
			assert.NoDuplicates(t, func() ([]uintptr, error) {
				addresses := make([]uintptr, 0, 3)
				for range 3 {
					addresses = append(addresses, reflect.ValueOf(s.Alloc(arena.MinClass)).Pointer())
				}

				return addresses, nil
			}, "Alloc must return a freed slice once")
		})

		t.Run("keeps the slices of one class apart", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			kept := make([][]byte, 5)
			for i := range kept {
				kept[i] = s.Alloc(arena.MinClass)
			}
			for _, b := range kept[:4] {
				assert.NoError(t, s.Free(b), "Free must take the slice back")
			}
			assert.NoDuplicates(t, func() ([]uintptr, error) {
				addresses := make([]uintptr, 0, 6)
				addresses = append(addresses, reflect.ValueOf(kept[4]).Pointer())
				for range 5 {
					addresses = append(addresses, reflect.ValueOf(s.Alloc(arena.MinClass)).Pointer())
				}

				return addresses, nil
			}, "no two slices in use may share memory")
		})

		t.Run("zeroes the whole class of the slice", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			b := s.Alloc(10)
			copy(b[:cap(b)], bytes.Repeat([]byte{fill}, cap(b)))
			assert.NoError(t, s.Free(b), "Free must take the slice back")
			assert.Empty(t, slabsWith(s, fill), "no slab may contain a byte of the freed slice")
		})

		t.Run("returns nil for a slice of capacity 0", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			s.Alloc(1)

			var err error
			assert.Pure(t, s.InUse, func() { err = s.Free(nil) }, "Free of nil must change nothing")
			assert.NoError(t, err, "Free must return nil for nil")
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

				var err error
				assert.Pure(t, s.InUse, func() { err = s.Free(b) }, "Free must change nothing with an error")
				assert.ErrorIs(t, err, arena.ErrSizeClass, "Free must refuse the slice")
				assert.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
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
			assert.Equal(t, s.InUse(), 64+128, "InUse must sum the classes in use")
			assert.NoError(t, s.Free(b), "Free must take the slice back")
			assert.Equal(t, s.InUse(), 128, "InUse must drop the class that Free took back")
			s.Alloc(10)
			assert.Equal(t, s.InUse(), 64+128, "InUse must count a reused slice")
		})

		t.Run("returns 0 for the zero Slabs", func(t *testing.T) {
			t.Parallel()
			var s arena.Slabs
			assert.Equal(t, s.InUse(), 0, "the zero Slabs has nothing in use")
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
			assert.Equal(t, slabLens(s), []int{slabSize, 4096, slabSize}, "All must yield each slab once, in order")
		})

		t.Run("yields slabs whose capacity is their length", func(t *testing.T) {
			t.Parallel()
			s := arena.NewSlabs(slabSize)
			s.Alloc(1)
			for slab := range s.All() {
				assert.Equal(t, cap(slab), len(slab), "an append to a slab must not write into the Slabs")
			}
		})

		t.Run("yields nothing for the zero Slabs", func(t *testing.T) {
			t.Parallel()
			var s arena.Slabs
			assert.Empty(t, slabLens(&s), "the zero Slabs has no slab")
		})
	})

	t.Run("keeps the slices in use apart under any sequence of Alloc and Free", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, slabsContract, slabsSteps)
	})
}

// FuzzSlabs checks the machine of slabsSteps on the call sequences that a
// fuzzer finds.
func FuzzSlabs(f *testing.F) {
	prop.Fuzz(f, slabsContract, slabsSteps)
}

// TestSlabsAllocs checks the allocation contract of each method of a Slabs.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestSlabsAllocs(t *testing.T) {
	t.Run("Alloc and Free", func(t *testing.T) {
		s := arena.NewSlabs(slabSize)

		var err error
		expect.MaxAllocs(t, func() { err = s.Free(s.Alloc(100)) }, 0,
			"an Alloc of a class that Free took back must not allocate")
		assert.NoError(t, err, "the test must measure a Free that succeeds")
	})

	t.Run("Alloc and Free of 1 MiB", func(t *testing.T) {
		s := arena.NewSlabs(arena.DefaultSlabSize)

		var err error
		expect.MaxAllocs(t, func() { err = s.Free(s.Alloc(arena.DefaultSlabSize)) }, 0,
			"an Alloc of a class that Free took back must not allocate")
		assert.NoError(t, err, "the test must measure a Free that succeeds")
	})

	t.Run("Alloc from a new slab", func(t *testing.T) {
		var got []byte
		expect.MaxAllocsWithSetup(t, func() *arena.Slabs { return arena.NewSlabs(slabSize) },
			func(s *arena.Slabs) { got = s.Alloc(1) }, 2, "a new slab must cost the slab and the index of slabs")
		assert.Length(t, got, 1, "the test must measure an Alloc of one byte")
	})

	t.Run("InUse", func(t *testing.T) {
		s := arena.NewSlabs(slabSize)
		s.Alloc(1)

		var got int
		expect.MaxAllocs(t, func() { got = s.InUse() }, 0, "InUse must not allocate")
		assert.Equal(t, got, arena.MinClass, "the test must measure one slice in use")
	})

	t.Run("All", func(t *testing.T) {
		s := arena.NewSlabs(slabSize)
		s.Alloc(1)

		var got int
		expect.MaxAllocs(t, func() {
			for slab := range s.All() {
				got = len(slab)
			}
		}, 0, "All must not allocate")
		assert.Equal(t, got, slabSize, "the test must measure the slab")
	})
}

// BenchmarkSlabs reports the cost of each method of a Slabs, and fails when
// a method allocates more than the allocation contract of [arena.Slabs]
// allows.
func BenchmarkSlabs(b *testing.B) {
	b.Run("Alloc", func(b *testing.B) {
		b.Run("and Free", func(b *testing.B) {
			s := arena.NewSlabs(slabSize)
			assert.NoError(b, s.Free(s.Alloc(100)), "Free must take the slice back")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = s.Free(s.Alloc(100))
			}

			assert.NoError(b, err, "the benchmark must measure a Free that succeeds")
		})

		b.Run("of 1 MiB and Free", func(b *testing.B) {
			s := arena.NewSlabs(arena.DefaultSlabSize)
			assert.NoError(b, s.Free(s.Alloc(arena.DefaultSlabSize)), "Free must take the slice back")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = s.Free(s.Alloc(arena.DefaultSlabSize))
			}

			assert.NoError(b, err, "the benchmark must measure a Free that succeeds")
		})

		b.Run("from a new slab", func(b *testing.B) {
			var got []byte

			c := bench.Start(b).MaxAllocs(2)
			defer c.End()

			for c.Loop() {
				got = arena.NewSlabs(slabSize).Alloc(1)
			}

			assert.Length(b, got, 1, "the benchmark must measure an Alloc of one byte")
		})
	})

	b.Run("InUse", func(b *testing.B) {
		s := arena.NewSlabs(slabSize)
		s.Alloc(1)

		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = s.InUse()
		}

		assert.Equal(b, got, arena.MinClass, "the benchmark must measure one slice in use")
	})

	b.Run("All", func(b *testing.B) {
		s := arena.NewSlabs(slabSize)
		s.Alloc(1)

		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			for slab := range s.All() {
				got = len(slab)
			}
		}

		assert.Equal(b, got, slabSize, "the benchmark must measure the slab")
	})
}

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

// slabsSteps runs the steps of a machine over Alloc and Free of a Slabs.
// Each Alloc fills the whole class of its slice with a byte of its own, so
// two slices in use that share memory change each other's bytes, and the
// invariant checks every slice in use and InUse after each step.
func slabsSteps(c *prop.Case) {
	s := arena.NewSlabs(slabSize)

	var (
		inUse []held
		total int
		next  byte
	)

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:  "Alloc",
				Input: func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer(1, 3*slabSize), "n") },
				Run: func(c *prop.Case, _ int, in any) {
					n := in.(int)
					b := s.Alloc(n)
					assert.Length(c, b, n, "Alloc must return n bytes")
					assert.Equal(c, cap(b), max(arena.MinClass, 1<<bits.Len(uint(n-1))),
						"the capacity must be the size class of n")
					assert.Equal(c, b[:cap(b)], make([]byte, cap(b)), "the class of the slice must be zero")

					next = next%254 + 1
					b = b[:cap(b)]
					copy(b, bytes.Repeat([]byte{next}, len(b)))
					inUse = append(inUse, held{b: b, v: next})
					total += len(b)
				},
			},
			{
				Name:    "Free",
				Enabled: func(struct{}) bool { return len(inUse) > 0 },
				Input:   func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer(0, len(inUse)-1), "slice") },
				Run: func(c *prop.Case, _ int, in any) {
					i := in.(int)
					assert.NoError(c, s.Free(inUse[i].b), "Free must take back a slice that Alloc returned")
					total -= len(inUse[i].b)
					inUse = slices.Delete(inUse, i, i+1)
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			assert.Equal(c, s.InUse(), total, "InUse must sum the classes in use")
			for _, h := range inUse {
				assert.Equal(c, bytes.Count(h.b, []byte{h.v}), len(h.b), "no other slice may write into a slice in use")
			}
		},
	})
}
