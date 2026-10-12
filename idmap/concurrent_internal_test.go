// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idmap

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/id"
)

// TestConcurrentBuckets checks the buckets of a Concurrent: the buckets that
// NewConcurrent allocates, the waits of a reader and of a writer, and the
// overflow counts that Store and Delete keep.
func TestConcurrentBuckets(t *testing.T) {
	t.Parallel()

	t.Run("NewConcurrent", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give int
			want int
		}{
			{name: "allocates 1 bucket for a capacity below 1", give: 0, want: 1},
			{name: "allocates 1 bucket for a capacity of 2", give: 2, want: 1},
			{name: "allocates 2 buckets for a capacity of 3", give: 3, want: 2},
			{name: "allocates 2 buckets for a capacity of 4", give: 4, want: 2},
			{name: "allocates 4 buckets for a capacity of 5", give: 5, want: 4},
			{name: "allocates 2^19 buckets for a capacity of 1,000,000", give: 1_000_000, want: 1 << 19},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := NewConcurrent[id.ID128, int64](tt.give)
				assert.Length(t, c.buckets, tt.want,
					"NewConcurrent must allocate the least buckets for twice the capacity")
			})
		}

		t.Run("allocates no key words past 16 bytes for ID128 keys", func(t *testing.T) {
			t.Parallel()
			c := NewConcurrent[id.ID128, int64](8)
			assert.Empty(t, c.rest, "an ID128 key must have no words past 16 bytes")
		})

		t.Run("allocates one key word past 16 bytes per slot for ID160 keys", func(t *testing.T) {
			t.Parallel()
			c := NewConcurrent[id.ID160, int64](8)
			assert.Length(t, c.rest, 4*bucketSlots, "an ID160 key must have one word past 16 bytes")
		})

		t.Run("allocates two key words past 16 bytes per slot for ID256 keys", func(t *testing.T) {
			t.Parallel()
			c := NewConcurrent[id.ID256, int64](8)
			assert.Length(t, c.rest, 2*4*bucketSlots, "an ID256 key must have two words past 16 bytes")
		})
	})

	t.Run("find", func(t *testing.T) {
		t.Parallel()

		t.Run("yields until the writer of the bucket makes its sequence number even", func(t *testing.T) {
			t.Parallel()
			c := NewConcurrent[id.ID128, int64](2)
			k := keyAt[id.ID128](1)
			assert.NoError(t, c.Store(k, 7), "Store must store the key")
			b := &c.buckets[0]
			b.seq.Add(1)
			yields := 0
			c.yield = func() {
				yields++
				b.seq.Add(1)
			}
			v, ok := c.Load(k)
			expect.True(t, ok, "Load must find the key once the writer finished")
			expect.Equal(t, v, int64(7), "Load must return the value of the key")
			expect.Equal(t, yields, 1, "Load must yield once while the sequence number is odd")
		})
	})

	t.Run("sameRest", func(t *testing.T) {
		t.Parallel()

		t.Run("tells apart two ID160 keys of one tag that differ in their last 4 bytes", func(t *testing.T) {
			t.Parallel()
			c := NewConcurrent[id.ID160, int64](2)
			a, b := sameTag(c, 19)
			assert.NoError(t, c.Store(a, 1), "Store must store the first key")
			_, ok := c.Load(b)
			assert.False(t, ok, "Load must not find a key that differs from a stored key in its last 4 bytes")
		})

		t.Run("tells apart two ID256 keys of one tag that differ in their third word", func(t *testing.T) {
			t.Parallel()
			c := NewConcurrent[id.ID256, int64](2)
			a, b := sameTag(c, 20)
			assert.NoError(t, c.Store(a, 1), "Store must store the first key")
			_, ok := c.Load(b)
			assert.False(t, ok, "Load must not find a key that differs from a stored key in its third word")
		})

		t.Run("tells apart two ID256 keys of one tag that differ in their fourth word", func(t *testing.T) {
			t.Parallel()
			c := NewConcurrent[id.ID256, int64](2)
			a, b := sameTag(c, 28)
			assert.NoError(t, c.Store(a, 1), "Store must store the first key")
			_, ok := c.Load(b)
			assert.False(t, ok, "Load must not find a key that differs from a stored key in its fourth word")
		})
	})

	t.Run("lockHome", func(t *testing.T) {
		t.Parallel()

		t.Run("yields until the writer of the home releases its lock", func(t *testing.T) {
			t.Parallel()
			c := NewConcurrent[id.ID128, int64](2)
			hb := &c.buckets[0]
			hb.home.Store(1)
			yields := 0
			c.yield = func() {
				yields++
				hb.home.Store(0)
			}
			err := c.Store(keyAt[id.ID128](1), 7)
			expect.NoError(t, err, "Store must store the key once the other writer released the home")
			expect.Equal(t, yields, 1, "Store must yield once while another writer has the home")
			expect.Equal(t, hb.home.Load(), uint32(0), "Store must release the home lock")
		})

		t.Run("makes Delete yield until the writer of the home releases its lock", func(t *testing.T) {
			t.Parallel()
			c := NewConcurrent[id.ID128, int64](2)
			assert.NoError(t, c.Store(keyAt[id.ID128](1), 7), "Store must store the key")
			hb := &c.buckets[0]
			hb.home.Store(1)
			yields := 0
			c.yield = func() {
				yields++
				hb.home.Store(0)
			}
			ok := c.Delete(keyAt[id.ID128](1))
			expect.True(t, ok, "Delete must remove the key once the other writer released the home")
			expect.Equal(t, yields, 1, "Delete must yield once while another writer has the home")
			expect.Equal(t, hb.home.Load(), uint32(0), "Delete must release the home lock")
		})
	})

	t.Run("lockSlots", func(t *testing.T) {
		t.Parallel()

		t.Run("yields until another writer of the bucket makes its sequence number even", func(t *testing.T) {
			t.Parallel()
			c := NewConcurrent[id.ID128, int64](8)
			keys := keysWithHome(c, 0, bucketSlots+1)
			for i, k := range keys[:bucketSlots] {
				assert.NoError(t, c.Store(k, int64(i)), "Store must store a key of the full bucket")
			}
			next := &c.buckets[1]
			next.seq.Add(1)
			yields := 0
			c.yield = func() {
				yields++
				next.seq.Add(1)
			}
			err := c.Store(keys[bucketSlots], 9)
			assert.Equal(t, yields, 1, "Store must yield once while another writer changes the next bucket")
			v, ok := c.Load(keys[bucketSlots])
			expect.NoError(t, err, "Store must store the key in the next bucket")
			expect.True(t, ok, "Load must find the key in the next bucket")
			expect.Equal(t, v, int64(9), "Load must return the value of the key")
		})

		t.Run("advances the sequence number of the bucket by 2 for each write", func(t *testing.T) {
			t.Parallel()
			c := NewConcurrent[id.ID128, int64](2)
			b := &c.buckets[0]
			before := b.seq.Load()
			assert.NoError(t, c.Store(keyAt[id.ID128](1), 7), "Store must store the key")
			assert.Equal(t, b.seq.Load(), before+2, "a Store of a new key must advance the sequence number by 2")
			assert.NoError(t, c.Store(keyAt[id.ID128](1), 8), "Store must replace the value of the key")
			assert.Equal(t, b.seq.Load(), before+4, "a Store of a present key must advance the sequence number by 2")
			assert.True(t, c.Delete(keyAt[id.ID128](1)), "Delete must remove the key")
			assert.Equal(t, b.seq.Load(), before+6, "a Delete must advance the sequence number by 2")
		})
	})

	t.Run("Load", func(t *testing.T) {
		t.Parallel()

		t.Run("stops at a bucket whose overflow count is zero", func(t *testing.T) {
			t.Parallel()
			c := NewConcurrent[id.ID128, int64](4)
			missing := keysWithHome(c, 0, 1)[0]
			next := &c.buckets[1]
			next.seq.Add(1)
			reads := 0
			c.yield = func() {
				reads++
				next.seq.Add(1)
			}
			_, ok := c.Load(missing)
			expect.False(t, ok, "Load must not find a missing key")
			expect.Equal(t, reads, 0, "Load must not read the bucket after a bucket whose overflow count is zero")
		})

		t.Run("finds a key whose walk passed a full bucket after a key of that bucket left", func(t *testing.T) {
			t.Parallel()
			c := NewConcurrent[id.ID256, int64](8)
			keys := keysWithHome(c, 2, bucketSlots+1)
			for i, k := range keys {
				assert.NoError(t, c.Store(k, int64(i)), "Store must store every key")
			}
			assert.True(t, c.Delete(keys[0]), "Delete must remove a key of the full bucket")
			v, ok := c.Load(keys[bucketSlots])
			expect.True(t, ok, "Load must walk past the home bucket of the key")
			expect.Equal(t, v, int64(bucketSlots), "Load must return the value of the key")
		})
	})

	t.Run("Store", func(t *testing.T) {
		t.Parallel()

		t.Run("counts each key in the overflow count of every bucket that its walk passed", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "each overflow count must count the keys whose walk passed its bucket", func(pc *prop.Case) {
				c, present := churn(pc)
				assert.Equal(pc, overflows(c), passes(c, present),
					"the overflow counts must count the walks past each bucket")
			})
		})
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("stops at a bucket whose overflow count is zero", func(t *testing.T) {
			t.Parallel()
			c := NewConcurrent[id.ID128, int64](4)
			missing := keysWithHome(c, 0, 1)[0]
			next := &c.buckets[1]
			next.seq.Add(1)
			reads := 0
			c.yield = func() {
				reads++
				next.seq.Add(1)
			}
			expect.False(t, c.Delete(missing), "Delete must report a missing key as not present")
			expect.Equal(t, reads, 0, "Delete must not read the bucket after a bucket whose overflow count is zero")
		})

		t.Run("leaves every overflow count at zero once the map is empty again", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "the overflow counts of an empty map must be zero", func(pc *prop.Case) {
				c, present := churn(pc)
				for i := range present {
					assert.True(pc, c.Delete(keyAt[id.ID160](i)), "Delete must remove every stored key")
				}
				assert.Equal(pc, overflows(c), make([]uint32, len(c.buckets)), "every overflow count must be zero")
			})
		})
	})
}

// keysWithHome returns n keys whose home bucket in c is home.
func keysWithHome[K Key, V Word](c *Concurrent[K, V], home uint64, n int) []K {
	keys := make([]K, 0, n)
	for i := 0; len(keys) < n; i++ {
		k := keyAt[K](i)
		if h, _ := c.hash(c.seed, k); h&c.mask == home {
			keys = append(keys, k)
		}
	}

	return keys
}

// sameTag returns two keys that differ only in the byte at, and whose
// hashes in c have one tag. A tag has 128 values, so the search ends within
// 129 keys.
func sameTag[K Key, V Word](c *Concurrent[K, V], at int) (K, K) {
	seen := map[uint32]K{}
	for x := 0; ; x++ {
		var k K
		k[at] = byte(x)

		h, _ := c.hash(c.seed, k)
		tag := uint32(h>>tagShift) | fullTag
		if other, ok := seen[tag]; ok {
			return other, k
		}

		seen[tag] = k
	}
}

// churn returns a map of 4 buckets after random Stores and Deletes of 24
// keys, and the numbers of the keys that it contains.
func churn(pc *prop.Case) (*Concurrent[id.ID160, int64], map[int]bool) {
	ops := pc.Draw(prop.List(prop.Integer(-24, 23), prop.MaxSize(200)), "ops")
	c := NewConcurrent[id.ID160, int64](8)
	present := map[int]bool{}
	for _, op := range ops {
		if op < 0 {
			c.Delete(keyAt[id.ID160](-op - 1))
			delete(present, -op-1)
		} else if c.Store(keyAt[id.ID160](op), int64(op)) == nil {
			present[op] = true
		}
	}

	return c, present
}

// overflows returns the overflow count of each bucket of c.
func overflows[K Key, V Word](c *Concurrent[K, V]) []uint32 {
	counts := make([]uint32, len(c.buckets))
	for i := range c.buckets {
		counts[i] = c.buckets[i].overflow.Load()
	}

	return counts
}

// passes returns, for each bucket of c, the number of the present keys whose
// walk from their home bucket passed it: the buckets from the home bucket of
// a key up to the bucket before the one that contains the key.
func passes(c *Concurrent[id.ID160, int64], present map[int]bool) []uint32 {
	counts := make([]uint32, len(c.buckets))
	for i := range present {
		h, w := c.hash(c.seed, keyAt[id.ID160](i))
		tag := uint32(h>>tagShift) | fullTag
		home := h & c.mask
		at := home
		for s, _ := c.find(at, tag, w); s == bucketSlots; s, _ = c.find(at, tag, w) {
			at = (at + 1) & c.mask
		}
		for j := home; j != at; j = (j + 1) & c.mask {
			counts[j]++
		}
	}

	return counts
}
