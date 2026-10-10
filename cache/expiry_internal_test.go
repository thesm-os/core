// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"slices"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
)

// TestExpiry is in package cache because expiry is unexported. It checks
// the order in which the heap returns its entries, which Expire follows.
func TestExpiry(t *testing.T) {
	t.Parallel()

	t.Run("push", func(t *testing.T) {
		t.Parallel()

		t.Run("puts the entry that expires first at the root", func(t *testing.T) {
			t.Parallel()
			var x expiry[int, int]
			es := expiring(3, 1, 2)
			for _, e := range es {
				x.push(e)
			}
			assert.Equal(t, x.first(), es[1], "the root must be the entry that expires first", assert.ByIdentity())
		})

		t.Run("records the position of each entry in its slot", func(t *testing.T) {
			t.Parallel()
			var x expiry[int, int]
			es := expiring(5, 4, 3, 2, 1)
			for _, e := range es {
				x.push(e)
			}
			for i, e := range x.heap {
				expect.Equal(t, e.slot, i, "the slot of each entry must be its position in the heap")
			}
		})
	})

	t.Run("remove", func(t *testing.T) {
		t.Parallel()

		t.Run("takes out the last entry of the heap", func(t *testing.T) {
			t.Parallel()
			var x expiry[int, int]
			es := expiring(1, 2)
			x.push(es[0])
			x.push(es[1])
			x.remove(es[1])
			expect.Length(t, x.heap, 1, "the heap must lose the entry")
			expect.Equal(t, x.first(), es[0], "the other entry must remain", expect.ByIdentity())
		})

		t.Run("moves the last entry up when it expires before the parent of the removed entry", func(t *testing.T) {
			t.Parallel()
			var x expiry[int, int]
			es := expiring(1, 10, 2, 11, 12, 3, 4)
			for _, e := range es {
				x.push(e)
			}
			x.remove(es[3])
			assert.Equal(t, x.heap[1], es[6], "the last entry must move up past the parent of the removed entry",
				assert.ByIdentity())
		})

		t.Run("drops its reference to the removed entry", func(t *testing.T) {
			t.Parallel()
			var x expiry[int, int]
			es := expiring(1, 2)
			x.push(es[0])
			x.push(es[1])
			x.remove(es[0])
			assert.Nil(t, x.heap[:2][1], "the heap must not keep the removed entry reachable")
		})
	})

	t.Run("first", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for an empty heap", func(t *testing.T) {
			t.Parallel()
			var x expiry[int, int]
			assert.Nil(t, x.first(), "an empty heap must have no first entry")
		})

		t.Run("returns the entries in the order of their expiry times after pushes and removals", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "the heap must return the remaining entries in the order of their expiry times",
				func(c *prop.Case) {
					offsets := c.Draw(prop.List(prop.Integer(0, 40), prop.MaxSize(40)), "offsets")
					removals := c.Draw(prop.List(prop.Integer(0, 39), prop.MaxSize(40)), "removals")

					var x expiry[int, int]
					es := expiring(offsets...)
					for _, e := range es {
						x.push(e)
					}

					gone := make([]bool, len(es))
					for _, r := range removals {
						if r < len(es) && !gone[r] {
							x.remove(es[r])
							gone[r] = true
						}
					}

					want := make([]time.Time, 0, len(es))
					for i, e := range es {
						if !gone[i] {
							want = append(want, e.expires)
						}
					}
					slices.SortFunc(want, time.Time.Compare)

					got := make([]time.Time, 0, len(es))
					for e := x.first(); e != nil; e = x.first() {
						got = append(got, e.expires)
						x.remove(e)
					}

					assert.Equal(c, got, want,
						"the heap must return the remaining entries in the order of their expiry times")
				})
		})
	})
}

// expiring returns one entry per offset, which expires that many seconds
// after origin and has the offset's index as its key.
func expiring(offsets ...int) []*entry[int, int] {
	es := make([]*entry[int, int], len(offsets))
	for i, o := range offsets {
		es[i] = &entry[int, int]{key: i, expires: origin.Add(time.Duration(o) * time.Second)}
	}

	return es
}
