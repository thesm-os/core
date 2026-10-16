// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"bytes"
	"cmp"
	"math"
	"slices"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/arena"
)

// The names of the calls that the machines of a Bump make and record.
const (
	opReserve = "Reserve"
	opLen     = "Len"
	opReset   = "Reset"
)

// The workloads of the cases of a Bump.
const (
	// bumpCapacity is the capacity of the Bump of the machines. It is small,
	// so the machines fill it.
	bumpCapacity = 16

	// tileCapacity is the capacity of the Bump that four goroutines fill.
	tileCapacity = 4096

	// largestReserve is the largest n of a Reserve of the goroutines that
	// fill a Bump of tileCapacity values.
	largestReserve = 8
)

// bumpContract is the contract of bumpSteps, which TestBump and FuzzBump
// check.
const bumpContract = "every reserved range must keep its own values, and Len must sum the ranges"

// reservation is what a Reserve returns: the index of the first value and
// whether Reserve reserved the values.
type reservation struct {
	at int
	ok bool
}

// reserved is a range that a Reserve returned: its first index, its length
// and the byte that fills it.
type reserved struct {
	at, n int
	v     byte
}

// filling is what a goroutine that fills a Bump reserved: its ranges, and
// the n of the Reserve that did not find room.
type filling struct {
	ranges  []reserved
	refused int
}

func TestBump(t *testing.T) {
	t.Parallel()

	t.Run("NewBump", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an empty Bump with room for capacity values", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[uint32](8)
			expect.Equal(t, b.Len(), 0, "a new Bump has no reserved values")
			expect.Length(t, b.Values(), 8, "a new Bump has room for capacity values")
		})

		t.Run("returns a Bump without values for a negative capacity", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[byte](-1)
			_, ok := b.Reserve(1)
			expect.Empty(t, b.Values(), "the Bump has no values")
			expect.False(t, ok, "Reserve finds no room in the Bump")
		})
	})

	t.Run("Reserve", func(t *testing.T) {
		t.Parallel()

		t.Run("returns consecutive ranges", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[byte](8)
			first, ok := b.Reserve(3)
			assert.True(t, ok, "Reserve reserves the first range")
			second, ok := b.Reserve(2)
			assert.True(t, ok, "Reserve reserves the second range")
			expect.Equal(t, first, 0, "the first range starts at index 0")
			expect.Equal(t, second, 3, "the second range starts after the first")
			expect.Equal(t, b.Len(), 5, "Len counts the values of both ranges")
		})

		t.Run("returns zero values", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[byte](8)
			at, ok := b.Reserve(8)
			assert.True(t, ok, "Reserve reserves every value")
			assert.Equal(t, b.Values()[at:at+8], make([]byte, 8), "the reserved values are zero")
		})

		t.Run("reports false for more values than remain", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[byte](8)
			_, ok := b.Reserve(6)
			assert.True(t, ok, "Reserve reserves the first range")
			assert.Pure(t, b.Len, func() { _, ok = b.Reserve(3) }, "a Reserve that does not fit changes nothing")
			assert.False(t, ok, "Reserve reports that 3 values do not fit")
		})

		t.Run("reserves the values that remain after a Reserve that did not fit", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[byte](8)
			_, ok := b.Reserve(6)
			assert.True(t, ok, "Reserve reserves the first range")
			_, ok = b.Reserve(3)
			assert.False(t, ok, "Reserve reports that 3 values do not fit")
			at, ok := b.Reserve(2)
			assert.True(t, ok, "Reserve reserves the 2 values that remain")
			assert.Equal(t, at, 6, "the range starts after the first range")
		})

		t.Run("reports false for a negative n", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[byte](8)
			var ok bool
			assert.Pure(t, b.Len, func() { _, ok = b.Reserve(-1) }, "a Reserve of a negative n changes nothing")
			assert.False(t, ok, "Reserve refuses a negative n")
		})

		t.Run("returns the number of reserved values for an n of 0", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[byte](8)
			_, ok := b.Reserve(5)
			assert.True(t, ok, "Reserve reserves the first range")
			at, ok := b.Reserve(0)
			assert.True(t, ok, "Reserve reserves no values")
			expect.Equal(t, at, 5, "the empty range starts at the end of the reserved values")
			expect.Equal(t, b.Len(), 5, "a Reserve of 0 values reserves nothing")
		})

		t.Run("reports false on the zero Bump for an n above 0", func(t *testing.T) {
			t.Parallel()
			var b arena.Bump[byte]
			_, ok := b.Reserve(1)
			assert.False(t, ok, "the zero Bump has no room")
		})

		t.Run("returns what one sequence of calls returns for concurrent calls", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "concurrent calls must linearize as the calls of one sequence", func(c *prop.Case) {
				b := arena.NewBump[byte](bumpCapacity)
				stateful.Steps(c, stateful.Machine[int]{
					Spec: history.Spec[int]{
						Initial: func() int { return 0 },
						Next:    stepBump,
					},
					Actions: []stateful.Action[int]{
						{
							Name:  opReserve,
							Input: func(c *prop.Case, _ int) any { return c.Draw(prop.Integer(-1, 6), "n") },
							Run: func(c *prop.Case, client int, in any) {
								call := c.History().Invoke(client, opReserve, []any{in})
								at, ok := b.Reserve(in.(int))
								call.OK(reservation{at: at, ok: ok})
							},
						},
						{
							Name: opLen,
							Run: func(c *prop.Case, client int, _ any) {
								call := c.History().Invoke(client, opLen, nil)
								call.OK(b.Len())
							},
						},
					},
				}, stateful.Clients(2))
			})
		})

		t.Run("returns ranges that tile the reserved values under concurrent calls", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[byte](tileCapacity)
			outcomes := history.Concurrently(4, time.Minute, func(client int) (any, error) {
				var f filling
				for n := client%largestReserve + 1; ; n = n%largestReserve + 1 {
					at, ok := b.Reserve(n)
					if !ok {
						f.refused = n

						return f, nil
					}
					f.ranges = append(f.ranges, reserved{at: at, n: n})
				}
			})

			var ranges []reserved
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every goroutine finishes")
				f := o.Output.(filling)
				expect.InRange(t, f.refused, float64(tileCapacity-b.Len()+1), largestReserve,
					"a Reserve finds no room only for an n above the values that remain")
				ranges = append(ranges, f.ranges...)
			}
			slices.SortFunc(ranges, func(a, b reserved) int { return cmp.Compare(a.at, b.at) })
			assert.NotEmpty(t, ranges, "the goroutines reserve ranges")
			expect.Equal(t, ranges[0].at, 0, "the first range starts at index 0")
			expect.Pairwise(t, ranges, func(earlier, later reserved) bool { return earlier.at+earlier.n == later.at },
				"each range starts where the range before it ends")
			expect.Equal(t, ranges[len(ranges)-1].at+ranges[len(ranges)-1].n, b.Len(),
				"the last range ends at the number of reserved values")
		})
	})

	t.Run("Values", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the same array on every call", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[byte](8)
			first := b.Values()
			_, ok := b.Reserve(4)
			assert.True(t, ok, "Reserve reserves the range")
			assert.Equal(t, &b.Values()[0], &first[0], "Values returns the array of the first call",
				assert.ByIdentity())
		})

		t.Run("returns an array whose capacity is its length", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[byte](8)
			assert.Equal(t, cap(b.Values()), 8, "an append to the values does not write past the array")
		})

		t.Run("keeps the values that a caller writes into its range", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[uint32](8)
			at, ok := b.Reserve(2)
			assert.True(t, ok, "Reserve reserves the range")
			b.Values()[at], b.Values()[at+1] = 7, 9
			assert.Equal(t, b.Values()[at:at+2], []uint32{7, 9}, "the array keeps the written values")
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the number of reserved values", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[byte](8)
			for _, n := range []int{3, 4} {
				_, ok := b.Reserve(n)
				assert.True(t, ok, "Reserve reserves the range")
			}
			assert.Equal(t, b.Len(), 7, "Len sums the reserved ranges")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("ends every reservation", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[byte](8)
			_, ok := b.Reserve(8)
			assert.True(t, ok, "Reserve reserves every value")
			b.Reset()
			assert.Equal(t, b.Len(), 0, "Reset ends the reservations")
			at, ok := b.Reserve(8)
			assert.True(t, ok, "Reserve reserves every value again")
			assert.Equal(t, at, 0, "the next range starts at index 0")
		})

		t.Run("zeroes the reserved values", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[byte](8)
			at, ok := b.Reserve(5)
			assert.True(t, ok, "Reserve reserves the range")
			copy(b.Values()[at:at+5], bytes.Repeat([]byte{fill}, 5))
			b.Reset()
			assert.Equal(t, b.Values(), make([]byte, 8), "Reset zeroes every reserved value")
		})

		t.Run("keeps the array", func(t *testing.T) {
			t.Parallel()
			b := arena.NewBump[byte](8)
			before := b.Values()
			b.Reset()
			assert.Equal(t, &b.Values()[0], &before[0], "Reset keeps the array", assert.ByIdentity())
		})
	})

	t.Run("keeps the reserved ranges apart under any sequence of calls", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, bumpContract, bumpSteps)
	})
}

// FuzzBump checks the machine of bumpSteps on the call sequences that a
// fuzzer finds.
func FuzzBump(f *testing.F) {
	prop.Fuzz(f, bumpContract, bumpSteps)
}

// TestBumpAllocs checks the allocation contract of each method of a Bump.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestBumpAllocs(t *testing.T) {
	t.Run("Reserve", func(t *testing.T) {
		b := arena.NewBump[byte](1024)

		var ok bool
		expect.MaxAllocs(t, func() { _, ok = b.Reserve(1) }, 0, "Reserve does not allocate")
		assert.True(t, ok, "the test measures a Reserve that succeeds")
	})

	t.Run("Values", func(t *testing.T) {
		b := arena.NewBump[byte](1024)

		var got []byte
		expect.MaxAllocs(t, func() { got = b.Values() }, 0, "Values does not allocate")
		assert.Length(t, got, 1024, "the test measures the array")
	})

	t.Run("Len", func(t *testing.T) {
		b := arena.NewBump[byte](1024)
		_, ok := b.Reserve(10)
		assert.True(t, ok, "Reserve reserves the range")

		var got int
		expect.MaxAllocs(t, func() { got = b.Len() }, 0, "Len does not allocate")
		assert.Equal(t, got, 10, "the test measures the reserved range")
	})

	t.Run("Reset", func(t *testing.T) {
		b := arena.NewBump[byte](1024)

		var ok bool
		expect.MaxAllocs(t, func() {
			_, ok = b.Reserve(64)
			b.Reset()
		}, 0, "Reset does not allocate")
		assert.True(t, ok, "the test measures a Reset of a reserved range")
	})
}

// BenchmarkBump reports the cost of each method of a Bump on a hot path, and
// fails when a method allocates.
func BenchmarkBump(b *testing.B) {
	b.Run("Reserve", func(b *testing.B) {
		b.Run("and Reset", func(b *testing.B) {
			bp := arena.NewBump[byte](64)

			var ok bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, ok = bp.Reserve(30)
				bp.Reset()
			}

			assert.True(b, ok, "the benchmark measures a Reserve that succeeds")
		})

		// A value of struct{} takes no memory, so the capacity of the Bump
		// does not run out within the benchmark.
		b.Run("by goroutines in parallel", func(b *testing.B) {
			bp := arena.NewBump[struct{}](math.MaxInt)

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			c.RunParallel(func(pb *bench.PB) {
				for pb.Next() {
					bp.Reserve(30)
				}
			})

			assert.NotEqual(b, bp.Len(), 0, "the benchmark measures Reserves that succeed")
		})
	})

	b.Run("Len", func(b *testing.B) {
		bp := arena.NewBump[byte](64)
		_, ok := bp.Reserve(30)
		assert.True(b, ok, "Reserve reserves the range")

		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = bp.Len()
		}

		assert.Equal(b, got, 30, "the benchmark measures the reserved range")
	})
}

// stepBump returns the number of reserved values of a Bump of bumpCapacity
// values after op, from a Bump with state reserved values. It returns no
// state when the Bump cannot have returned the output of op.
func stepBump(state int, op history.Operation) []int {
	if op.Name == opLen {
		if op.Returned(state) {
			return []int{state}
		}

		return nil
	}
	n := op.Args[0].(int)
	if n < 0 || n > bumpCapacity-state {
		if op.Returned(reservation{}) {
			return []int{state}
		}

		return nil
	}
	if op.Returned(reservation{at: state, ok: true}) {
		return []int{state + n}
	}

	return nil
}

// bumpSteps runs the steps of a machine over Reserve and Reset of a Bump.
// Each Reserve fills its range with a byte of its own, so two ranges that
// share values change each other's bytes. The invariant checks every range
// in use, the zero values past the ranges and Len after each step.
func bumpSteps(c *prop.Case) {
	b := arena.NewBump[byte](bumpCapacity)

	var (
		inUse []reserved
		total int
		next  byte
	)

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:   opReserve,
				Weight: 4,
				Input:  func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer(-1, 6), "n") },
				Run: func(c *prop.Case, _ int, in any) {
					n := in.(int)
					at, ok := b.Reserve(n)
					if n < 0 || n > bumpCapacity-total {
						assert.False(c, ok, "Reserve reports false when n does not fit")

						return
					}
					assert.True(c, ok, "Reserve reserves n values that fit")
					assert.Equal(c, at, total, "the range starts at the end of the reserved values")
					assert.Equal(c, b.Values()[at:at+n], make([]byte, n), "the reserved values are zero")

					next = next%254 + 1
					copy(b.Values()[at:at+n], bytes.Repeat([]byte{next}, n))
					inUse = append(inUse, reserved{at: at, n: n, v: next})
					total += n
				},
			},
			{
				Name: opReset,
				Run: func(c *prop.Case, _ int, _ any) {
					b.Reset()
					inUse, total = inUse[:0], 0
					assert.Equal(c, b.Values(), make([]byte, bumpCapacity), "Reset zeroes every reserved value")
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			assert.Equal(c, b.Len(), total, "Len sums the reserved ranges")
			for _, r := range inUse {
				assert.Equal(c, bytes.Count(b.Values()[r.at:r.at+r.n], []byte{r.v}), r.n,
					"no other range writes into a range in use")
			}
			assert.Equal(c, b.Values()[total:], make([]byte, bumpCapacity-total),
				"the values past the reserved ranges are zero", assert.EquateEmpty())
		},
	})
}
