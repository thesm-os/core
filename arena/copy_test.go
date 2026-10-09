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

	"go.thesmos.sh/core/arena"
)

// Shape of the entries that the RebaseSlices benchmarks rebase.
const (
	// rebaseItems is the number of entries.
	rebaseItems = 32

	// rebaseItemSize is the length of each entry.
	rebaseItemSize = 256
)

func TestCopy(t *testing.T) {
	t.Parallel()

	t.Run("CopyOut", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for an empty arena", func(t *testing.T) {
			t.Parallel()
			expect.Nil(t, arena.New().CopyOut(), "an arena without a backing buffer must copy out nil")
			expect.Nil(t, arena.NewWithCapacity(64).CopyOut(), "an empty arena with a backing buffer must copy out nil")
		})

		t.Run("returns a copy that later writes to the arena leave unchanged", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "CopyOut must return a copy of the bytes that the caller owns", func(c *prop.Case) {
				a := arena.NewWithCapacity(c.Draw(capacities, "initialCap"))
				data := c.Draw(payloads, "payloads")
				for _, d := range data {
					a.Append(d)
				}

				got := a.CopyOut()
				assert.Equal(c, got, slices.Concat(data...), "CopyOut must return the appended bytes")

				overwrite := c.Draw(payload, "overwrite")
				assert.Pure(c, func() []byte { return bytes.Clone(got) }, func() {
					a.Reset()
					a.Append(overwrite)
				}, "Reset and a later Append must leave the copy unchanged")
			})
		})
	})

	t.Run("CopyOutTo", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the bytes of the arena to dst", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "CopyOutTo must extend dst with the bytes of the arena", func(c *prop.Case) {
				dst := c.Draw(payload, "dst")
				a := arena.NewWithCapacity(c.Draw(capacities, "initialCap"))
				data := c.Draw(payloads, "payloads")
				for _, d := range data {
					a.Append(d)
				}

				assert.Equal(c, a.CopyOutTo(dst), slices.Concat(dst, slices.Concat(data...)),
					"CopyOutTo must return dst followed by the bytes of the arena", assert.EquateEmpty())
			})
		})
	})

	t.Run("RebaseSlices", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil without a byte to rebase", func(t *testing.T) {
			t.Parallel()
			prop.Nil(t, arena.RebaseSlices, "RebaseSlices must return nil when every entry is empty",
				prop.Using(prop.List(prop.OneOf(prop.Just([]byte(nil)), prop.Just([]byte{})), prop.MaxSize(4))),
				prop.Example([][]byte(nil)), prop.Example([][]byte{}), prop.Example([][]byte{nil, {}, nil}))
		})

		t.Run("copies the entries into the result and points each entry into it", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "RebaseSlices must move every entry into one allocation", func(c *prop.Case) {
				entries := c.Draw(payloads, "entries")
				want := make([][]byte, 0, len(entries))
				for _, e := range entries {
					want = append(want, bytes.Clone(e))
				}

				out := arena.RebaseSlices(entries)
				assert.Equal(c, out, slices.Concat(want...), "the result must be the entries in order")
				assert.Equal(c, entries, want, "every entry must keep its bytes", assert.EquateEmpty())
				for _, e := range entries {
					assert.Equal(c, cap(e), len(e), "the capacity of an entry must be its length")
				}

				clear(out)
				assert.Equal(c, slices.Concat(entries...), make([]byte, len(out)),
					"a write to the result must show in the entries", assert.EquateEmpty())
			})
		})

		t.Run("copies entries that overlap in their source", func(t *testing.T) {
			t.Parallel()
			src := []byte("abcdefgh")
			entries := [][]byte{src[0:5], src[2:7], src[0:5]}
			out := arena.RebaseSlices(entries)
			clear(src)
			assert.Equal(t, out, []byte("abcdecdefgabcde"), "each entry must be read before the next is written")
			assert.Equal(t, entries, [][]byte{[]byte("abcde"), []byte("cdefg"), []byte("abcde")},
				"no entry may still point into the source")
		})
	})

	t.Run("RebaseSlicesTo", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the entries to dst and points each entry into the result", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "RebaseSlicesTo must move every entry into dst", func(c *prop.Case) {
				dst := c.Draw(payload, "dst")
				entries := c.Draw(payloads, "entries")
				want := make([][]byte, 0, len(entries))
				for _, e := range entries {
					want = append(want, bytes.Clone(e))
				}

				out := arena.RebaseSlicesTo(dst, entries)
				assert.Equal(c, out, slices.Concat(append([][]byte{dst}, want...)...),
					"the result must be dst followed by the entries in order", assert.EquateEmpty())
				assert.Equal(c, entries, want, "every entry must keep its bytes", assert.EquateEmpty())
				for _, e := range entries {
					assert.Equal(c, cap(e), len(e), "the capacity of an entry must be its length")
				}

				clear(out[len(dst):])
				assert.Equal(c, slices.Concat(entries...), make([]byte, len(out)-len(dst)),
					"a write to the result must show in the entries", assert.EquateEmpty())
			})
		})
	})
}

// TestCopyAllocs checks the allocation contracts of CopyOut, CopyOutTo,
// RebaseSlices and RebaseSlicesTo. MaxAllocs counts the allocations of the
// whole process, so the test does not run in parallel.
func TestCopyAllocs(t *testing.T) {
	data := []byte("hello world")

	t.Run("CopyOut", func(t *testing.T) {
		a := arena.NewWithCapacity(1024)
		a.Append(data)

		var got []byte
		expect.MaxAllocs(t, func() { got = a.CopyOut() }, 1, "CopyOut must allocate only the copy")
		assert.Equal(t, got, data, "the test must measure a copy of the payload")
	})

	t.Run("CopyOutTo", func(t *testing.T) {
		a := arena.NewWithCapacity(1024)
		a.Append(data)
		dst := make([]byte, 0, 64)

		var got []byte
		expect.MaxAllocs(t, func() { got = a.CopyOutTo(dst) }, 0, "CopyOutTo into a dst with room must not allocate")
		assert.Equal(t, got, data, "the test must measure a copy of the payload")
	})

	t.Run("RebaseSlices", func(t *testing.T) {
		entries := [][]byte{[]byte("ab"), []byte("cd"), []byte("ef")}

		var got []byte
		expect.MaxAllocs(t, func() { got = arena.RebaseSlices(entries) }, 1,
			"RebaseSlices must allocate only the result")
		assert.Equal(t, got, []byte("abcdef"), "the test must measure a rebase of every entry")
	})

	t.Run("RebaseSlicesTo", func(t *testing.T) {
		entries := [][]byte{[]byte("ab"), []byte("cd"), []byte("ef")}
		dst := make([]byte, 0, 64)

		var got []byte
		expect.MaxAllocs(t, func() { got = arena.RebaseSlicesTo(dst, entries) }, 0,
			"RebaseSlicesTo into a dst with room must not allocate")
		assert.Equal(t, got, []byte("abcdef"), "the test must measure a rebase of every entry")
	})
}

// BenchmarkCopy reports the cost of copying the bytes of an arena out to
// memory of the caller's, and fails when a call allocates more than its
// allocation contract allows.
func BenchmarkCopy(b *testing.B) {
	b.Run("CopyOut", func(b *testing.B) {
		for _, size := range sizes {
			b.Run(size.name, func(b *testing.B) {
				a := arena.NewWithCapacity(size.n)
				a.Append(make([]byte, size.n))
				b.SetBytes(int64(size.n))

				var got []byte

				c := bench.Start(b).MaxAllocs(1)
				defer c.End()

				for c.Loop() {
					got = a.CopyOut()
				}

				assert.Length(b, got, size.n, "the benchmark must measure a copy of every byte")
			})
		}
	})

	b.Run("CopyOutTo", func(b *testing.B) {
		for _, size := range sizes {
			b.Run(size.name, func(b *testing.B) {
				a := arena.NewWithCapacity(size.n)
				a.Append(make([]byte, size.n))
				dst := make([]byte, 0, size.n)
				b.SetBytes(int64(size.n))

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					dst = a.CopyOutTo(dst[:0])
				}

				assert.Length(b, dst, size.n, "the benchmark must measure a copy of every byte")
			})
		}
	})

	b.Run("RebaseSlices", func(b *testing.B) {
		all := make([]byte, rebaseItems*rebaseItemSize)
		b.SetBytes(int64(len(all)))

		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			var entries [rebaseItems][]byte
			for i := range entries {
				entries[i] = all[i*rebaseItemSize : (i+1)*rebaseItemSize]
			}
			got = arena.RebaseSlices(entries[:])
		}

		assert.Length(b, got, len(all), "the benchmark must measure a rebase of every entry")
	})

	b.Run("RebaseSlicesTo", func(b *testing.B) {
		all := make([]byte, rebaseItems*rebaseItemSize)
		dst := make([]byte, 0, len(all))
		b.SetBytes(int64(len(all)))

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			var entries [rebaseItems][]byte
			for i := range entries {
				entries[i] = all[i*rebaseItemSize : (i+1)*rebaseItemSize]
			}
			dst = arena.RebaseSlicesTo(dst[:0], entries[:])
		}

		assert.Length(b, dst, len(all), "the benchmark must measure a rebase of every entry")
	})
}
