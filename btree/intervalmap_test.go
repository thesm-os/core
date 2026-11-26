// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package btree_test

import (
	"bytes"
	"cmp"
	"fmt"
	"iter"
	"math/rand/v2"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/btree"
)

// Sizes of the maps of the tests of IntervalMap.
const (
	// intervalPoints bounds the starts of the intervals of a random map, so
	// that many intervals overlap each point.
	intervalPoints = 1_000

	// intervalLength bounds the distance from the start to the end of an
	// interval of a random map.
	intervalLength = 100

	// manyIntervals is the number of intervals of a random map. The map has
	// two levels of internal nodes.
	manyIntervals = 20_000

	// benchIntervals is the number of intervals of the maps of the
	// benchmarks.
	benchIntervals = 1 << 16

	// nodeChildren is the capacity of an internal node in children, which
	// the package documents.
	nodeChildren = 64

	// descentComparisons bounds the comparisons of a query that ends at the
	// first interval of a map of manyIntervals intervals in ascending order:
	// the query itself, one max end at each of the two levels of internal
	// nodes, and the first interval, with room to spare.
	descentComparisons = 8
)

// span is an interval of int points.
type span = btree.Interval[int]

// The intervals of the map of fixedIntervals, and fixedSpans, the list of
// them in the order of the map.
var (
	fromZero      = span{Start: 0, End: 10}
	fromZeroToTen = span{Start: 0, End: 10, EndBound: btree.Included}
	emptyAtThree  = span{Start: 3, End: 3}
	fromFive      = span{Start: 5, EndBound: btree.Unbounded}
	sevenOnly     = span{Start: 7, End: 7, EndBound: btree.Included}
	fromTen       = span{Start: 10, End: 20}
	fixedSpans    = []span{fromZero, fromZeroToTen, emptyAtThree, fromFive, sevenOnly, fromTen}
)

func TestIntervalMap(t *testing.T) {
	t.Parallel()

	t.Run("NewIntervalMap", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an empty map", func(t *testing.T) {
			t.Parallel()
			m := btree.NewIntervalMap[int, int](cmp.Compare[int])
			expect.Equal(t, m.Len(), 0, "a new map must contain no interval")
			expect.Empty(t, spansOf(m.Containing(0), 1), "a new map must contain no point")
		})

		t.Run("panics on a nil function", func(t *testing.T) {
			t.Parallel()
			want := any("btree: NewIntervalMap with a nil comparison function")
			got := assert.Panics(t, func() { btree.NewIntervalMap[int, int](nil) }, "a map without an order must panic")
			assert.Equal(t, got, want, "the panic must name the cause")
		})
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of a present interval", func(t *testing.T) {
			t.Parallel()
			m := fixedIntervals()
			v, ok := m.Get(fromTen)
			assert.True(t, ok, "Get must find a present interval")
			assert.Equal(t, v, 5, "Get must return the value of the interval")
		})

		t.Run("reports an interval with another bound as absent", func(t *testing.T) {
			t.Parallel()
			m := fixedIntervals()
			v, ok := m.Get(span{Start: 10, End: 20, EndBound: btree.Included})
			expect.False(t, ok, "an Included end must differ from an Excluded end of the same point")
			expect.Equal(t, v, 0, "a miss must return the zero value")
		})

		t.Run("finds an Unbounded interval whatever its End", func(t *testing.T) {
			t.Parallel()
			m := fixedIntervals()
			v, ok := m.Get(span{Start: 5, End: 99, EndBound: btree.Unbounded})
			assert.True(t, ok, "an Unbounded interval must not use its End")
			assert.Equal(t, v, 3, "Get must return the value of the Unbounded interval")
		})

		t.Run("treats a Bound other than Included and Unbounded as Excluded", func(t *testing.T) {
			t.Parallel()
			m := fixedIntervals()
			v, ok := m.Get(span{Start: 10, End: 20, EndBound: btree.Unbounded + 1})
			assert.True(t, ok, "an unknown Bound must count as Excluded")
			assert.Equal(t, v, 5, "Get must return the value of the Excluded interval")
		})
	})

	t.Run("Has", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for a present interval", func(t *testing.T) {
			t.Parallel()
			assert.True(t, fixedIntervals().Has(emptyAtThree), "Has must report a present interval")
		})

		t.Run("reports false for an absent interval", func(t *testing.T) {
			t.Parallel()
			assert.False(t, fixedIntervals().Has(span{Start: 3, End: 4}), "Has must report an absent interval")
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for a new interval", func(t *testing.T) {
			t.Parallel()
			m := fixedIntervals()
			old, ok := m.Set(span{Start: 1, End: 2}, 9)
			expect.False(t, ok, "Set must report a new interval")
			expect.Equal(t, old, 0, "Set of a new interval must return the zero value")
		})

		t.Run("returns the previous value of a present interval", func(t *testing.T) {
			t.Parallel()
			m := fixedIntervals()
			old, ok := m.Set(fromFive, 9)
			assert.True(t, ok, "Set must report a present interval")
			assert.Equal(t, old, 3, "Set must return the previous value")
		})

		t.Run("keeps the stored End of an Unbounded interval", func(t *testing.T) {
			t.Parallel()
			m := btree.NewIntervalMap[int, int](cmp.Compare[int])
			m.Set(span{Start: 1, End: 9, EndBound: btree.Unbounded}, 1)
			m.Set(span{Start: 1, End: 3, EndBound: btree.Unbounded}, 2)
			assert.Equal(t, spansOf(m.All(), 2), []span{{Start: 1, End: 9, EndBound: btree.Unbounded}},
				"two Unbounded intervals of one Start must be one interval")
		})
	})

	t.Run("Update", func(t *testing.T) {
		t.Parallel()

		t.Run("passes fn the value of a present interval", func(t *testing.T) {
			t.Parallel()
			m := fixedIntervals()
			var old int
			var exists bool
			m.Update(sevenOnly, func(o int, e bool) int {
				old, exists = o, e

				return o
			})
			assert.True(t, exists, "fn must receive a present interval")
			assert.Equal(t, old, 4, "fn must receive the value")
		})

		t.Run("stores the result of fn", func(t *testing.T) {
			t.Parallel()
			m := fixedIntervals()
			got := m.Update(sevenOnly, func(int, bool) int { return 40 })
			v, _ := m.Get(sevenOnly)
			expect.Equal(t, got, 40, "Update must return the new value")
			expect.Equal(t, v, 40, "Update must store the new value")
		})
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of a present interval", func(t *testing.T) {
			t.Parallel()
			m := fixedIntervals()
			old, ok := m.Delete(fromZeroToTen)
			assert.True(t, ok, "Delete must report a present interval")
			assert.Equal(t, old, 1, "Delete must return the value")
		})

		t.Run("removes the interval from the queries", func(t *testing.T) {
			t.Parallel()
			m := fixedIntervals()
			m.Delete(fromZeroToTen)
			assert.Equal(t, spansOf(m.Containing(10), 4), []span{fromFive, fromTen},
				"a deleted interval must not contain a point")
		})

		t.Run("lets the caller reuse the bytes of the interval that ends last", func(t *testing.T) {
			t.Parallel()
			m, last := byteIntervals()
			_, ok := m.Delete(last)
			assert.True(t, ok, "Delete must remove the interval")
			clear(last.Start)
			clear(last.End)
			for _, p := range [][]byte{[]byte("0000"), []byte("0500"), []byte("0998")} {
				want := 0
				for iv := range m.All() {
					if bytes.Compare(iv.Start, p) <= 0 && bytes.Compare(p, iv.End) < 0 {
						want++
					}
				}
				got := 0
				for range m.Containing(p) {
					got++
				}
				assert.Equal(t, got, want, "a query must not read the bytes of a removed interval")
			}
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the intervals", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, fixedIntervals().Len(), len(fixedSpans), "Len must count the intervals")
		})
	})

	t.Run("Clear", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every interval", func(t *testing.T) {
			t.Parallel()
			m := randomMap(1, manyIntervals)
			m.Clear()
			expect.Equal(t, m.Len(), 0, "Clear must remove every interval")
			expect.Empty(t, spansOf(m.Containing(intervalPoints/2), 1), "a cleared map must contain no point")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the queries of a refilled map", func(t *testing.T) {
			t.Parallel()
			m := randomMap(2, manyIntervals)
			m.Reset()
			assert.Equal(t, m.Len(), 0, "Reset must remove every interval")
			for i, iv := range randomIntervals(3, manyIntervals) {
				m.Set(iv, i)
			}
			for p := -1; p <= intervalPoints+intervalLength; p += 7 {
				requireQuery(t, m, m.Containing(p), func(iv span) bool { return contains(iv, p) },
					"the refilled map must yield the intervals that contain the point")
			}
		})
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the intervals by start and then by end", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, spansOf(fixedIntervals().All(), len(fixedSpans)), fixedSpans,
				"All must yield the intervals in their order")
		})
	})

	t.Run("Containing", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			givePoint int
			want      []span
		}{
			{name: "yields no interval for a point before every start", givePoint: -1},
			{name: "yields the intervals that start at the point", givePoint: 0, want: []span{fromZero, fromZeroToTen}},
			{
				name:      "does not yield an empty interval that starts at the point",
				givePoint: 3,
				want:      []span{fromZero, fromZeroToTen},
			},
			{
				name:      "yields an Unbounded interval at its start",
				givePoint: 5,
				want:      []span{fromZero, fromZeroToTen, fromFive},
			},
			{
				name:      "yields an Included interval of one point",
				givePoint: 7,
				want:      []span{fromZero, fromZeroToTen, fromFive, sevenOnly},
			},
			{
				name:      "yields an Included interval at its end",
				givePoint: 10,
				want:      []span{fromZeroToTen, fromFive, fromTen},
			},
			{name: "yields an Unbounded interval after every end", givePoint: 25, want: []span{fromFive}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, spansOf(fixedIntervals().Containing(tt.givePoint), len(fixedSpans)), tt.want,
					"Containing must yield the intervals that contain the point")
			})
		}

		t.Run("yields the intervals that contain each point of a map with internal nodes", func(t *testing.T) {
			t.Parallel()
			m := randomMap(4, manyIntervals)
			for p := -1; p <= intervalPoints+intervalLength; p++ {
				requireQuery(t, m, m.Containing(p), func(iv span) bool { return contains(iv, p) },
					"Containing must yield the intervals that contain the point")
			}
		})

		t.Run("yields the intervals that contain each point after deletes of most intervals", func(t *testing.T) {
			t.Parallel()
			ivs := randomIntervals(5, manyIntervals)
			m := btree.NewIntervalMap[int, int](cmp.Compare[int])
			for i, iv := range ivs {
				m.Set(iv, i)
			}
			for _, iv := range ivs[:manyIntervals*9/10] {
				m.Delete(iv)
			}
			for p := -1; p <= intervalPoints+intervalLength; p++ {
				requireQuery(t, m, m.Containing(p), func(iv span) bool { return contains(iv, p) },
					"Containing must yield the intervals that remain")
			}
		})

		t.Run("yields each interval once while each yield deletes its interval", func(t *testing.T) {
			t.Parallel()
			m := randomMap(6, manyIntervals)
			const p = intervalPoints / 2
			var want []span
			for iv := range m.All() {
				if contains(iv, p) {
					want = append(want, iv)
				}
			}
			var got []span
			for iv := range m.Containing(p) {
				if got = append(got, iv); len(got) > len(want) {
					break
				}
				m.Delete(iv)
			}
			expect.Equal(t, got, want, "the iteration must yield every interval that contains the point once")
			expect.Empty(t, spansOf(m.Containing(p), 1), "every interval that contains the point must be gone")
		})

		t.Run("yields an interval that a yield inserts after the interval of the yield", func(t *testing.T) {
			t.Parallel()
			m := fixedIntervals()
			late := span{Start: 6, End: 8}
			var got []span
			for iv := range m.Containing(7) {
				if got = append(got, iv); len(got) > len(fixedSpans) {
					break
				}
				if iv == fromZero {
					m.Set(late, 9)
					m.Set(span{Start: -1, End: 9}, 9)
				}
			}
			assert.Equal(t, got, []span{fromZero, fromZeroToTen, fromFive, late, sevenOnly},
				"the iteration must yield the interval inserted ahead and not the one inserted behind")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, spansOf(fixedIntervals().Containing(7), 1), []span{fromZero},
				"the iteration must end at the break of the loop")
		})

		t.Run("stops in a map with internal nodes when the loop breaks", func(t *testing.T) {
			t.Parallel()
			assert.Length(t, spansOf(randomMap(15, manyIntervals).Containing(intervalPoints/2), 1), 1,
				"the iteration must end at the break of the loop")
		})

		t.Run("yields each interval once in a map with internal nodes while each yield inserts an interval",
			func(t *testing.T) {
				t.Parallel()
				m := btree.NewIntervalMap[int, int](cmp.Compare[int])
				for i, iv := range randomIntervals(16, manyIntervals) {
					iv.Start, iv.End = iv.Start-intervalPoints/2, iv.End-intervalPoints/2
					m.Set(iv, i)
				}
				const p = -intervalPoints / 4
				var want []span
				for iv := range m.All() {
					if contains(iv, p) {
						want = append(want, iv)
					}
				}
				var got []span
				for iv := range m.Containing(p) {
					if got = append(got, iv); len(got) > len(want) {
						break
					}
					m.Set(span{Start: 2*intervalPoints + len(got), End: 2*intervalPoints + len(got)}, 0)
				}
				assert.Equal(t, got, want, "the iteration must yield each interval that contains the point once")
			})

		t.Run("compares the point with the max ends of the children of the root alone after every end",
			func(t *testing.T) {
				t.Parallel()
				compared := 0
				m := btree.NewIntervalMap[int, int](func(a, b int) int {
					compared++

					return cmp.Compare(a, b)
				})
				for i := range manyIntervals {
					m.Set(span{Start: 2 * i, End: 2*i + 1}, i)
				}
				compared = 0
				assert.Empty(t, spansOf(m.Containing(2*manyIntervals), 1), "no interval may contain the point")
				assert.InRange(t, compared, 1, nodeChildren+1,
					"the query must compare the point with the max end of each child of the root and no interval")
			})
	})

	t.Run("Overlapping", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			giveQuery span
			want      []span
		}{
			{
				name:      "yields the intervals that share a point with the query",
				giveQuery: span{Start: 3, End: 6},
				want:      []span{fromZero, fromZeroToTen, fromFive},
			},
			{
				name:      "yields an Included interval that ends at the start of the query",
				giveQuery: span{Start: 10, End: 15},
				want:      []span{fromZeroToTen, fromFive, fromTen},
			},
			{
				name:      "yields an interval that starts at the Included end of the query",
				giveQuery: span{Start: -5, End: 0, EndBound: btree.Included},
				want:      []span{fromZero, fromZeroToTen},
			},
			{
				name:      "does not yield an interval that starts at the Excluded end of the query",
				giveQuery: span{Start: -5},
			},
			{
				name:      "yields the intervals that end after the start of an Unbounded query",
				giveQuery: span{Start: 20, EndBound: btree.Unbounded},
				want:      []span{fromFive},
			},
			{name: "yields no interval for an empty query", giveQuery: span{Start: 4, End: 4}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, spansOf(fixedIntervals().Overlapping(tt.giveQuery), len(fixedSpans)), tt.want,
					"Overlapping must yield the intervals that share a point with the query")
			})
		}

		t.Run("yields the intervals that overlap each query of a map with internal nodes", func(t *testing.T) {
			t.Parallel()
			m := randomMap(7, manyIntervals)
			for _, q := range randomIntervals(8, 2_000) {
				q.Start -= intervalLength / 2
				requireQuery(t, m, m.Overlapping(q), func(iv span) bool { return overlaps(iv, q) },
					"Overlapping must yield the intervals that share a point with the query")
			}
		})

		t.Run("yields each interval once while each yield deletes its interval", func(t *testing.T) {
			t.Parallel()
			m := randomMap(9, manyIntervals)
			q := span{Start: intervalPoints / 4, End: intervalPoints / 2}
			var want []span
			for iv := range m.All() {
				if overlaps(iv, q) {
					want = append(want, iv)
				}
			}
			var got []span
			for iv := range m.Overlapping(q) {
				if got = append(got, iv); len(got) > len(want) {
					break
				}
				m.Delete(iv)
			}
			expect.Equal(t, got, want, "the iteration must yield every interval that overlaps the query once")
			expect.Empty(t, spansOf(m.Overlapping(q), 1), "every interval that overlaps the query must be gone")
		})

		t.Run("ends at the first interval that starts after the end of the query", func(t *testing.T) {
			t.Parallel()
			compared := 0
			m := btree.NewIntervalMap[int, int](func(a, b int) int {
				compared++

				return cmp.Compare(a, b)
			})
			for i := range manyIntervals {
				m.Set(span{Start: 2 * i, End: 2*i + 1}, i)
			}
			compared = 0
			assert.Empty(t, spansOf(m.Overlapping(span{Start: -10, End: -5}), 1), "no interval may overlap the query")
			assert.InRange(t, compared, 1, descentComparisons, "the query must end at the first interval of the map")
		})
	})
}

// TestIntervalMapAllocs checks that the queries, Get, and a Set after a
// Delete of a map whose size does not change do not allocate. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
func TestIntervalMapAllocs(t *testing.T) {
	m := randomMap(10, manyIntervals)
	iv := randomIntervals(10, 1)[0]

	t.Run("Get", func(t *testing.T) {
		var ok bool
		expect.MaxAllocs(t, func() { _, ok = m.Get(iv) }, 0, "Get must not allocate")
		assert.True(t, ok, "the test must measure a present interval")
	})

	t.Run("Set", func(t *testing.T) {
		var present bool
		expect.MaxAllocs(t, func() {
			m.Delete(iv)
			_, present = m.Set(iv, 0)
		}, 0, "a Set after a Delete of the same interval must not allocate")
		assert.False(t, present, "the test must measure the Set of an absent interval")
	})

	t.Run("Containing", func(t *testing.T) {
		n := 0
		expect.MaxAllocs(t, func() {
			n = 0
			for range m.Containing(intervalPoints / 2) {
				n++
			}
		}, 0, "a range loop over Containing must not allocate")
		assert.NotEqual(t, n, 0, "the test must measure a point that intervals contain")
	})

	t.Run("Overlapping", func(t *testing.T) {
		q := span{Start: intervalPoints / 4, End: intervalPoints / 2}
		n := 0
		expect.MaxAllocs(t, func() {
			n = 0
			for range m.Overlapping(q) {
				n++
			}
		}, 0, "a range loop over Overlapping must not allocate")
		assert.NotEqual(t, n, 0, "the test must measure a query that intervals overlap")
	})
}

// BenchmarkIntervalMap reports the cost of the queries, Get and Set on a map
// of benchIntervals intervals, and fails when one of them allocates.
func BenchmarkIntervalMap(b *testing.B) {
	b.Run("Containing", func(b *testing.B) {
		b.Run("of a point after the end of every interval", func(b *testing.B) {
			m := btree.NewIntervalMap[int, int](cmp.Compare[int])
			for i := range benchIntervals {
				m.Set(span{Start: 2 * i, End: 2*i + 1}, i)
			}
			n := 0

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				for range m.Containing(2 * benchIntervals) {
					n++
				}
			}

			assert.Equal(b, n, 0, "the benchmark must measure a point that no interval contains")
		})

		b.Run("of a point that intervals contain", func(b *testing.B) {
			m := randomMap(11, benchIntervals)
			n := 0

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				for range m.Containing(intervalPoints / 2) {
					n++
				}
			}

			assert.NotEqual(b, n, 0, "the benchmark must measure a point that intervals contain")
		})
	})

	b.Run("Overlapping", func(b *testing.B) {
		m := randomMap(12, benchIntervals)
		q := span{Start: intervalPoints / 2, End: intervalPoints/2 + 1}
		n := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			for range m.Overlapping(q) {
				n++
			}
		}

		assert.NotEqual(b, n, 0, "the benchmark must measure a query that intervals overlap")
	})

	b.Run("Get", func(b *testing.B) {
		ivs := randomIntervals(13, benchIntervals)
		m := btree.NewIntervalMap[int, int](cmp.Compare[int])
		for i, iv := range ivs {
			m.Set(iv, i)
		}
		i, hits := 0, 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			if _, ok := m.Get(ivs[i]); ok {
				hits++
			}
			i = (i + 1) % benchIntervals
		}

		assert.NotEqual(b, hits, 0, "the benchmark must measure present intervals")
	})

	b.Run("Set", func(b *testing.B) {
		b.Run("of an interval that a Delete removed", func(b *testing.B) {
			ivs := randomIntervals(14, benchIntervals)
			m := btree.NewIntervalMap[int, int](cmp.Compare[int])
			for i, iv := range ivs {
				m.Set(iv, i)
			}
			i := 0

			var present bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				m.Delete(ivs[i])
				_, present = m.Set(ivs[i], i)
				i = (i + 1) % benchIntervals
			}

			assert.False(b, present, "the benchmark must measure the Set of an absent interval")
		})
	})
}

// fixedIntervals returns a map of the intervals of fixedSpans, each with
// its index in the list as its value.
func fixedIntervals() *btree.IntervalMap[int, int] {
	m := btree.NewIntervalMap[int, int](cmp.Compare[int])
	for i, iv := range fixedSpans {
		m.Set(iv, i)
	}

	return m
}

// randomIntervals returns n intervals from a source seeded with seed. Each
// starts below intervalPoints, ends less than intervalLength after its
// start, and is Excluded, Included or Unbounded with equal odds. Some are
// empty.
func randomIntervals(seed uint64, n int) []span {
	r := rand.New(rand.NewPCG(seed, 0))
	ivs := make([]span, n)
	for i := range ivs {
		start := r.IntN(intervalPoints)
		ivs[i] = span{Start: start, End: start + r.IntN(intervalLength), EndBound: btree.Bound(r.IntN(3))}
	}

	return ivs
}

// randomMap returns a map of randomIntervals(seed, n), each with its index as
// its value. An interval that comes again keeps the value of its last Set.
func randomMap(seed uint64, n int) *btree.IntervalMap[int, int] {
	m := btree.NewIntervalMap[int, int](cmp.Compare[int])
	for i, iv := range randomIntervals(seed, n) {
		m.Set(iv, i)
	}

	return m
}

// byteIntervals returns a map of the intervals of []byte points from k to
// k+50 for every k of the points 0000 to 0999 of four digits, and the
// interval from 0500 to 9999, which ends after every other interval. The
// value of each interval is its start.
func byteIntervals() (*btree.IntervalMap[[]byte, int], btree.Interval[[]byte]) {
	m := btree.NewIntervalMap[[]byte, int](bytes.Compare)
	for k := range intervalPoints {
		m.Set(btree.Interval[[]byte]{Start: fmt.Appendf(nil, "%04d", k), End: fmt.Appendf(nil, "%04d", k+50)}, k)
	}
	last := btree.Interval[[]byte]{Start: []byte("0500"), End: []byte("9999")}
	m.Set(last, intervalPoints/2)

	return m, last
}

// contains reports whether iv contains p, as Interval states it.
func contains(iv span, p int) bool {
	if p < iv.Start {
		return false
	}
	switch iv.EndBound {
	case btree.Unbounded:
		return true
	case btree.Included:
		return p <= iv.End
	default:
		return p < iv.End
	}
}

// overlaps reports whether a and b have a point in common. The first point
// that both can contain is the later of their starts.
func overlaps(a, b span) bool {
	first := max(a.Start, b.Start)

	return contains(a, first) && contains(b, first)
}

// spansOf returns the intervals that seq yields, up to limit of them, so an
// iteration that yields an interval again stops.
func spansOf[P any](seq iter.Seq2[btree.Interval[P], int], limit int) []btree.Interval[P] {
	var ivs []btree.Interval[P]
	for iv := range seq {
		if ivs = append(ivs, iv); len(ivs) >= limit {
			break
		}
	}

	return ivs
}

// requireQuery fails the test with the message msg unless seq yields the
// intervals of m that keep accepts, in the order of All.
func requireQuery(tb assert.TB, m *btree.IntervalMap[int, int], seq iter.Seq2[span, int], keep func(span) bool,
	msg string,
) {
	tb.Helper()
	var want []span
	for iv := range m.All() {
		if keep(iv) {
			want = append(want, iv)
		}
	}
	assert.Equal(tb, spansOf(seq, len(want)+1), want, msg)
}
