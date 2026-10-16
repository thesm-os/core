// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package skiplist

import (
	"encoding/binary"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
)

// hintedEntries is the number of entries that each writer of the concurrent
// case adds through its own Inserter.
const hintedEntries = 2000

func TestInserter(t *testing.T) {
	t.Parallel()

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		t.Run("adds entries in order that a cursor yields in order", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			var ins Inserter
			want := make([]entry, 0, 100)
			for i := range uint64(100) {
				e := entry{key: binary.BigEndian.AppendUint64(nil, i), seq: 1}
				assert.NoError(t, ins.Add(l, e.key, e.seq, e.value), "Add adds the entry")
				want = append(want, e)
			}
			assert.Equal(t, scan(t.Context(), l.NewIter()), want, "a cursor yields the entries in order",
				assert.EquateEmpty())
			assert.NoError(t, check(l), "the List keeps the invariants of its levels")
		})

		t.Run("adds entries in descending order that a cursor yields in order", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			var ins Inserter
			want := make([]entry, 100)
			for i := range uint64(100) {
				e := entry{key: binary.BigEndian.AppendUint64(nil, 99-i), seq: 1}
				assert.NoError(t, ins.Add(l, e.key, e.seq, e.value), "Add adds the entry before its last entry")
				want[99-i] = e
			}
			assert.Equal(t, scan(t.Context(), l.NewIter()), want, "a cursor yields the entries in order",
				assert.EquateEmpty())
			assert.NoError(t, check(l), "the List keeps the invariants of its levels")
		})

		hints := []struct {
			name      string
			giveReset bool
		}{
			{name: "searches from its last entry instead of the head", giveReset: false},
			{name: "searches from its last entry in a List that Reset emptied", giveReset: true},
		}
		for _, tt := range hints {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newList(t)
				if tt.giveReset {
					l.Reset()
				}
				var ins Inserter
				for _, k := range []string{"a", "b"} {
					assert.NoError(t, ins.Add(l, []byte(k), 1, nil), "Add adds the entry")
				}
				a, b := l.nodes[nodeHeader], ins.prev[0]
				// A search from the head or from a finds no node once neither of
				// them links a node.
				clear(l.nodes[nodeHeader : nodeHeader+maxHeight])
				clear(l.nodes[a+nodeHeader:][:l.height([]byte("a"), 1)])
				assert.NoError(t, ins.Add(l, []byte("c"), 1, nil), "Add adds c")
				assert.Equal(t, l.nodes[b+nodeHeader], ins.prev[0], "Add links c after b at level 0")
			})
		}

		t.Run("searches the levels below a level whose search moved from the node that it found", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			// first returns the first key from the number i on whose node has
			// more than 1 level when tall is true, and 1 level otherwise.
			first := func(i uint64, tall bool) []byte {
				for ; ; i++ {
					k := binary.BigEndian.AppendUint64(nil, i)
					if (l.height(k, 1) > 1) == tall {
						return k
					}
				}
			}
			x := first(0, false)
			after := first(binary.BigEndian.Uint64(x)+1, true)
			var ins Inserter
			assert.NoError(t, ins.Add(l, x, 1, nil), "Add adds an entry of 1 level")
			assert.NoError(t, l.Add(after, 1, nil), "List.Add adds a taller entry after it")
			xn := l.nodes[nodeHeader]
			an := l.nodes[xn+nodeHeader]
			// The hint of level 0 is the node of x, and a search of level 0 from
			// it now ends there.
			l.nodes[xn+nodeHeader] = 0
			y := binary.BigEndian.AppendUint64(nil, binary.BigEndian.Uint64(after)+1)
			assert.NoError(t, ins.Add(l, y, 1, nil), "Add adds an entry after the taller entry")
			assert.Equal(t, l.nodes[an+nodeHeader], ins.prev[0],
				"Add links the entry after the taller entry at level 0")
		})

		t.Run("returns ErrExists for its last entry", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			var ins Inserter
			assert.NoError(t, ins.Add(l, []byte("a"), 1, nil), "Add adds a")
			var err error
			assert.Pure(t, l.Size, func() { err = ins.Add(l, []byte("a"), 1, nil) },
				"Add of an entry of the List reserves nothing")
			assert.ErrorIs(t, err, ErrExists, "Add refuses the entry")
		})

		t.Run("returns ErrExists for an entry after its last entry", func(t *testing.T) {
			t.Parallel()
			l := listOf(t, entry{key: []byte("c"), seq: 1})
			var ins Inserter
			assert.NoError(t, ins.Add(l, []byte("a"), 1, nil), "Add adds a")
			assert.ErrorIs(t, ins.Add(l, []byte("c"), 1, nil), ErrExists, "Add refuses the entry of c")
		})

		t.Run("keeps its hint after an Add that fails", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			var ins Inserter
			assert.NoError(t, ins.Add(l, []byte("a"), 1, nil), "Add adds a")
			hint := ins.prev
			assert.ErrorIs(t, ins.Add(l, []byte("a"), 1, nil), ErrExists, "Add refuses a again")
			expect.Equal(t, ins.prev, hint, "the failed Add keeps the nodes of the hint")
			assert.NoError(t, ins.Add(l, []byte("b"), 1, nil), "Add adds b after the failed Add")
			assert.NoError(t, check(l), "the List keeps the invariants of its levels")
		})

		t.Run("searches from the head after Reset of the List", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			var ins Inserter
			for _, k := range []string{"a", "b", "c", "d"} {
				assert.NoError(t, ins.Add(l, []byte(k), 1, nil), "Add adds the entry")
			}
			l.Reset()
			assert.NoError(t, ins.Add(l, []byte("b"), 1, nil), "Add adds an entry after Reset")
			assert.Equal(t, scan(t.Context(), l.NewIter()), []entry{{key: []byte("b"), seq: 1}},
				"a cursor yields the entry after Reset", assert.EquateEmpty())
			assert.NoError(t, check(l), "the List keeps the invariants of its levels")
		})

		t.Run("searches from the head of another List", func(t *testing.T) {
			t.Parallel()
			first, second := newList(t), newList(t)
			var ins Inserter
			for i := range uint64(64) {
				assert.NoError(t, ins.Add(first, binary.BigEndian.AppendUint64(nil, i), 1, nil), "Add adds the entry")
			}
			assert.NoError(t, second.Add([]byte("a"), 1, nil), "Add adds a to the second List")
			assert.NoError(t, ins.Add(second, []byte("b"), 1, nil), "Add adds b to the second List")
			want := []entry{{key: []byte("a"), seq: 1}, {key: []byte("b"), seq: 1}}
			assert.Equal(t, scan(t.Context(), second.NewIter()), want,
				"a cursor of the second List yields its two entries", assert.EquateEmpty())
			assert.NoError(t, check(second), "the second List keeps the invariants of its levels")
		})

		t.Run("searches from the head at the levels that the List gained", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			// The second factor of the hash of an entry with the sequence 2 is 0,
			// so its node has every level.
			l.secret1 = 2
			var ins Inserter
			assert.NoError(t, ins.Add(l, []byte("a"), 1, nil), "Add adds a")
			assert.NoError(t, l.Add([]byte("m"), 2, nil), "Add adds m with every level")
			assert.Equal(t, l.levels.Load(), uint32(maxHeight), "the List has every level")
			assert.NoError(t, ins.Add(l, []byte("z"), 1, nil), "Add adds z after m")
			assert.NoError(t, check(l), "the List keeps the invariants of its levels")
		})

		t.Run("adds every entry while writers add through Inserters of their own", func(t *testing.T) {
			t.Parallel()
			l, err := New(Config{
				DataBytes: writers * hintedEntries * heightKeyLen,
				NodeBytes: writers * hintedEntries * benchNodeBytes,
				Seed:      1,
			})
			assert.NoError(t, err, "New returns a List")
			// Each writer adds every writers-th key in order, so the writers add
			// next to each other and each one adds after its last entry.
			outcomes := history.Concurrently(writers, time.Minute, func(client int) (any, error) {
				var ins Inserter
				for i := range hintedEntries {
					key := binary.BigEndian.AppendUint64(nil, uint64(i*writers+client))
					if err := ins.Add(l, key, 0, nil); err != nil {
						return i, err
					}
				}

				return hintedEntries, nil
			})
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every writer finishes")
				assert.NoError(t, o.Error, "every Add succeeds")
			}
			expect.Equal(t, l.Len(), writers*hintedEntries, "the List has every entry")
			expect.Length(t, scan(t.Context(), l.NewIter()), writers*hintedEntries, "a cursor yields every entry")
			expect.Length(t, scanBack(t.Context(), l.NewIter()), writers*hintedEntries,
				"a cursor yields every entry back")
			assert.NoError(t, check(l), "the List keeps the invariants of its levels")
		})
	})
}

// TestInserterAllocs checks the allocation contract of Add. MaxAllocs counts
// the allocations of the whole process, so the test does not run in
// parallel.
func TestInserterAllocs(t *testing.T) {
	value := make([]byte, benchValueLen)

	t.Run("Add", func(t *testing.T) {
		l, err := New(Config{DataBytes: 1 << 20, NodeBytes: 1 << 20, Seed: 1})
		assert.NoError(t, err, "New returns a List")
		var ins Inserter
		var k [benchKeyLen]byte
		i := uint64(0)
		expect.MaxAllocs(t, func() {
			binary.BigEndian.PutUint64(k[:8], i)
			err = ins.Add(l, k[:], 1, value)
			i++
		}, 0, "Add does not allocate")
		assert.NoError(t, err, "the test measures Adds that succeed")
	})
}

// BenchmarkInserter reports the cost of Add for each set of keys, and fails
// when Add allocates.
func BenchmarkInserter(b *testing.B) {
	value := make([]byte, benchValueLen)

	b.Run("Add", func(b *testing.B) {
		for _, set := range benchKeySets {
			b.Run("of "+set.name, func(b *testing.B) {
				l, err := New(Config{
					DataBytes: benchEntries * (benchKeyLen + benchValueLen),
					NodeBytes: benchEntries * benchNodeBytes,
					Seed:      1,
				})
				assert.NoError(b, err, "New returns a List")
				var ins Inserter
				var k [benchKeyLen]byte
				i := uint64(0)

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					if i == benchEntries {
						c.Excluding(l.Reset)
						i = 0
					}
					set.key(&k, i)
					err = ins.Add(l, k[:], 1, value)
					i++
				}

				assert.NoError(b, err, "the benchmark measures Adds that succeed")
			})
		}
	})
}
