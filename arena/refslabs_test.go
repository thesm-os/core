// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"bytes"
	"flag"
	"math"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/arena"
	"go.thesmos.sh/core/errs"
)

// refSlabsContract is the contract of refSlabsSteps, which TestRefSlabs and
// FuzzRefSlabs check.
const refSlabsContract = "every region in use must keep its own bytes, and InUse must sum their classes"

// childEnv is set in the environment of the child process in which a case
// runs alone.
const childEnv = "CORE_TEST_CHILD"

// heldRef is a region in use in refSlabsSteps: its Ref, its length and the
// byte that fills its class.
type heldRef struct {
	r arena.Ref
	n int
	v byte
}

func TestRefSlabs(t *testing.T) {
	t.Parallel()

	t.Run("NewRefSlabs", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a RefSlabs whose slabs are slabSize bytes", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			s.Alloc(1)
			assert.Equal(t, slabLens(s), []int{slabSize}, "the slab has slabSize bytes")
		})

		t.Run("returns a RefSlabs whose slabs are MinClass bytes for a smaller slabSize", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(arena.MinClass - 1)
			s.Alloc(1)
			assert.Equal(t, slabLens(s), []int{arena.MinClass}, "the slab has MinClass bytes")
		})

		t.Run("returns a RefSlabs whose slabs are at most 2^32 bytes", func(t *testing.T) {
			t.Parallel()
			// The slab takes 4 GiB of address space. The runtime zeroes a large
			// allocation that reuses pages that a collection freed, and so
			// touches every page. The case therefore runs alone in a child
			// process of the test binary, whose heap has freed no pages yet.
			if os.Getenv(childEnv) == "" {
				exe, err := os.Executable()
				assert.NoError(t, err, "the test binary has a path")
				levels := strings.Split(t.Name(), "/")
				for i, level := range levels {
					levels[i] = "^" + regexp.QuoteMeta(level) + "$"
				}
				args := []string{"-test.run=" + strings.Join(levels, "/"), "-test.count=1"}
				if dir := flag.Lookup("test.gocoverdir"); dir != nil && dir.Value.String() != "" {
					args = append(args, "-test.gocoverdir="+dir.Value.String())
				}
				if deadline, ok := t.Deadline(); ok {
					args = append(args, "-test.timeout="+time.Until(deadline).String())
				}
				cmd := exec.CommandContext(t.Context(), exe, args...)
				cmd.Env = append(os.Environ(), childEnv+"=1")
				out, err := cmd.CombinedOutput()
				assert.NoError(t, err, "the case passes in a child process, whose output is:\n"+string(out))

				return
			}
			s := arena.NewRefSlabs(math.MaxInt)
			s.Alloc(1)
			assert.Equal(t, slabLens(s), []int{int(min(uint64(math.MaxInt), 1<<32))},
				"the slab has 2^32 bytes, so every offset fits 32 bits")
		})

		t.Run("returns a RefSlabs without a slab", func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, slabLens(arena.NewRefSlabs(slabSize)), "NewRefSlabs allocates no slab")
		})
	})

	t.Run("Alloc", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a region of n zero bytes of the size class of n", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(n int) allocated {
				s := arena.NewRefSlabs(slabSize)
				b := s.Bytes(s.Alloc(n), n)

				return allocated{len: len(b), cap: cap(b), zero: bytes.Count(b[:cap(b)], []byte{0}) == cap(b)}
			}, func(n int) allocated {
				i, _ := slices.BinarySearch(sizeClasses, n)

				return allocated{len: n, cap: sizeClasses[i], zero: true}
			}, "Alloc returns a region of n zero bytes whose capacity is the smallest size class that contains n",
				prop.Using(prop.Integer(1, 4*slabSize)), prop.Example(1), prop.Example(63), prop.Example(64),
				prop.Example(65), prop.Example(72), prop.Example(73), prop.Example(512), prop.Example(513),
				prop.Example(1025), prop.Example(1070), prop.Example(1536), prop.Example(2049))
		})

		t.Run("returns the zero Ref for no bytes", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			prop.Equal(t, s.Alloc, func(int) arena.Ref { return 0 }, "Alloc returns the zero Ref for no bytes",
				prop.Using(prop.Integer(-64, 0)), prop.Example(0), prop.Example(-1))
			assert.Empty(t, slabLens(s), "Alloc allocates no slab for no bytes")
		})

		t.Run("returns a Ref of the number of its slab plus one and its offset", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			first, second := s.Alloc(1), s.Alloc(1)
			expect.Equal(t, first, arena.Ref(1<<32), "the first region is at offset 0 of the first slab")
			expect.Equal(t, second, arena.Ref(1<<32|arena.MinClass), "the second region follows the first")
		})

		t.Run("returns regions of one slab until the slab has no room", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			s.Alloc(512)
			s.Alloc(512)
			assert.Equal(t, slabLens(s), []int{slabSize}, "two regions of 512 bytes fill one slab")
			s.Alloc(1)
			assert.Equal(t, slabLens(s), []int{slabSize, slabSize}, "a third region takes a new slab")
		})

		t.Run("returns regions whose classes do not overlap", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			b, c := s.Bytes(s.Alloc(10), 10), s.Bytes(s.Alloc(10), 10)
			copy(b[:cap(b)], bytes.Repeat([]byte{fill}, cap(b)))
			assert.Equal(t, c[:cap(c)], make([]byte, cap(c)),
				"a write to the class of one region does not change another")
		})

		t.Run("returns a region at offset 0 of a slab of its own for a class larger than slabSize", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			r := s.Alloc(1500)
			expect.Equal(t, r, arena.Ref(1<<32), "the region is at offset 0 of the first slab")
			expect.Equal(t, slabLens(s), []int{1536}, "the slab has the size of the class")
			s.Alloc(1)
			expect.Equal(t, slabLens(s), []int{1536, slabSize}, "the next region takes a slab of slabSize")
		})

		t.Run("returns the rest of the previous slab for a class that fits in it", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			for _, n := range []int{512, 256, 128, 64} {
				s.Alloc(n)
			}
			s.Alloc(slabSize)
			s.Bytes(s.Alloc(arena.MinClass), arena.MinClass)[0] = fill
			assert.Equal(t, slabLens(s), []int{slabSize, slabSize}, "the last 64 bytes of the first slab serve")
			assert.Equal(t, slabsWith(s, fill), []int{0}, "the region is in the first slab")
		})

		// Alloc(72) leaves 952 bytes of the first slab. The largest class of
		// at most 952 bytes is 896. The last 56 bytes are fewer than MinClass
		// and remain unused.
		t.Run("returns the largest class that fits in the rest of the previous slab", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			s.Alloc(72)
			s.Alloc(slabSize)
			s.Bytes(s.Alloc(896), 896)[0] = fill
			assert.Equal(t, slabLens(s), []int{slabSize, slabSize}, "the rest of the first slab serves the class")
			assert.Equal(t, slabsWith(s, fill), []int{0}, "the region is in the first slab")
		})

		t.Run("returns regions of slabs of DefaultSlabSize bytes for the zero RefSlabs", func(t *testing.T) {
			t.Parallel()
			var s arena.RefSlabs
			s.Alloc(1)
			assert.Equal(t, slabLens(&s), []int{arena.DefaultSlabSize}, "the slab has DefaultSlabSize bytes")
		})
	})

	t.Run("Bytes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the same memory on every call", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			r := s.Alloc(10)
			assert.Equal(t, &s.Bytes(r, 10)[0], &s.Bytes(r, 10)[0], "Bytes returns the memory of the region",
				assert.ByIdentity())
		})

		t.Run("returns a slice of the region's slab", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			copy(s.Bytes(s.Alloc(10), 10), "0123456789")
			assert.Equal(t, slabsWith(s, '9'), []int{0}, "a write to the region shows in its slab")
		})

		refused := []struct {
			give func(s *arena.RefSlabs) (arena.Ref, int)
			name string
		}{
			{
				name: "returns nil for the zero Ref",
				give: func(*arena.RefSlabs) (arena.Ref, int) { return 0, 10 },
			},
			{
				name: "returns nil for a Ref past the last slab",
				give: func(s *arena.RefSlabs) (arena.Ref, int) { return s.Alloc(10) + 1<<32, 10 },
			},
			{
				name: "returns nil for a Ref whose class runs past the end of its slab",
				give: func(s *arena.RefSlabs) (arena.Ref, int) { return s.Alloc(10) + slabSize - arena.MinClass + 8, 10 },
			},
			{
				name: "returns nil for a length below 1",
				give: func(s *arena.RefSlabs) (arena.Ref, int) { return s.Alloc(10), 0 },
			},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s := arena.NewRefSlabs(slabSize)
				r, n := tt.give(s)
				assert.Nil(t, s.Bytes(r, n), "Bytes returns no slice for a region outside the slabs")
			})
		}
	})

	t.Run("Free", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps a region for the next Alloc of its class", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			r := s.Alloc(100)
			assert.NoError(t, s.Free(r, 100), "Free takes the region back")
			expect.Equal(t, s.Alloc(98), r, "Alloc returns the freed region")
			expect.Length(t, slabLens(s), 1, "Alloc takes no new slab")
		})

		t.Run("keeps each region for one Alloc", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			assert.NoError(t, s.Free(s.Alloc(arena.MinClass), arena.MinClass), "Free takes the region back")
			assert.NoDuplicates(t, func() ([]arena.Ref, error) {
				got := make([]arena.Ref, 0, 3)
				for range 3 {
					got = append(got, s.Alloc(arena.MinClass))
				}

				return got, nil
			}, "Alloc returns a freed region once")
		})

		t.Run("keeps the regions of one class apart", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			kept := make([]arena.Ref, 5)
			for i := range kept {
				kept[i] = s.Alloc(arena.MinClass)
			}
			assert.Total(t, func(r arena.Ref) error { return s.Free(r, arena.MinClass) }, kept[:4],
				"Free takes the region back")
			assert.NoDuplicates(t, func() ([]arena.Ref, error) {
				got := make([]arena.Ref, 0, 6)
				got = append(got, kept[4])
				for range 5 {
					got = append(got, s.Alloc(arena.MinClass))
				}

				return got, nil
			}, "no two regions in use share memory")
		})

		t.Run("zeroes the whole class of the region", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			r := s.Alloc(10)
			b := s.Bytes(r, 10)
			copy(b[:cap(b)], bytes.Repeat([]byte{fill}, cap(b)))
			assert.NoError(t, s.Free(r, 10), "Free takes the region back")
			assert.Empty(t, slabsWith(s, fill), "no slab contains a byte of the freed region")
		})

		t.Run("returns nil for the zero Ref", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			s.Alloc(1)

			var err error
			assert.Pure(t, s.InUse, func() { err = s.Free(0, 10) }, "Free of the zero Ref changes nothing")
			assert.NoError(t, err, "Free returns nil for the zero Ref")
		})

		refused := []struct {
			give func(s *arena.RefSlabs) (arena.Ref, int)
			name string
		}{
			{
				name: "returns ErrRef for a Ref past the last slab",
				give: func(s *arena.RefSlabs) (arena.Ref, int) { return s.Alloc(10) + 1<<32, 10 },
			},
			{
				name: "returns ErrRef for a Ref whose class runs past the end of its slab",
				give: func(s *arena.RefSlabs) (arena.Ref, int) { return s.Alloc(10) + slabSize - arena.MinClass + 8, 10 },
			},
			{
				name: "returns ErrRef for a length below 1",
				give: func(s *arena.RefSlabs) (arena.Ref, int) { return s.Alloc(10), 0 },
			},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s := arena.NewRefSlabs(slabSize)
				r, n := tt.give(s)

				var err error
				assert.Pure(t, s.InUse, func() { err = s.Free(r, n) }, "Free changes nothing with an error")
				assert.ErrorIs(t, err, arena.ErrRef, "Free refuses the region")
				assert.Equal(t, errs.Classify(err), errs.Invalid, "the error classifies as Invalid")
			})
		}
	})

	t.Run("InUse", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the sum of the classes that Alloc returned and Free has not taken back", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			r := s.Alloc(1)
			s.Alloc(100)
			assert.Equal(t, s.InUse(), 64+104, "InUse sums the classes in use")
			assert.NoError(t, s.Free(r, 1), "Free takes the region back")
			assert.Equal(t, s.InUse(), 104, "InUse drops the class that Free took back")
			s.Alloc(10)
			assert.Equal(t, s.InUse(), 64+104, "InUse counts a reused region")
		})

		t.Run("returns 0 for the zero RefSlabs", func(t *testing.T) {
			t.Parallel()
			var s arena.RefSlabs
			assert.Equal(t, s.InUse(), 0, "the zero RefSlabs has nothing in use")
		})
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every slab in the order of its allocation", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			s.Alloc(slabSize)
			s.Alloc(4000)
			s.Alloc(1)
			assert.Equal(t, slabLens(s), []int{slabSize, 4096, slabSize}, "All yields each slab once, in order")
		})

		t.Run("yields slabs whose capacity is their length", func(t *testing.T) {
			t.Parallel()
			s := arena.NewRefSlabs(slabSize)
			s.Alloc(1)
			for slab := range s.All() {
				assert.Equal(t, cap(slab), len(slab), "an append to a slab does not write into the RefSlabs")
			}
		})

		t.Run("yields nothing for the zero RefSlabs", func(t *testing.T) {
			t.Parallel()
			var s arena.RefSlabs
			assert.Empty(t, slabLens(&s), "the zero RefSlabs has no slab")
		})
	})

	t.Run("keeps the regions in use apart under any sequence of Alloc and Free", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, refSlabsContract, refSlabsSteps)
	})
}

// FuzzRefSlabs checks the machine of refSlabsSteps on the call sequences
// that a fuzzer finds.
func FuzzRefSlabs(f *testing.F) {
	prop.Fuzz(f, refSlabsContract, refSlabsSteps)
}

// TestRefSlabsAllocs checks the allocation contract of each method of a
// RefSlabs. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestRefSlabsAllocs(t *testing.T) {
	t.Run("Alloc and Free", func(t *testing.T) {
		s := arena.NewRefSlabs(slabSize)

		var err error
		expect.MaxAllocs(t, func() { err = s.Free(s.Alloc(100), 100) }, 0,
			"an Alloc of a class that Free took back does not allocate")
		assert.NoError(t, err, "the test measures a Free that succeeds")
	})

	t.Run("Alloc and Free of 1 MiB", func(t *testing.T) {
		s := arena.NewRefSlabs(arena.DefaultSlabSize)

		var err error
		expect.MaxAllocs(t, func() { err = s.Free(s.Alloc(arena.DefaultSlabSize), arena.DefaultSlabSize) }, 0,
			"an Alloc of a class that Free took back does not allocate")
		assert.NoError(t, err, "the test measures a Free that succeeds")
	})

	t.Run("Alloc from a new slab", func(t *testing.T) {
		var got arena.Ref
		expect.MaxAllocsWithSetup(t, func() *arena.RefSlabs { return arena.NewRefSlabs(slabSize) },
			func(s *arena.RefSlabs) { got = s.Alloc(1) }, 2, "a new slab costs the slab and the index of slabs")
		assert.NotEqual(t, got, arena.Ref(0), "the test measures an Alloc of one byte")
	})

	t.Run("Bytes", func(t *testing.T) {
		s := arena.NewRefSlabs(slabSize)
		r := s.Alloc(10)

		var got []byte
		expect.MaxAllocs(t, func() { got = s.Bytes(r, 10) }, 0, "Bytes does not allocate")
		assert.Length(t, got, 10, "the test measures the region")
	})

	t.Run("InUse", func(t *testing.T) {
		s := arena.NewRefSlabs(slabSize)
		s.Alloc(1)

		var got int
		expect.MaxAllocs(t, func() { got = s.InUse() }, 0, "InUse does not allocate")
		assert.Equal(t, got, arena.MinClass, "the test measures one region in use")
	})

	t.Run("All", func(t *testing.T) {
		s := arena.NewRefSlabs(slabSize)
		s.Alloc(1)

		var got int
		expect.MaxAllocs(t, func() {
			for slab := range s.All() {
				got = len(slab)
			}
		}, 0, "All does not allocate")
		assert.Equal(t, got, slabSize, "the test measures the slab")
	})
}

// BenchmarkRefSlabs reports the cost of each method of a RefSlabs, and fails
// when a method allocates more than the allocation contract of
// [arena.RefSlabs] allows.
func BenchmarkRefSlabs(b *testing.B) {
	b.Run("Alloc", func(b *testing.B) {
		b.Run("and Free", func(b *testing.B) {
			s := arena.NewRefSlabs(slabSize)
			assert.NoError(b, s.Free(s.Alloc(100), 100), "Free takes the region back")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = s.Free(s.Alloc(100), 100)
			}

			assert.NoError(b, err, "the benchmark measures a Free that succeeds")
		})

		b.Run("of 1 MiB and Free", func(b *testing.B) {
			s := arena.NewRefSlabs(arena.DefaultSlabSize)
			assert.NoError(b, s.Free(s.Alloc(arena.DefaultSlabSize), arena.DefaultSlabSize),
				"Free takes the region back")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = s.Free(s.Alloc(arena.DefaultSlabSize), arena.DefaultSlabSize)
			}

			assert.NoError(b, err, "the benchmark measures a Free that succeeds")
		})

		b.Run("from a new slab", func(b *testing.B) {
			var got arena.Ref

			c := bench.Start(b).MaxAllocs(2)
			defer c.End()

			for c.Loop() {
				got = arena.NewRefSlabs(slabSize).Alloc(1)
			}

			assert.NotEqual(b, got, arena.Ref(0), "the benchmark measures an Alloc of one byte")
		})
	})

	b.Run("Bytes", func(b *testing.B) {
		s := arena.NewRefSlabs(slabSize)
		r := s.Alloc(10)

		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = s.Bytes(r, 10)
		}

		assert.Length(b, got, 10, "the benchmark measures the region")
	})

	b.Run("InUse", func(b *testing.B) {
		s := arena.NewRefSlabs(slabSize)
		s.Alloc(1)

		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = s.InUse()
		}

		assert.Equal(b, got, arena.MinClass, "the benchmark measures one region in use")
	})

	b.Run("All", func(b *testing.B) {
		s := arena.NewRefSlabs(slabSize)
		s.Alloc(1)

		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			for slab := range s.All() {
				got = len(slab)
			}
		}

		assert.Equal(b, got, slabSize, "the benchmark measures the slab")
	})
}

// refSlabsSteps runs the steps of a machine over Alloc and Free of a
// RefSlabs. Each Alloc fills the whole class of its region with a byte of
// its own, so two regions in use that share memory change each other's
// bytes. The invariant checks every region in use and InUse after each step.
func refSlabsSteps(c *prop.Case) {
	s := arena.NewRefSlabs(slabSize)

	var (
		inUse []heldRef
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
					r := s.Alloc(n)
					b := s.Bytes(r, n)
					assert.Length(c, b, n, "Bytes returns n bytes of the region")
					i, _ := slices.BinarySearch(sizeClasses, n)
					assert.Equal(c, cap(b), sizeClasses[i], "the capacity is the size class of n")
					assert.Equal(c, b[:cap(b)], make([]byte, cap(b)), "the class of the region is zero")

					next = next%254 + 1
					b = b[:cap(b)]
					copy(b, bytes.Repeat([]byte{next}, len(b)))
					inUse = append(inUse, heldRef{r: r, n: n, v: next})
					total += len(b)
				},
			},
			{
				Name:    "Free",
				Enabled: func(struct{}) bool { return len(inUse) > 0 },
				Input:   func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer(0, len(inUse)-1), "region") },
				Run: func(c *prop.Case, _ int, in any) {
					h := inUse[in.(int)]
					assert.NoError(c, s.Free(h.r, h.n), "Free takes back a region that Alloc returned")
					total -= cap(s.Bytes(h.r, h.n))
					inUse = slices.Delete(inUse, in.(int), in.(int)+1)
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			assert.Equal(c, s.InUse(), total, "InUse sums the classes in use")
			for _, h := range inUse {
				b := s.Bytes(h.r, h.n)
				assert.Equal(c, bytes.Count(b[:cap(b)], []byte{h.v}), cap(b), "no other region writes into the region")
			}
		},
	})
}
