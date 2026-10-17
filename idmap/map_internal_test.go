// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idmap

import (
	"fmt"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/ksuid"
	"go.thesmos.sh/core/id/ulid"
	"go.thesmos.sh/core/id/uuidv4"
	"go.thesmos.sh/core/id/uuidv7"
)

// TestMapTable checks the table of a Map: the slots that New allocates, the
// hash of the keys of an identifier type, the limit at which Set doubles the
// slots, and the runs that Delete leaves.
func TestMapTable(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give int
			want int
		}{
			{name: "allocates 8 slots for a capacity below 1", give: 0, want: 8},
			{name: "allocates 8 slots for a capacity of 5", give: 5, want: 8},
			{name: "allocates 16 slots for a capacity of 6", give: 6, want: 16},
			{name: "allocates 16 slots for a capacity of 10", give: 10, want: 16},
			{name: "allocates 32 slots for a capacity of 11", give: 11, want: 32},
			{name: "allocates 2^21 slots for a capacity of 1,000,000", give: 1_000_000, want: 1 << 21},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m := New[id.ID128, int](tt.give)
				assert.Length(t, m.tags, tt.want, "New must allocate the least table with room for the capacity")
			})
		}

		t.Run("draws a new random seed for each map", func(t *testing.T) {
			t.Parallel()
			a, b := New[id.ID128, int](1), New[id.ID128, int](1)
			expect.NotEqual(t, a.seed.s0, b.seed.s0, "two maps must have different first secret words")
			expect.NotEqual(t, a.seed.s1, b.seed.s1, "two maps must have different second secret words")
			expect.NotEqual(t, a.seed.s0, a.seed.s1, "the two secret words of one seed must differ")
		})
	})

	t.Run("NewTyped128", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map that hashes a key as hash128 hashes its identifier", func(t *testing.T) {
			t.Parallel()
			m := NewTyped128[struct{ id.ID128 }, int](1)
			k := counting[id.ID128]()
			got, _ := m.hash(m.seed, struct{ id.ID128 }{k})
			want, _ := hash128(m.seed, &k)
			assert.Equal(t, got, want, "the hash of a key must be the hash of its identifier")
		})
	})

	t.Run("NewTyped160", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map that hashes a key as hash160 hashes its identifier", func(t *testing.T) {
			t.Parallel()
			m := NewTyped160[struct{ id.ID160 }, int](1)
			k := counting[id.ID160]()
			got, _ := m.hash(m.seed, struct{ id.ID160 }{k})
			want, _ := hash160(m.seed, &k)
			assert.Equal(t, got, want, "the hash of a key must be the hash of its identifier")
		})
	})

	t.Run("NewTyped256", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map that hashes a key as hash256 hashes its identifier", func(t *testing.T) {
			t.Parallel()
			m := NewTyped256[struct{ id.ID256 }, int](1)
			k := counting[id.ID256]()
			got, _ := m.hash(m.seed, struct{ id.ID256 }{k})
			want, _ := hash256(m.seed, &k)
			assert.Equal(t, got, want, "the hash of a key must be the hash of its identifier")
		})
	})

	t.Run("New128", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map that hashes a key as hash128 hashes the identifier of its bytes", func(t *testing.T) {
			t.Parallel()
			m := New128[ulid.ULID, int](1)
			k := counting[id.ID128]()
			got, gotWords := m.hash(m.seed, ulid.ULID(k))
			want, wantWords := hash128(m.seed, &k)
			expect.Equal(t, got, want, "the hash of a key must be the hash of its identifier")
			expect.Equal(t, gotWords, wantWords, "the words of a key must be the words of its identifier")
		})
	})

	t.Run("New160", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map that hashes a key as hash160 hashes the identifier of its bytes", func(t *testing.T) {
			t.Parallel()
			m := New160[ksuid.KSUID, int](1)
			k := counting[id.ID160]()
			got, gotWords := m.hash(m.seed, ksuid.KSUID(k))
			want, wantWords := hash160(m.seed, &k)
			expect.Equal(t, got, want, "the hash of a key must be the hash of its identifier")
			expect.Equal(t, gotWords, wantWords, "the words of a key must be the words of its identifier")
		})
	})

	t.Run("New256", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map that hashes a key as hash256 hashes the identifier of its bytes", func(t *testing.T) {
			t.Parallel()
			m := New256[[id.Size256]byte, int](1)
			k := counting[id.ID256]()
			got, gotWords := m.hash(m.seed, [id.Size256]byte(k))
			want, wantWords := hash256(m.seed, &k)
			expect.Equal(t, got, want, "the hash of a key must be the hash of its identifier")
			expect.Equal(t, gotWords, wantWords, "the words of a key must be the words of its identifier")
		})
	})

	t.Run("NewTypedULID", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map that hashes a key as hash128 hashes the identifier of its ULID", func(t *testing.T) {
			t.Parallel()
			m := NewTypedULID[struct{ ulid.ULID }, int](1)
			k := counting[id.ID128]()
			got, gotWords := m.hash(m.seed, struct{ ulid.ULID }{ulid.ULID(k)})
			want, wantWords := hash128(m.seed, &k)
			expect.Equal(t, got, want, "the hash of a key must be the hash of its identifier")
			expect.Equal(t, gotWords, wantWords, "the words of a key must be the words of its identifier")
		})
	})

	t.Run("NewTypedKSUID", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map that hashes a key as hash160 hashes the identifier of its KSUID", func(t *testing.T) {
			t.Parallel()
			m := NewTypedKSUID[struct{ ksuid.KSUID }, int](1)
			k := counting[id.ID160]()
			got, gotWords := m.hash(m.seed, struct{ ksuid.KSUID }{ksuid.KSUID(k)})
			want, wantWords := hash160(m.seed, &k)
			expect.Equal(t, got, want, "the hash of a key must be the hash of its identifier")
			expect.Equal(t, gotWords, wantWords, "the words of a key must be the words of its identifier")
		})
	})

	t.Run("NewTypedUUIDv4", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map that hashes a key as hash128 hashes the identifier of its UUID", func(t *testing.T) {
			t.Parallel()
			m := NewTypedUUIDv4[struct{ uuidv4.UUID }, int](1)
			k := counting[id.ID128]()
			got, gotWords := m.hash(m.seed, struct{ uuidv4.UUID }{uuidv4.UUID(k)})
			want, wantWords := hash128(m.seed, &k)
			expect.Equal(t, got, want, "the hash of a key must be the hash of its identifier")
			expect.Equal(t, gotWords, wantWords, "the words of a key must be the words of its identifier")
		})
	})

	t.Run("NewTypedUUIDv7", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map that hashes a key as hash128 hashes the identifier of its UUID", func(t *testing.T) {
			t.Parallel()
			m := NewTypedUUIDv7[struct{ uuidv7.UUID }, int](1)
			k := counting[id.ID128]()
			got, gotWords := m.hash(m.seed, struct{ uuidv7.UUID }{uuidv7.UUID(k)})
			want, wantWords := hash128(m.seed, &k)
			expect.Equal(t, got, want, "the hash of a key must be the hash of its identifier")
			expect.Equal(t, gotWords, wantWords, "the words of a key must be the words of its identifier")
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		t.Run("doubles the slots for the first new key past five eighths of the slots", func(t *testing.T) {
			t.Parallel()
			m := New[id.ID128, int](5)
			for i := range 5 {
				m.Set(keyAt[id.ID128](i), i)
			}
			assert.Length(t, m.tags, 8, "a table of 8 slots must take 5 keys")
			m.Set(keyAt[id.ID128](5), 5)
			assert.Length(t, m.tags, 16, "the sixth key must double the slots")
		})
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps every key reachable from its home slot without an empty slot on the way", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "every key must be reachable from its home slot after any writes", func(pc *prop.Case) {
				ops := pc.Draw(prop.List(prop.Integer(-24, 23), prop.MaxSize(200)), "ops")
				m := New[id.ID128, int](6)
				for _, op := range ops {
					if op < 0 {
						m.Delete(keyAt[id.ID128](-op - 1))
					} else {
						m.Set(keyAt[id.ID128](op), op)
					}
				}

				full := make([]uint64, 0, len(m.tags))
				for j, tag := range m.tags {
					if tag != 0 {
						full = append(full, uint64(j))
					}
				}
				assert.Total(pc, func(j uint64) error {
					h, _ := m.hash(m.seed, m.keys[j])
					if m.tags[j] != uint8(h>>tagShift)|fullTag {
						return fmt.Errorf("slot %d: tag %#x for a key of hash %#x", j, m.tags[j], h)
					}
					for i := h & m.mask; i != j; i = (i + 1) & m.mask {
						if m.tags[i] == 0 {
							return fmt.Errorf("slot %d: the walk from home slot %d passes the empty slot %d",
								j, h&m.mask, i)
						}
					}

					return nil
				}, full, "every full slot must have the tag of its key, and a walk without an empty slot from its home slot")
			})
		})
	})
}

// keyAt returns the key number i of an internal test: i in its first 8
// bytes, little-endian.
func keyAt[K Key](i int) K {
	var k K
	for b := range 8 {
		k[b] = byte(i >> (8 * b))
	}

	return k
}
