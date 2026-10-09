// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"bytes"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/arena"
)

// fill is the byte that a case writes into the memory of an arena or a
// Slabs, to tell a written byte from a zero one.
const fill = 0xa5

// arenaContract is the contract of arenaSteps, which TestArena and
// FuzzArena check.
const arenaContract = "every call must agree with a model of the bytes of the current lifecycle"

// Generators of the arena properties.
var (
	// capacities generates the initial capacity of an arena: none, or room
	// for a few payloads.
	capacities = prop.Integer(0, 128)

	// payload generates the bytes of one append, the empty payload
	// included.
	payload = prop.Bytes(prop.MaxSize(48))

	// payloads generates the payloads of a sequence of appends.
	payloads = prop.List(payload, prop.MaxSize(8))
)

// sizes are the payload sizes of the benchmarks, from a small record to a
// large batch.
var sizes = []struct {
	name string
	n    int
}{
	{"16B", 16},
	{"64B", 64},
	{"256B", 256},
	{"4K", 4096},
	{"64K", 65536},
}

// ends are the calls that end the lifecycle of an arena, and with it the
// validity of every Marker that the lifecycle returned.
var ends = []struct {
	name string
	end  func(*arena.Arena)
}{
	{name: "Reset", end: (*arena.Arena).Reset},
	{name: "Shrink", end: (*arena.Arena).Shrink},
}

// marked is a Marker beside the position and the lifecycle that the model
// of arenaSteps records for it.
type marked struct {
	m     arena.Marker
	pos   int
	cycle int
}

func TestArena(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an arena without a backing buffer", func(t *testing.T) {
			t.Parallel()
			a := arena.New()
			expect.Equal(t, a.Len(), 0, "a new arena must be empty")
			expect.Equal(t, a.Cap(), 0, "a new arena must have no backing buffer")
		})
	})

	t.Run("NewWithCapacity", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an empty arena with room for initialCap bytes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "NewWithCapacity must return an empty arena of the requested capacity", func(c *prop.Case) {
				n := c.Draw(capacities, "initialCap")
				a := arena.NewWithCapacity(n)
				assert.Equal(c, a.Len(), 0, "the arena must be empty")
				assert.Equal(c, a.Cap(), n, "the capacity must be initialCap")
			})
		})
	})

	t.Run("Append", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a region that keeps its payload across later appends", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "every region, Bytes and Len must follow the payloads in the order of the appends",
				appendsInOrder)
		})
	})

	t.Run("Alloc", func(t *testing.T) {
		t.Parallel()

		// have is the capacity of the arena of the growth cases, and
		// allocCap returns the capacity after an Alloc of n bytes on it.
		const have = 50
		allocCap := func(n int) int {
			a := arena.NewWithCapacity(have)
			a.Alloc(n)

			return a.Cap()
		}

		t.Run("returns n zero bytes after the bytes appended before", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Alloc must append n zero bytes and keep the bytes before them", func(c *prop.Case) {
				a := arena.NewWithCapacity(c.Draw(capacities, "initialCap"))
				head := c.Draw(payload, "head")
				a.Append(head)
				n := c.Draw(prop.Integer(0, 96), "n")

				got := a.Alloc(n)
				assert.Equal(c, got, make([]byte, n), "the region must be n zero bytes", assert.EquateEmpty())
				assert.Equal(c, cap(got), n, "the capacity of the region must be its length")
				assert.Equal(c, a.Bytes(), slices.Concat(head, got), "the region must follow the appended bytes",
					assert.EquateEmpty())
			})
		})

		t.Run("keeps the backing buffer for a request that fits it", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, allocCap, func(int) int { return have },
				"Alloc must not reallocate for a request within the capacity",
				prop.Using(prop.Integer(0, have)), prop.Example(have))
		})

		t.Run("grows the capacity to the larger of twice the capacity and the request", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, allocCap, func(n int) int { return max(2*have, n) },
				"Alloc must double the capacity, or take the request when it is larger",
				prop.Using(prop.Integer(have+1, 1024)),
				prop.Example(have+1), prop.Example(2*have), prop.Example(2*have+1), prop.Example(1000))
		})

		t.Run("returns zero bytes where the lifecycle before Reset wrote", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			copy(a.Alloc(32), bytes.Repeat([]byte{fill}, 32))
			a.Reset()
			assert.Equal(t, a.Alloc(32), make([]byte, 32), "Alloc after Reset must return zero bytes")
		})

		t.Run("returns zero bytes where a TruncateTo rewind left written ones", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			m := a.Mark()
			a.Append(bytes.Repeat([]byte{fill}, 16))
			assert.True(t, a.TruncateTo(m), "TruncateTo must return true for a current marker")
			assert.Equal(t, a.Alloc(16), make([]byte, 16), "Alloc must clear the rewound bytes")
		})

		t.Run("returns zero bytes past the bytes that a rewind left written", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			a.Append([]byte("keep"))
			m := a.Mark()
			a.Append(bytes.Repeat([]byte{fill}, 8))
			assert.True(t, a.TruncateTo(m), "TruncateTo must return true for a current marker")
			assert.Equal(t, a.Alloc(32), make([]byte, 32), "every byte of the region must be zero")
			assert.Equal(t, a.Bytes()[:4], []byte("keep"), "the bytes before the region must remain")
		})

		t.Run("returns zero bytes where a failed AppendVia wrote", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			_, err := a.AppendVia(func(dst []byte) ([]byte, error) {
				_ = append(dst, bytes.Repeat([]byte{fill}, 32)...)

				return nil, errEncode
			})
			assert.ErrorIs(t, err, errEncode, "AppendVia must return the appender's error")
			assert.Equal(t, a.Alloc(48), make([]byte, 48), "Alloc must clear what the failed appender wrote")
		})

		t.Run("returns a region that the caller fills in place", func(t *testing.T) {
			t.Parallel()
			a := arena.New()
			copy(a.Alloc(5), "hello")
			assert.Equal(t, a.Bytes(), []byte("hello"), "a write into the region must show in Bytes")
		})

		t.Run("panics for a negative n", func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, func() { arena.New().Alloc(-1) }, "a negative n must panic")
		})
	})

	t.Run("SliceSince", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the bytes appended after the marker", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "SliceSince must return the bytes appended after Mark", func(c *prop.Case) {
				a := arena.NewWithCapacity(c.Draw(capacities, "initialCap"))
				for _, d := range c.Draw(payloads, "before") {
					a.Append(d)
				}
				m := a.Mark()
				after := c.Draw(payloads, "after")
				for _, d := range after {
					a.Append(d)
				}

				got := a.SliceSince(m)
				assert.Equal(c, got, slices.Concat(after...), "SliceSince must return the bytes after the marker")
				assert.Equal(c, cap(got), len(got), "the capacity of the slice must be its length")
			})
		})

		t.Run("returns nil for a marker at the end", func(t *testing.T) {
			t.Parallel()
			a := arena.New()
			a.Append([]byte("data"))
			assert.Nil(t, a.SliceSince(a.Mark()), "no byte may follow a marker at the end")
		})

		for _, tt := range ends {
			t.Run("returns nil for a marker from before "+tt.name, func(t *testing.T) {
				t.Parallel()
				a := arena.New()
				a.Append([]byte("first lifecycle"))
				stale := a.Mark()
				tt.end(a)
				a.Append(make([]byte, 200))
				assert.Nil(t, a.SliceSince(stale), "a marker of an ended lifecycle must slice nothing")
			})
		}
	})

	t.Run("Bytes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns no byte for a new arena", func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, arena.New().Bytes(), "a new arena must have no bytes")
		})
	})

	t.Run("behaves as a byte string under any sequence of calls", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, arenaContract, arenaSteps)
	})
}

// FuzzArena checks the machine of arenaSteps on the call sequences that a
// fuzzer finds.
func FuzzArena(f *testing.F) {
	prop.Fuzz(f, arenaContract, arenaSteps)
}

// TestArenaAllocs checks the allocation contract of each method of an
// Arena whose backing buffer has room for its writes. MaxAllocs counts the
// allocations of the whole process, so the test does not run in parallel.
func TestArenaAllocs(t *testing.T) {
	data := []byte("hello world")

	t.Run("Append", func(t *testing.T) {
		a := arena.NewWithCapacity(1024)

		var got []byte
		expect.MaxAllocs(t, func() {
			a.Reset()
			got = a.Append(data)
		}, 0, "Append into a backing buffer with room must not allocate")
		assert.Equal(t, got, data, "the test must measure an append of the payload")
	})

	t.Run("Alloc", func(t *testing.T) {
		a := arena.NewWithCapacity(1024)

		var got []byte
		expect.MaxAllocs(t, func() {
			a.Reset()
			got = a.Alloc(64)
		}, 0, "Alloc within the capacity must not allocate")
		assert.Length(t, got, 64, "the test must measure a region of 64 bytes")
	})

	t.Run("Mark", func(t *testing.T) {
		a := arena.NewWithCapacity(1024)
		a.Append(data)

		var got arena.Marker
		expect.MaxAllocs(t, func() { got = a.Mark() }, 0, "Mark must not allocate")
		assert.Nil(t, a.SliceSince(got), "the test must measure a marker at the end")
	})

	t.Run("SliceSince", func(t *testing.T) {
		a := arena.NewWithCapacity(1024)
		a.Append(data)
		m := a.Mark()
		a.Append(data)

		var got []byte
		expect.MaxAllocs(t, func() { got = a.SliceSince(m) }, 0, "SliceSince must not allocate")
		assert.Equal(t, got, data, "the test must measure the bytes after the marker")
	})

	t.Run("Len", func(t *testing.T) {
		a := arena.NewWithCapacity(1024)
		a.Append(data)

		var got int
		expect.MaxAllocs(t, func() { got = a.Len() }, 0, "Len must not allocate")
		assert.Equal(t, got, len(data), "the test must measure the length of the payload")
	})

	t.Run("Cap", func(t *testing.T) {
		a := arena.NewWithCapacity(1024)

		var got int
		expect.MaxAllocs(t, func() { got = a.Cap() }, 0, "Cap must not allocate")
		assert.Equal(t, got, 1024, "the test must measure the initial capacity")
	})

	t.Run("Bytes", func(t *testing.T) {
		a := arena.NewWithCapacity(1024)
		a.Append(data)

		var got []byte
		expect.MaxAllocs(t, func() { got = a.Bytes() }, 0, "Bytes must not allocate")
		assert.Equal(t, got, data, "the test must measure the payload")
	})
}

// BenchmarkArena reports the cost of each method of an Arena whose backing
// buffer has room for its writes, and fails when a method allocates.
func BenchmarkArena(b *testing.B) {
	b.Run("Append", func(b *testing.B) {
		for _, size := range sizes {
			b.Run(size.name, func(b *testing.B) {
				a := arena.NewWithCapacity(size.n)
				data := make([]byte, size.n)
				b.SetBytes(int64(size.n))

				var got []byte

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					a.Reset()
					got = a.Append(data)
				}

				assert.Length(b, got, size.n, "the benchmark must measure an append of the payload")
			})
		}
	})

	// Each iteration fills the region, the use that Alloc exists for. On an
	// arena that Reset cleared, the fill is the only write to the region.
	b.Run("Alloc", func(b *testing.B) {
		for _, size := range sizes {
			b.Run(size.name, func(b *testing.B) {
				a := arena.NewWithCapacity(size.n)
				src := bytes.Repeat([]byte{fill}, size.n)
				b.SetBytes(int64(size.n))

				var got []byte

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					a.Reset()
					got = a.Alloc(size.n)
					copy(got, src)
				}

				assert.Equal(b, got, src, "the benchmark must measure a filled region")
			})
		}
	})

	b.Run("Mark", func(b *testing.B) {
		a := arena.NewWithCapacity(4096)
		a.Append(make([]byte, 256))

		var got arena.Marker

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = a.Mark()
		}

		assert.Nil(b, a.SliceSince(got), "the benchmark must measure a marker at the end")
	})

	b.Run("SliceSince", func(b *testing.B) {
		a := arena.NewWithCapacity(4096)
		m := a.Mark()
		a.Append(make([]byte, 128))

		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = a.SliceSince(m)
		}

		assert.Length(b, got, 128, "the benchmark must measure the bytes after the marker")
	})

	b.Run("Len", func(b *testing.B) {
		a := arena.NewWithCapacity(4096)
		a.Append(make([]byte, 1024))

		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = a.Len()
		}

		assert.Equal(b, got, 1024, "the benchmark must measure the appended length")
	})

	b.Run("Cap", func(b *testing.B) {
		a := arena.NewWithCapacity(4096)

		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = a.Cap()
		}

		assert.Equal(b, got, 4096, "the benchmark must measure the initial capacity")
	})

	b.Run("Bytes", func(b *testing.B) {
		a := arena.NewWithCapacity(4096)
		a.Append(make([]byte, 1024))

		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = a.Bytes()
		}

		assert.Length(b, got, 1024, "the benchmark must measure the appended bytes")
	})
}

// appendsInOrder appends a sequence of payloads to an arena of any initial
// capacity. Each region must keep its payload across the appends after it,
// also across those that move the arena to a new backing array, and Bytes
// and Len must state the payloads in order.
func appendsInOrder(c *prop.Case) {
	a := arena.NewWithCapacity(c.Draw(capacities, "initialCap"))
	data := c.Draw(payloads, "payloads")

	regions := make([][]byte, 0, len(data))
	for _, d := range data {
		region := a.Append(d)
		assert.Equal(c, cap(region), len(region), "the capacity of a region must be its length")
		regions = append(regions, region)
	}

	want := slices.Concat(data...)
	assert.Equal(c, regions, data, "every region must keep its payload", assert.EquateEmpty())
	assert.Equal(c, a.Bytes(), want, "Bytes must return the payloads in order", assert.EquateEmpty())
	assert.Equal(c, cap(a.Bytes()), len(want), "the capacity of Bytes must be its length")
	assert.Equal(c, a.Len(), len(want), "Len must count the bytes of every payload")
}

// arenaSteps runs the steps of a machine over the methods of an Arena that
// change it, and checks every call against a model: the bytes of the
// current lifecycle, the number of lifecycles that Reset and Shrink ended,
// and the position and lifecycle of every Marker. Each region that Alloc
// returns is filled, so later calls find written bytes to zero.
func arenaSteps(c *prop.Case) {
	a := arena.NewWithCapacity(c.Draw(capacities, "initialCap"))

	var (
		want  []byte
		cycle int
		marks []marked
	)

	data := func(c *prop.Case, _ struct{}) any { return c.Draw(payload, "data") }
	marker := func(c *prop.Case, _ struct{}) any { return marks[c.Draw(prop.Integer(0, len(marks)-1), "marker")] }
	hasMarks := func(struct{}) bool { return len(marks) > 0 }

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:  "Append",
				Input: data,
				Run: func(c *prop.Case, _ int, in any) {
					v := in.([]byte)
					assert.Equal(c, a.Append(v), v, "Append must return its payload", assert.EquateEmpty())
					want = append(want, v...)
				},
			},
			{
				Name:  "Alloc",
				Input: func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer(0, 64), "n") },
				Run: func(c *prop.Case, _ int, in any) {
					n := in.(int)
					region := a.Alloc(n)
					assert.Equal(c, region, make([]byte, n), "Alloc must return zero bytes", assert.EquateEmpty())
					for i := range region {
						region[i] = fill
					}
					want = append(want, region...)
				},
			},
			{
				Name:  "AppendVia",
				Input: data,
				Run: func(c *prop.Case, _ int, in any) {
					v := in.([]byte)
					region, err := a.AppendVia(func(dst []byte) ([]byte, error) { return append(dst, v...), nil })
					assert.NoError(c, err, "AppendVia must return the appender's success")
					assert.Equal(c, region, v, "AppendVia must return what the appender appended", assert.EquateEmpty())
					want = append(want, v...)
				},
			},
			{
				Name:  "AppendVia with an error",
				Input: data,
				Run: func(c *prop.Case, _ int, in any) {
					region, err := a.AppendVia(func(dst []byte) ([]byte, error) {
						_ = append(dst, in.([]byte)...)

						return nil, errEncode
					})
					assert.ErrorIs(c, err, errEncode, "AppendVia must return the appender's error")
					assert.Nil(c, region, "AppendVia must return no region with an error")
				},
			},
			{
				Name: "Mark",
				Run: func(*prop.Case, int, any) {
					marks = append(marks, marked{m: a.Mark(), pos: len(want), cycle: cycle})
				},
			},
			{
				Name:    "SliceSince",
				Enabled: hasMarks,
				Input:   marker,
				Run: func(c *prop.Case, _ int, in any) {
					mk := in.(marked)
					var tail []byte
					if mk.cycle == cycle && mk.pos < len(want) {
						tail = want[mk.pos:]
					}
					assert.Equal(c, a.SliceSince(mk.m), tail, "SliceSince must return the bytes after a current marker")
				},
			},
			{
				Name:    "TruncateTo",
				Enabled: hasMarks,
				Input:   marker,
				Run: func(c *prop.Case, _ int, in any) {
					mk := in.(marked)
					current := mk.cycle == cycle && mk.pos <= len(want)
					assert.Equal(c, a.TruncateTo(mk.m), current,
						"TruncateTo must return true for a current marker at or before the end")
					if current {
						want = want[:mk.pos]
					}
				},
			},
			{
				Name: "Reset",
				Run: func(c *prop.Case, _ int, _ any) {
					a.Reset()
					want, cycle = want[:0], cycle+1
					assert.Equal(c, spare(c, a), make([]byte, a.Cap()), "Reset must zero the backing buffer",
						assert.EquateEmpty())
				},
			},
			{
				Name: "Shrink",
				Run: func(c *prop.Case, _ int, _ any) {
					a.Shrink()
					want, cycle = want[:0], cycle+1
					assert.Equal(c, a.Cap(), 0, "Shrink must release the backing buffer")
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			assert.Equal(c, a.Bytes(), want, "Bytes must return the bytes of the model", assert.EquateEmpty())
		},
	})
}
