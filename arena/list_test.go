// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"runtime"
	"slices"
	"strconv"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/arena"
	"go.thesmos.sh/core/crypto"
)

// chunk is the number of elements in every chunk of a List after the
// first. The tests pin it, because callers size their memory by it.
const chunk = 4096

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

// chunksOf returns the slices List.Chunks yields. They share memory with
// the List, so a test reads through them what Truncate and Append did to
// the chunks.
func chunksOf(l *arena.List[int]) [][]int {
	var out [][]int
	for c := range l.Chunks() {
		out = append(out, c)
	}

	return out
}

func TestList(t *testing.T) {
	t.Parallel()

	t.Run("Append", func(t *testing.T) {
		t.Parallel()

		t.Run("stores each element at the next index, across chunks", func(t *testing.T) {
			t.Parallel()
			n := 2*chunk + 100
			l := filled(n)
			got := make([]int, l.Len())
			for i := range got {
				got[i] = l.At(i)
			}
			testkit.Equal(t, got, ascending(n), "At must return every element in the order appended")
		})

		t.Run("doubles the first chunk from 1 element to 4,096", func(t *testing.T) {
			t.Parallel()
			l := &arena.List[int]{}
			for _, step := range []struct{ appends, capacity int }{
				{1, 1}, {2, 2}, {3, 4}, {5, 8}, {9, 16}, {17, 32}, {chunk, chunk},
			} {
				for l.Len() < step.appends {
					l.Append(l.Len() + 1)
				}
				testkit.Equal(t, l.Cap(), step.capacity,
					"after "+strconv.Itoa(step.appends)+" appends the first chunk must have this size")
			}
		})

		t.Run("adds a chunk of 4,096 elements once the first is full", func(t *testing.T) {
			t.Parallel()
			l := filled(chunk + 1)
			testkit.Equal(t, l.Cap(), 2*chunk, "the second chunk must be allocated whole")
			for l.Len() < 2*chunk+1 {
				l.Append(l.Len() + 1)
			}
			testkit.Equal(t, l.Cap(), 3*chunk, "every later chunk must have 4,096 elements")
		})

		t.Run("never moves a chunk after the first", func(t *testing.T) {
			t.Parallel()
			l := filled(chunk + 10)
			second := chunksOf(l)[1]
			for range 100 {
				l.Append(0)
			}
			second[0] = -1
			testkit.Equal(t, l.At(chunk), -1, "a slice from Chunks must still share the chunk's memory")
		})
	})

	t.Run("At", func(t *testing.T) {
		t.Parallel()

		t.Run("returns elements of earlier chunks and of the current chunk", func(t *testing.T) {
			t.Parallel()
			l := filled(2*chunk + 3)
			testkit.Equal(t, []int{l.At(0), l.At(chunk - 1), l.At(chunk), l.At(2 * chunk), l.At(2*chunk + 2)},
				[]int{1, chunk, chunk + 1, 2*chunk + 1, 2*chunk + 3}, "At must find an element in any chunk")
		})

		for name, tc := range outOfRange() {
			t.Run("panics "+name, func(t *testing.T) {
				t.Parallel()
				got := testkit.Panics(t, func() { tc.l.At(tc.i) }, "an index outside [0, Len) must panic")
				_, isRuntime := got.(runtime.Error)
				testkit.True(t, isRuntime, "the panic must be the runtime's index error")
			})
		}
	})

	t.Run("Ptr", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the address of elements of earlier chunks and of the current chunk", func(t *testing.T) {
			t.Parallel()
			l := filled(2*chunk + 3)
			testkit.Equal(t, []int{*l.Ptr(0), *l.Ptr(chunk - 1), *l.Ptr(chunk), *l.Ptr(2 * chunk), *l.Ptr(2*chunk + 2)},
				[]int{1, chunk, chunk + 1, 2*chunk + 1, 2*chunk + 3}, "Ptr must address an element in any chunk")
		})

		t.Run("lets a write through the address change the element", func(t *testing.T) {
			t.Parallel()
			l := filled(chunk + 3)
			*l.Ptr(1) = -1
			*l.Ptr(chunk + 1) = -2
			testkit.Equal(t, []int{l.At(1), l.At(chunk + 1)}, []int{-1, -2},
				"At must read what was written through Ptr")
		})

		t.Run("keeps an element's address across appends once the first chunk is full", func(t *testing.T) {
			t.Parallel()
			l := filled(chunk)
			first, second := l.Ptr(0), l.Ptr(chunk-1)
			for range 2 * chunk {
				l.Append(0)
			}
			*first, *second = -1, -2
			testkit.Equal(t, []int{l.At(0), l.At(chunk - 1)}, []int{-1, -2},
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
			testkit.Equal(t, l.At(chunk), -1, "an address in a later chunk must survive later chunks")
		})

		t.Run("leaves an earlier address on the old copy when the first chunk doubles", func(t *testing.T) {
			t.Parallel()
			l := filled(1)
			p := l.Ptr(0)
			l.Append(2)
			*p = -1
			testkit.Equal(t, l.At(0), 1, "the doubling must have copied the element to a new array")
		})

		for name, tc := range outOfRange() {
			t.Run("panics "+name, func(t *testing.T) {
				t.Parallel()
				got := testkit.Panics(t, func() { _ = tc.l.Ptr(tc.i) }, "an index outside [0, Len) must panic")
				_, isRuntime := got.(runtime.Error)
				testkit.True(t, isRuntime, "the panic must be the runtime's index error")
			})
		}
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("is 0 for a zero List", func(t *testing.T) {
			t.Parallel()
			var l arena.List[int]
			testkit.Equal(t, l.Len(), 0, "a zero List must be empty")
		})
	})

	t.Run("Cap", func(t *testing.T) {
		t.Parallel()

		t.Run("is 0 for a zero List", func(t *testing.T) {
			t.Parallel()
			var l arena.List[int]
			testkit.Equal(t, l.Cap(), 0, "a zero List must have no chunk")
		})

		t.Run("counts the first chunk and 4,096 for every later chunk", func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, filled(3*chunk+1).Cap(), 4*chunk, "four chunks must store 16,384 elements")
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
			testkit.Equal(t, indexes, ascending(n), "indexes must run from 0 without a gap")
			testkit.Equal(t, values, ascending(n), "each value must be the element at its index")
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
			testkit.Equal(t, visited, 3, "All must stop at the break")
		})

		t.Run("yields nothing for a zero List", func(t *testing.T) {
			t.Parallel()
			var l arena.List[int]
			visited := 0
			for range l.All() {
				visited++
			}
			testkit.Equal(t, visited, 0, "a zero List has no element to yield")
		})
	})

	t.Run("Chunks", func(t *testing.T) {
		t.Parallel()

		t.Run("yields chunks of 4,096 elements with the rest last", func(t *testing.T) {
			t.Parallel()
			n := 2*chunk + 5
			chunks := chunksOf(filled(n))
			lengths := make([]int, len(chunks))
			for i, c := range chunks {
				lengths[i] = len(c)
			}
			testkit.Equal(t, lengths, []int{chunk, chunk, 5}, "every chunk but the last must be full")
			testkit.Equal(t, slices.Concat(chunks...), ascending(n), "the chunks must cover every element in order")
		})

		t.Run("caps each chunk's capacity at its length", func(t *testing.T) {
			t.Parallel()
			l := filled(20)
			first := chunksOf(l)[0]
			testkit.Equal(t, cap(first), len(first), "a chunk's capacity must be its length")
			first = append(first, -1)
			l.Append(21)
			testkit.Equal(t, first[20], -1, "an append to a yielded chunk must not share the List's memory")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			yielded := 0
			for range filled(2*chunk + 1).Chunks() {
				yielded++
				break
			}
			testkit.Equal(t, yielded, 1, "Chunks must stop at the break")
		})

		t.Run("yields nothing past Len when Truncate kept chunks", func(t *testing.T) {
			t.Parallel()
			l := filled(3 * chunk)
			l.Truncate(10)
			testkit.Equal(t, chunksOf(l), [][]int{ascending(10)}, "only the elements below Len must be yielded")
		})

		t.Run("yields nothing for a zero List", func(t *testing.T) {
			t.Parallel()
			var l arena.List[int]
			testkit.Equal(t, len(chunksOf(&l)), 0, "a zero List has no chunk")
		})
	})

	t.Run("Truncate", func(t *testing.T) {
		t.Parallel()

		t.Run("drops the elements at n and later", func(t *testing.T) {
			t.Parallel()
			l := filled(2*chunk + 7)
			l.Truncate(chunk + 4)
			testkit.Equal(t, l.Len(), chunk+4, "Len must be n")
			testkit.Equal(t, l.At(chunk+3), chunk+4, "the element before n must remain")
			testkit.Panics(t, func() { l.At(chunk + 4) }, "the element at n must be gone")
		})

		for _, n := range []int{0, 5, chunk, chunk + 4, 2*chunk + 7} {
			t.Run("zeroes every element it drops, to length "+strconv.Itoa(n), func(t *testing.T) {
				t.Parallel()
				l := filled(2*chunk + 7)
				chunks := chunksOf(l)
				l.Truncate(n)
				want := ascending(2*chunk + 7)
				clear(want[n:])
				testkit.Equal(t, slices.Concat(chunks...), want,
					"the elements below n must remain and every dropped element must be zero")
			})
		}

		t.Run("writes no element past the old length", func(t *testing.T) {
			t.Parallel()
			l := filled(20)
			first := chunksOf(l)[0]
			l.Truncate(10)
			first[15] = -1
			l.Truncate(5)
			testkit.Equal(t, first[15], -1, "Truncate must clear only the elements it drops")
		})

		t.Run("keeps every chunk", func(t *testing.T) {
			t.Parallel()
			l := filled(3 * chunk)
			first := chunksOf(l)[0]
			l.Truncate(0)
			testkit.Equal(t, l.Cap(), 3*chunk, "Truncate must keep the chunks")
			for l.Len() < 3*chunk {
				l.Append(-l.Len())
			}
			testkit.Equal(t, l.Cap(), 3*chunk, "refilling must reuse the kept chunks")
			testkit.Equal(t, first[1], -1, "the refilled List must write into the kept first chunk")
		})

		t.Run("releases the chunks only when the zero List is assigned", func(t *testing.T) {
			t.Parallel()
			l := filled(3 * chunk)
			l.Truncate(0)
			testkit.Equal(t, l.Cap(), 3*chunk, "Truncate must keep the chunks")
			*l = arena.List[int]{}
			testkit.Equal(t, l.Cap(), 0, "the zero List must have no chunk")
			l.Append(1)
			testkit.Equal(t, l.Cap(), 1, "the next Append must start a new first chunk")
		})

		t.Run("lets Append continue at n", func(t *testing.T) {
			t.Parallel()
			l := filled(100)
			l.Truncate(40)
			l.Append(-1)
			testkit.Equal(t, l.Len(), 41, "the Append must follow element n-1")
			testkit.Equal(t, l.At(39), 40, "the elements below n must remain")
			testkit.Equal(t, l.At(40), -1, "the Append must be at index n")
		})

		t.Run("lets the first chunk keep doubling", func(t *testing.T) {
			t.Parallel()
			l := filled(20)
			l.Truncate(16)
			for l.Len() < 40 {
				l.Append(l.Len() + 1)
			}
			testkit.Equal(t, l.Cap(), 64, "the first chunk must double past its kept size")
			testkit.Equal(t, slices.Concat(chunksOf(l)...), ascending(40), "every element must survive the doubling")
		})

		t.Run("to Len at the end of a full chunk changes nothing", func(t *testing.T) {
			t.Parallel()
			l := filled(chunk)
			l.Truncate(chunk)
			l.Append(chunk + 1)
			testkit.Equal(t, l.Len(), chunk+1, "Append must add the next chunk")
			testkit.Equal(t, l.At(chunk), chunk+1, "the element must start the second chunk")
		})

		t.Run("to the start of a kept chunk", func(t *testing.T) {
			t.Parallel()
			l := filled(2*chunk + 3)
			l.Truncate(chunk)
			l.Append(-1)
			testkit.Equal(t, l.At(chunk), -1, "Append must write into the kept second chunk")
			testkit.Equal(t, l.Cap(), 3*chunk, "no chunk may be allocated")
		})

		t.Run("panics below zero", func(t *testing.T) {
			t.Parallel()
			l := filled(3)
			got := testkit.Panics(t, func() { l.Truncate(-1) }, "a negative length must panic")
			testkit.Equal(t, got, any("arena: List.Truncate length -1 out of range [0:3]"),
				"the panic must name the length and the current length")
		})

		t.Run("panics past Len", func(t *testing.T) {
			t.Parallel()
			l := filled(3)
			got := testkit.Panics(t, func() { l.Truncate(4) }, "a length past Len must panic")
			testkit.Equal(t, got, any("arena: List.Truncate length 4 out of range [0:3]"),
				"the panic must name the length and the current length")
		})
	})
}

// record has the shape of a leaf in a log: a byte slice, a kind and a
// digest. It is 96 bytes and contains a pointer.
type record struct {
	data []byte
	kind uint8
	hash crypto.Digest
}

// benchLen is the number of elements each List benchmark writes or reads.
const benchLen = 1 << 16

// sinkInt keeps the results of the read benchmarks alive.
var sinkInt int

// BenchmarkList reports the cost of every List operation over 65,536
// records of 96 bytes. Appending into kept chunks allocates nothing.
// Appending from empty allocates 33 times: 13 sizes of the first chunk,
// 15 later chunks and 5 growths of the chunk index.
func BenchmarkList(b *testing.B) {
	v := record{data: make([]byte, 8), hash: crypto.NewDigest256([crypto.DigestSize256]byte{1})}

	b.Run("Append from empty", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var l arena.List[record]
			for range benchLen {
				l.Append(v)
			}
		}
	})

	b.Run("Append into kept chunks", func(b *testing.B) {
		var l arena.List[record]
		for range benchLen {
			l.Append(v)
		}
		b.ReportAllocs()
		for b.Loop() {
			l.Truncate(0)
			for range benchLen {
				l.Append(v)
			}
		}
	})

	var full arena.List[record]
	for range benchLen {
		full.Append(v)
	}

	b.Run("At", func(b *testing.B) {
		b.ReportAllocs()
		sum := 0
		for b.Loop() {
			for i := range benchLen {
				sum += int(full.At(i).kind)
			}
		}
		sinkInt = sum
	})

	b.Run("Ptr", func(b *testing.B) {
		b.ReportAllocs()
		sum := 0
		for b.Loop() {
			for i := range benchLen {
				sum += int(full.Ptr(i).kind)
			}
		}
		sinkInt = sum
	})

	b.Run("All", func(b *testing.B) {
		b.ReportAllocs()
		sum := 0
		for b.Loop() {
			for _, r := range full.All() {
				sum += int(r.kind)
			}
		}
		sinkInt = sum
	})

	b.Run("Chunks", func(b *testing.B) {
		b.ReportAllocs()
		sum := 0
		for b.Loop() {
			for c := range full.Chunks() {
				for i := range c {
					sum += int(c[i].kind)
				}
			}
		}
		sinkInt = sum
	})
}
