// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"math/bits"
	"runtime"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/arena"
	"go.thesmos.sh/core/crypto"
)

// chunk is the number of elements in every chunk of a List after the
// first. The tests pin it, because callers size their memory by it.
const chunk = 4096

// benchLen is the number of elements each List benchmark writes or reads.
const benchLen = 1 << 16

// listContract is the contract of listSteps, which TestList and FuzzList
// check.
const listContract = "every call must agree with a slice of the same elements"

// record has the shape of a leaf in a log: a byte slice, a kind and a
// digest. It is 96 bytes and contains a pointer.
type record struct {
	data []byte
	kind uint8
	hash crypto.Digest
}

func TestList(t *testing.T) {
	t.Parallel()

	t.Run("Append", func(t *testing.T) {
		t.Parallel()

		t.Run("stores each element at the next index", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(n int) []int {
				l := filled(n)
				got := make([]int, l.Len())
				for i := range got {
					got[i] = l.At(i)
				}

				return got
			}, ascending, "At must return every element in the order appended",
				prop.Using(prop.Integer(0, 3*chunk+1)), prop.Example(0), prop.Example(2*chunk+100))
		})

		t.Run("sizes the first chunk to the next power of two up to 4,096", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(n int) int { return filled(n).Cap() },
				func(n int) int { return 1 << bits.Len(uint(n-1)) },
				"the first chunk must double from 1 element until it has room for n",
				prop.Using(prop.Integer(1, chunk)), prop.Example(1), prop.Example(2), prop.Example(3),
				prop.Example(5), prop.Example(9), prop.Example(17), prop.Example(chunk))
		})

		t.Run("adds a chunk of 4,096 elements once the first is full", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(n int) int { return filled(n).Cap() },
				func(n int) int { return (n + chunk - 1) / chunk * chunk },
				"every chunk after the first must have room for 4,096 elements",
				prop.Using(prop.Integer(chunk+1, 4*chunk)), prop.Example(chunk+1), prop.Example(2*chunk+1),
				prop.Example(3*chunk+1))
		})

		t.Run("never moves a chunk after the first", func(t *testing.T) {
			t.Parallel()
			l := filled(chunk + 10)
			second := slices.Collect(l.Chunks())[1]
			for range 100 {
				l.Append(0)
			}
			second[0] = -1
			assert.Equal(t, l.At(chunk), -1, "a slice from Chunks must still share the chunk's memory")
		})
	})

	t.Run("At", func(t *testing.T) {
		t.Parallel()

		t.Run("returns elements of earlier chunks and of the current chunk", func(t *testing.T) {
			t.Parallel()
			l := filled(2*chunk + 3)
			assert.Equal(t, []int{l.At(0), l.At(chunk - 1), l.At(chunk), l.At(2 * chunk), l.At(2*chunk + 2)},
				[]int{1, chunk, chunk + 1, 2*chunk + 1, 2*chunk + 3}, "At must find an element in any chunk")
		})

		for name, tc := range outOfRange() {
			t.Run("panics "+name, func(t *testing.T) {
				t.Parallel()
				got, _ := assert.Panics(t, func() { tc.l.At(tc.i) }, "an index outside [0, Len) must panic").(error)
				_ = assert.ErrorAs[runtime.Error](t, got, "the panic must be the runtime's index error")
			})
		}
	})

	t.Run("Ptr", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the address of elements of earlier chunks and of the current chunk", func(t *testing.T) {
			t.Parallel()
			l := filled(2*chunk + 3)
			assert.Equal(t, []int{*l.Ptr(0), *l.Ptr(chunk - 1), *l.Ptr(chunk), *l.Ptr(2 * chunk), *l.Ptr(2*chunk + 2)},
				[]int{1, chunk, chunk + 1, 2*chunk + 1, 2*chunk + 3}, "Ptr must address an element in any chunk")
		})

		t.Run("lets a write through the address change the element", func(t *testing.T) {
			t.Parallel()
			l := filled(chunk + 3)
			*l.Ptr(1) = -1
			*l.Ptr(chunk + 1) = -2
			assert.Equal(t, []int{l.At(1), l.At(chunk + 1)}, []int{-1, -2}, "At must read what was written through Ptr")
		})

		t.Run("keeps an element's address across appends once the first chunk is full", func(t *testing.T) {
			t.Parallel()
			l := filled(chunk)
			first, second := l.Ptr(0), l.Ptr(chunk-1)
			for range 2 * chunk {
				l.Append(0)
			}
			*first, *second = -1, -2
			assert.Equal(t, []int{l.At(0), l.At(chunk - 1)}, []int{-1, -2},
				"an address in a full first chunk must survive later chunks")
		})

		t.Run("keeps the address of an element past the first chunk", func(t *testing.T) {
			t.Parallel()
			l := filled(chunk + 1)
			p := l.Ptr(chunk)
			for range 2 * chunk {
				l.Append(0)
			}
			*p = -1
			assert.Equal(t, l.At(chunk), -1, "an address in a later chunk must survive later chunks")
		})

		t.Run("leaves an earlier address on the old copy when the first chunk doubles", func(t *testing.T) {
			t.Parallel()
			l := filled(1)
			p := l.Ptr(0)
			l.Append(2)
			*p = -1
			assert.Equal(t, l.At(0), 1, "the doubling must have copied the element to a new array")
		})

		for name, tc := range outOfRange() {
			t.Run("panics "+name, func(t *testing.T) {
				t.Parallel()
				got, _ := assert.Panics(t, func() { _ = tc.l.Ptr(tc.i) }, "an index outside [0, Len) must panic").(error)
				_ = assert.ErrorAs[runtime.Error](t, got, "the panic must be the runtime's index error")
			})
		}
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0 for a zero List", func(t *testing.T) {
			t.Parallel()
			var l arena.List[int]
			assert.Equal(t, l.Len(), 0, "a zero List must be empty")
		})
	})

	t.Run("Cap", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0 for a zero List", func(t *testing.T) {
			t.Parallel()
			var l arena.List[int]
			assert.Equal(t, l.Cap(), 0, "a zero List must have no chunk")
		})

		t.Run("never falls under any sequence of Append and Truncate", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Truncate must keep every chunk that Append allocated", func(c *prop.Case) {
				var l arena.List[int]
				assert.Monotonic(c, l.Cap, func() error {
					if c.Draw(prop.Boolean(), "truncate") {
						l.Truncate(c.Draw(prop.Integer(0, l.Len()), "n"))

						return nil
					}
					for range c.Draw(prop.Integer(0, 2*chunk), "appends") {
						l.Append(l.Len())
					}

					return nil
				}, 8, "Cap must never fall")
			})
		})
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every index and value in order", func(t *testing.T) {
			t.Parallel()
			n := chunk + 50
			var indexes, values []int
			for i, v := range filled(n).All() {
				indexes = append(indexes, i+1)
				values = append(values, v)
			}
			assert.Equal(t, indexes, ascending(n), "indexes must run from 0 without a gap")
			assert.Equal(t, values, ascending(n), "each value must be the element at its index")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			visited := 0
			for i := range filled(chunk + 1).All() {
				visited++
				if i == 2 {
					break
				}
			}
			assert.Equal(t, visited, 3, "All must stop at the break")
		})

		t.Run("yields nothing for a zero List", func(t *testing.T) {
			t.Parallel()
			var l arena.List[int]
			for range l.All() {
				t.Fatal("a zero List has no element to yield")
			}
		})
	})

	t.Run("Chunks", func(t *testing.T) {
		t.Parallel()

		t.Run("yields chunks of 4,096 elements with the rest last", func(t *testing.T) {
			t.Parallel()
			n := 2*chunk + 5
			chunks := slices.Collect(filled(n).Chunks())
			lengths := make([]int, len(chunks))
			for i, c := range chunks {
				lengths[i] = len(c)
			}
			assert.Equal(t, lengths, []int{chunk, chunk, 5}, "every chunk but the last must be full")
			assert.Equal(t, slices.Concat(chunks...), ascending(n), "the chunks must cover every element in order")
		})

		t.Run("caps each chunk's capacity at its length", func(t *testing.T) {
			t.Parallel()
			l := filled(20)
			first := slices.Collect(l.Chunks())[0]
			assert.Equal(t, cap(first), len(first), "a chunk's capacity must be its length")
			first = append(first, -1)
			l.Append(21)
			assert.Equal(t, first[20], -1, "an append to a yielded chunk must not share the List's memory")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			yielded := 0
			for range filled(2*chunk + 1).Chunks() {
				yielded++

				break
			}
			assert.Equal(t, yielded, 1, "Chunks must stop at the break")
		})

		t.Run("yields nothing past Len when Truncate kept chunks", func(t *testing.T) {
			t.Parallel()
			l := filled(3 * chunk)
			l.Truncate(10)
			assert.Equal(t, slices.Collect(l.Chunks()), [][]int{ascending(10)},
				"only the elements below Len may be yielded")
		})

		t.Run("yields nothing for a zero List", func(t *testing.T) {
			t.Parallel()
			var l arena.List[int]
			assert.Nil(t, slices.Collect(l.Chunks()), "a zero List has no chunk")
		})
	})

	t.Run("Truncate", func(t *testing.T) {
		t.Parallel()

		t.Run("drops the elements at n and later", func(t *testing.T) {
			t.Parallel()
			l := filled(2*chunk + 7)
			l.Truncate(chunk + 4)
			assert.Equal(t, l.Len(), chunk+4, "Len must be n")
			assert.Equal(t, l.At(chunk+3), chunk+4, "the element before n must remain")
			assert.Panics(t, func() { l.At(chunk + 4) }, "the element at n must be gone")
		})

		t.Run("zeroes every element it drops", func(t *testing.T) {
			t.Parallel()
			const total = 2*chunk + 7
			prop.Equal(t, func(n int) []int {
				l := filled(total)
				chunks := slices.Collect(l.Chunks())
				l.Truncate(n)

				return slices.Concat(chunks...)
			}, func(n int) []int {
				want := ascending(total)
				clear(want[n:])

				return want
			}, "Truncate must keep the elements below n and zero every element it drops",
				prop.Using(prop.Integer(0, total)), prop.Example(0), prop.Example(5), prop.Example(chunk),
				prop.Example(chunk+4), prop.Example(total))
		})

		t.Run("writes no element past the old length", func(t *testing.T) {
			t.Parallel()
			l := filled(20)
			first := slices.Collect(l.Chunks())[0]
			l.Truncate(10)
			first[15] = -1
			l.Truncate(5)
			assert.Equal(t, first[15], -1, "Truncate must clear only the elements it drops")
		})

		t.Run("keeps every chunk", func(t *testing.T) {
			t.Parallel()
			l := filled(3 * chunk)
			first := slices.Collect(l.Chunks())[0]
			assert.Pure(t, l.Cap, func() { l.Truncate(0) }, "Truncate must keep the chunks")
			for l.Len() < 3*chunk {
				l.Append(-l.Len())
			}
			assert.Equal(t, l.Cap(), 3*chunk, "refilling must reuse the kept chunks")
			assert.Equal(t, first[1], -1, "the refilled List must write into the kept first chunk")
		})

		t.Run("releases the chunks only when the zero List is assigned", func(t *testing.T) {
			t.Parallel()
			l := filled(3 * chunk)
			l.Truncate(0)
			assert.Equal(t, l.Cap(), 3*chunk, "Truncate must keep the chunks")
			*l = arena.List[int]{}
			assert.Equal(t, l.Cap(), 0, "the zero List must have no chunk")
			l.Append(1)
			assert.Equal(t, l.Cap(), 1, "the next Append must start a new first chunk")
		})

		t.Run("lets Append continue at n", func(t *testing.T) {
			t.Parallel()
			l := filled(100)
			l.Truncate(40)
			l.Append(-1)
			assert.Equal(t, l.Len(), 41, "the Append must follow element n-1")
			assert.Equal(t, l.At(39), 40, "the elements below n must remain")
			assert.Equal(t, l.At(40), -1, "the Append must be at index n")
		})

		t.Run("lets the first chunk keep doubling", func(t *testing.T) {
			t.Parallel()
			l := filled(20)
			l.Truncate(16)
			for l.Len() < 40 {
				l.Append(l.Len() + 1)
			}
			assert.Equal(t, l.Cap(), 64, "the first chunk must double past its kept size")
			assert.Equal(t, slices.Concat(slices.Collect(l.Chunks())...), ascending(40),
				"every element must survive the doubling")
		})

		t.Run("lets Append start the next chunk after a cut at the end of a full chunk", func(t *testing.T) {
			t.Parallel()
			l := filled(chunk)
			l.Truncate(chunk)
			l.Append(chunk + 1)
			assert.Equal(t, l.Len(), chunk+1, "Append must add the next chunk")
			assert.Equal(t, l.At(chunk), chunk+1, "the element must start the second chunk")
		})

		t.Run("lets Append write into a kept chunk after a cut at its start", func(t *testing.T) {
			t.Parallel()
			l := filled(2*chunk + 3)
			l.Truncate(chunk)
			l.Append(-1)
			assert.Equal(t, l.At(chunk), -1, "Append must write into the kept second chunk")
			assert.Equal(t, l.Cap(), 3*chunk, "no chunk may be allocated")
		})

		t.Run("panics below zero", func(t *testing.T) {
			t.Parallel()
			l := filled(3)
			got := assert.Panics(t, func() { l.Truncate(-1) }, "a negative length must panic")
			assert.Equal(t, got, any("arena: List.Truncate length -1 out of range [0:3]"),
				"the panic must name the length and the current length")
		})

		t.Run("panics past Len", func(t *testing.T) {
			t.Parallel()
			l := filled(3)
			got := assert.Panics(t, func() { l.Truncate(4) }, "a length past Len must panic")
			assert.Equal(t, got, any("arena: List.Truncate length 4 out of range [0:3]"),
				"the panic must name the length and the current length")
		})
	})

	t.Run("behaves as a slice under any sequence of calls", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, listContract, listSteps)
	})
}

// FuzzList checks the machine of listSteps on the call sequences that a
// fuzzer finds.
func FuzzList(f *testing.F) {
	prop.Fuzz(f, listContract, listSteps)
}

// TestListAllocs checks the allocation contract of each method of a List
// whose chunks have room for its elements. MaxAllocs counts the allocations
// of the whole process, so the test does not run in parallel.
func TestListAllocs(t *testing.T) {
	full := filled(2 * chunk)

	t.Run("Append", func(t *testing.T) {
		l := filled(2 * chunk)
		expect.MaxAllocs(t, func() {
			l.Truncate(0)
			for i := range 2 * chunk {
				l.Append(i)
			}
		}, 0, "Append into kept chunks must not allocate")
		assert.Equal(t, l.Len(), 2*chunk, "the test must measure a refill of every chunk")
	})

	t.Run("At", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = full.At(chunk) }, 0, "At must not allocate")
		assert.Equal(t, got, chunk+1, "the test must measure an element of the second chunk")
	})

	t.Run("Ptr", func(t *testing.T) {
		var got *int
		expect.MaxAllocs(t, func() { got = full.Ptr(chunk) }, 0, "Ptr must not allocate")
		assert.Equal(t, *got, chunk+1, "the test must measure an element of the second chunk")
	})

	t.Run("Len", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = full.Len() }, 0, "Len must not allocate")
		assert.Equal(t, got, 2*chunk, "the test must measure the length")
	})

	t.Run("Cap", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = full.Cap() }, 0, "Cap must not allocate")
		assert.Equal(t, got, 2*chunk, "the test must measure the capacity")
	})

	t.Run("All", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() {
			for _, v := range full.All() {
				got = v
			}
		}, 0, "All must not allocate")
		assert.Equal(t, got, 2*chunk, "the test must measure every element")
	})

	t.Run("Chunks", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() {
			got = 0
			for c := range full.Chunks() {
				got += len(c)
			}
		}, 0, "Chunks must not allocate")
		assert.Equal(t, got, 2*chunk, "the test must measure every chunk")
	})

	t.Run("Truncate", func(t *testing.T) {
		l := filled(chunk)
		expect.MaxAllocs(t, func() { l.Truncate(l.Len() / 2) }, 0, "Truncate must not allocate")
		assert.Equal(t, l.Len(), 0, "the test must measure Truncates down to an empty List")
	})
}

// BenchmarkList reports the cost of every List operation over 65,536
// records of 96 bytes. Appending into kept chunks allocates nothing.
// Appending from empty allocates 33 times: 13 sizes of the first chunk,
// 15 later chunks and 5 growths of the chunk index.
func BenchmarkList(b *testing.B) {
	v := record{data: make([]byte, 8), kind: 1, hash: crypto.NewDigest256([crypto.DigestSize256]byte{1})}

	var full arena.List[record]
	for range benchLen {
		full.Append(v)
	}

	b.Run("Append", func(b *testing.B) {
		b.Run("from empty", func(b *testing.B) {
			var l arena.List[record]

			c := bench.Start(b).MaxAllocs(33)
			defer c.End()

			for c.Loop() {
				l = arena.List[record]{}
				for range benchLen {
					l.Append(v)
				}
			}

			assert.Equal(b, l.Len(), benchLen, "the benchmark must measure a fill of every element")
		})

		b.Run("into kept chunks", func(b *testing.B) {
			var l arena.List[record]
			for range benchLen {
				l.Append(v)
			}

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				l.Truncate(0)
				for range benchLen {
					l.Append(v)
				}
			}

			assert.Equal(b, l.Len(), benchLen, "the benchmark must measure a refill of every element")
		})
	})

	b.Run("At", func(b *testing.B) {
		sum := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			for i := range benchLen {
				sum += int(full.At(i).kind)
			}
		}

		assert.InRange(b, sum, 1, 1<<63, "the benchmark must measure reads of every element")
	})

	b.Run("Ptr", func(b *testing.B) {
		sum := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			for i := range benchLen {
				sum += int(full.Ptr(i).kind)
			}
		}

		assert.InRange(b, sum, 1, 1<<63, "the benchmark must measure reads of every element")
	})

	b.Run("All", func(b *testing.B) {
		sum := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			for _, r := range full.All() {
				sum += int(r.kind)
			}
		}

		assert.InRange(b, sum, 1, 1<<63, "the benchmark must measure reads of every element")
	})

	b.Run("Chunks", func(b *testing.B) {
		sum := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			for part := range full.Chunks() {
				for i := range part {
					sum += int(part[i].kind)
				}
			}
		}

		assert.InRange(b, sum, 1, 1<<63, "the benchmark must measure reads of every element")
	})

	b.Run("Truncate", func(b *testing.B) {
		var l arena.List[record]

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			l.Append(v)
			l.Truncate(0)
		}

		assert.Equal(b, l.Len(), 0, "the benchmark must measure a Truncate to empty")
	})
}

// filled returns a List of n elements whose element i is i+1, so a zero
// element is a cleared one.
func filled(n int) *arena.List[int] {
	l := &arena.List[int]{}
	for i := range n {
		l.Append(i + 1)
	}

	return l
}

// truncated returns filled(n) truncated to length to.
func truncated(n, to int) *arena.List[int] {
	l := filled(n)
	l.Truncate(to)

	return l
}

// outOfRange returns Lists and an index outside [0, Len) of each, for
// the index checks of At and Ptr.
func outOfRange() map[string]struct {
	l *arena.List[int]
	i int
} {
	return map[string]struct {
		l *arena.List[int]
		i int
	}{
		"below zero":                            {filled(3), -1},
		"at Len":                                {filled(3), 3},
		"at Len at the end of a full chunk":     {filled(chunk), chunk},
		"on a zero List":                        {&arena.List[int]{}, 0},
		"at Len at the start of a kept chunk":   {truncated(2*chunk+3, chunk), chunk},
		"past Len in a kept chunk":              {truncated(2*chunk+3, chunk+1), chunk + 2},
		"at Len in the first chunk after a cut": {truncated(20, 10), 10},
	}
}

// ascending returns 1, 2, …, n, the elements of filled(n).
func ascending(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i + 1
	}

	return out
}

// listSteps runs the steps of a machine over the methods of a List, and
// checks every call against a slice of the same elements. An Append step
// appends up to 4,096 elements, so a case crosses the boundaries of
// chunks, and a Truncate step checks the elements that it zeroes in the
// chunks.
func listSteps(c *prop.Case) {
	var (
		l       arena.List[int]
		want    []int
		written int
	)

	index := func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer(0, len(want)-1), "i") }
	nonEmpty := func(struct{}) bool { return len(want) > 0 }

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:   "Append",
				Weight: 2,
				Input:  func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer(0, chunk), "count") },
				Run: func(_ *prop.Case, _ int, in any) {
					for range in.(int) {
						written++
						l.Append(written)
						want = append(want, written)
					}
				},
			},
			{
				Name:  "Truncate",
				Input: func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer(0, len(want)), "n") },
				Run: func(c *prop.Case, _ int, in any) {
					n := in.(int)
					chunks := slices.Collect(l.Chunks())
					kept := slices.Concat(want[:n], make([]int, len(want)-n))
					l.Truncate(n)
					want = want[:n]
					assert.Equal(c, slices.Concat(chunks...), kept,
						"Truncate must keep the elements below n and zero every element it drops", assert.EquateEmpty())
				},
			},
			{
				Name:    "At",
				Enabled: nonEmpty,
				Input:   index,
				Run: func(c *prop.Case, _ int, in any) {
					assert.Equal(c, l.At(in.(int)), want[in.(int)], "At must return the element at the index")
				},
			},
			{
				Name:    "Ptr",
				Enabled: nonEmpty,
				Input:   index,
				Run: func(c *prop.Case, _ int, in any) {
					assert.Equal(c, *l.Ptr(in.(int)), want[in.(int)], "Ptr must address the element at the index")
				},
			},
			{
				Name: "Chunks",
				Run: func(c *prop.Case, _ int, _ any) {
					assert.Equal(c, slices.Concat(slices.Collect(l.Chunks())...), want,
						"Chunks must yield the elements in order", assert.EquateEmpty())
				},
			},
			{
				Name: "assign the zero List",
				Run: func(*prop.Case, int, any) {
					l, want = arena.List[int]{}, want[:0]
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			assert.Equal(c, l.Len(), len(want), "Len must count the elements of the model")
		},
	})
}
