// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"slices"
	"strconv"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/crypto"
	coresha256 "go.thesmos.sh/core/crypto/sha256"
	coresha512 "go.thesmos.sh/core/crypto/sha512"
	"go.thesmos.sh/core/tlog"
)

// The fixture values of the cases of Builder.
const (
	// batchedSize is the number of leaves of the properties that integrate
	// a tree in batches, past the first tile of level 1.
	batchedSize = 1500

	// maxBatch is the size of the largest batch of the properties.
	maxBatch = 600

	// publishedSize is the size of the tree that a Builder resumes from in
	// the cases of NewBuilder, and resumedBatch the size of the batch after
	// it.
	publishedSize = 1000
	resumedBatch  = 500

	// modelLeaves is the number of leaves that the machine of
	// TestBuilderModel integrates at most, past the fifth full tile, and
	// modelBatch the largest batch of one step.
	modelLeaves = 1500
	modelBatch  = 300
)

// The labels under which the machine of TestBuilderModel counts a case
// that commits an Update and a case that refuses a stale one.
const (
	committedUpdate = "a committed Update"
	staleUpdate     = "a stale Update"
)

// update is an Update and the Builder that computed it, which the
// allocation test of Commit builds outside its measurement.
type update struct {
	tlog.Update

	b *tlog.Builder
}

// pending is an Update that the machine of TestBuilderModel computed, with
// the Builder that computed it and the size of the tree that it computed
// it from.
type pending struct {
	u    *tlog.Update
	b    *tlog.Builder
	base int
}

func TestBuilder(t *testing.T) {
	t.Parallel()

	v := loadVectors(t)
	h := coresha256.New()
	all := leaves(largeSize)

	t.Run("NewBuilder", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Builder of the root of a published size", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: map[tlog.Tile][]byte{}}
			b, err := tlog.NewBuilder(t.Context(), h, 0, m)
			assert.NoError(t, err, "NewBuilder must succeed")
			grow(t, b, m, all, 700, 300)

			resumed, err := tlog.NewBuilder(t.Context(), h, publishedSize, m)
			assert.NoError(t, err, "NewBuilder must succeed")
			assert.Equal(t, resumed.Root(), b.Root(), "the resumed root must be the published one")
		})

		t.Run("returns a Builder that writes the tiles of the Builder that published the size", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: map[tlog.Tile][]byte{}}
			b, err := tlog.NewBuilder(t.Context(), h, 0, m)
			assert.NoError(t, err, "NewBuilder must succeed")
			grow(t, b, m, all, 700, 300)

			resumed, err := tlog.NewBuilder(t.Context(), h, publishedSize, m)
			assert.NoError(t, err, "NewBuilder must succeed")

			var u, ru tlog.Update
			assert.NoError(t, b.Integrate(all[publishedSize:publishedSize+resumedBatch], &u), "Integrate must succeed")
			assert.NoError(t, resumed.Integrate(all[publishedSize:publishedSize+resumedBatch], &ru),
				"Integrate must succeed")
			expect.Equal(t, tilesOf(&ru), tilesOf(&u), "the resumed Builder must write the same tiles")
			expect.Equal(t, ru.Root(), u.Root(), "the resumed Builder must compute the same root")
		})

		t.Run("returns a Builder that rewrites the same tiles after a crash before Commit", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: map[tlog.Tile][]byte{}}
			b, err := tlog.NewBuilder(t.Context(), h, 0, m)
			assert.NoError(t, err, "NewBuilder must succeed")
			grow(t, b, m, all, publishedSize)

			var lost tlog.Update
			assert.NoError(t, b.Integrate(all[publishedSize:1300], &lost), "Integrate must succeed")
			m.store(&lost)

			resumed, err := tlog.NewBuilder(t.Context(), h, publishedSize, m)
			assert.NoError(t, err, "NewBuilder must succeed")

			var again tlog.Update
			assert.NoError(t, resumed.Integrate(all[publishedSize:1300], &again), "Integrate must succeed")
			assert.Equal(t, tilesOf(&again), tilesOf(&lost), "the retry must write the same bytes to the same paths")
		})

		t.Run("returns ErrTileSize for tile data of the wrong length", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: map[tlog.Tile][]byte{{Width: 3}: make([]byte, 2*h.Hash(nil).Size())}}
			_, err := tlog.NewBuilder(t.Context(), h, 3, m)
			assert.ErrorIs(t, err, tlog.ErrTileSize, "a short tile must be ErrTileSize")
		})

		t.Run("returns the error of the reader", func(t *testing.T) {
			t.Parallel()
			_, err := tlog.NewBuilder(t.Context(), h, 3, &memTiles{data: map[tlog.Tile][]byte{}})
			assert.ErrorIs(t, err, errAbsent, "the error of the reader must be returned")
		})
	})

	t.Run("Size", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the size of the tree that the last Commit set", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: map[tlog.Tile][]byte{}}
			b, err := tlog.NewBuilder(t.Context(), h, 0, m)
			assert.NoError(t, err, "NewBuilder must succeed")
			grow(t, b, m, all, 10, 5)
			assert.Equal(t, b.Size(), uint64(15), "Size must count every committed leaf")
		})
	})

	t.Run("Root", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the root of the tree that the last Commit set", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: map[tlog.Tile][]byte{}}
			b, err := tlog.NewBuilder(t.Context(), h, 0, m)
			assert.NoError(t, err, "NewBuilder must succeed")
			grow(t, b, m, all, 10, 5)
			assert.Equal(t, b.Root(), tlog.Root(h, all[:15]), "Root must return the root of every committed leaf")
		})
	})

	t.Run("Integrate", func(t *testing.T) {
		t.Parallel()

		t.Run("writes the recorded tiles of every recorded tree", func(t *testing.T) {
			t.Parallel()
			for size, want := range v.tiles {
				b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
				assert.NoError(t, err, "NewBuilder must succeed")

				var u tlog.Update
				assert.NoError(t, b.Integrate(all[:size], &u), "Integrate must succeed")
				expect.Equal(t, tileDigest(t, &u), want.digest, "the tiles of "+strconv.FormatUint(size, 10)+" leaves")
			}
		})

		t.Run("hashes each node of a full tile once", func(t *testing.T) {
			t.Parallel()
			var sums int
			b, err := tlog.NewBuilder(t.Context(), sumCounter{sums: &sums}, 0, nil)
			assert.NoError(t, err, "NewBuilder must succeed")

			var u tlog.Update
			assert.NoError(t, b.Integrate(all[:fullWidth], &u), "Integrate must succeed")
			assert.Equal(t, sums, fullWidth-1, "a tile of 256 hashes has 255 nodes")
		})

		t.Run("writes the tiles that Tiles lists", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Integrate must write the tiles that Tiles lists", func(c *prop.Case) {
				size := c.Draw(prop.Integer(0, batchedSize), "size")
				batch := c.Draw(prop.Integer(0, maxBatch), "batch")

				b, err := tlog.NewBuilder(c.Context(), h, 0, nil)
				assert.NoError(c, err, "NewBuilder must succeed")

				var u tlog.Update
				assert.NoError(c, b.Integrate(all[:size], &u), "Integrate must succeed")
				assert.NoError(c, b.Commit(&u), "Commit must succeed")
				assert.NoError(c, b.Integrate(all[size:size+batch], &u), "Integrate must succeed")

				var got []tlog.Tile
				for tile := range u.Tiles() {
					got = append(got, tile)
				}
				assert.Equal(c, got, slices.Collect(tlog.Tiles(uint64(size), uint64(size+batch))),
					"Integrate must write the tiles that the batch adds")
			})
		})

		t.Run("writes the tiles of one batch in any batching", func(t *testing.T) {
			t.Parallel()
			once, err := tlog.NewBuilder(t.Context(), h, 0, nil)
			assert.NoError(t, err, "NewBuilder must succeed")

			var whole tlog.Update
			assert.NoError(t, once.Integrate(all[:batchedSize], &whole), "Integrate must succeed")
			want := tilesOf(&whole)

			prop.ForAll(t, "Integrate must write the tiles of one batch in any batching", func(c *prop.Case) {
				m := &memTiles{data: map[tlog.Tile][]byte{}}
				b, err := tlog.NewBuilder(c.Context(), h, 0, m)
				assert.NoError(c, err, "NewBuilder must succeed")
				grow(c, b, m, all, batchesOf(c, batchedSize)...)
				assert.Equal(c, storedOf(m, want), want, "the tiles must not depend on the batching")
			})
		})

		t.Run("writes the tiles of one batch for a batch that completes a tile of level 1", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: map[tlog.Tile][]byte{}}
			b, err := tlog.NewBuilder(t.Context(), h, 0, m)
			assert.NoError(t, err, "NewBuilder must succeed")
			grow(t, b, m, all, 65535, 1, 4464)

			once, err := tlog.NewBuilder(t.Context(), h, 0, nil)
			assert.NoError(t, err, "NewBuilder must succeed")

			var u tlog.Update
			assert.NoError(t, once.Integrate(all, &u), "Integrate must succeed")
			want := tilesOf(&u)
			assert.Equal(t, storedOf(m, want), want, "the tiles must not depend on the batching")
		})

		t.Run("returns ErrLeafSize for a leaf of another size", func(t *testing.T) {
			t.Parallel()
			b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
			assert.NoError(t, err, "NewBuilder must succeed")

			var u tlog.Update
			wrong := coresha512.New384().Hash([]byte("a SHA-384 digest"))
			assert.ErrorIs(t, b.Integrate([]crypto.Digest{all[0], wrong}, &u), tlog.ErrLeafSize,
				"a SHA-384 leaf in a SHA-256 tree must be ErrLeafSize")
		})

		t.Run("empties the Update for a leaf of another size", func(t *testing.T) {
			t.Parallel()
			b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
			assert.NoError(t, err, "NewBuilder must succeed")

			var u tlog.Update
			assert.NoError(t, b.Integrate(all[:300], &u), "Integrate must succeed")
			wrong := coresha512.New384().Hash([]byte("a SHA-384 digest"))
			assert.ErrorIs(t, b.Integrate([]crypto.Digest{all[0], wrong}, &u), tlog.ErrLeafSize,
				"a SHA-384 leaf in a SHA-256 tree must be ErrLeafSize")
			expect.Empty(t, tilesOf(&u), "the Update must have no tiles")
			expect.Equal(t, u.Size(), uint64(0), "the Update must describe the unchanged tree")
		})

		t.Run("returns nil for the leaf that brings a tree to 2^64 - 1 leaves", func(t *testing.T) {
			t.Parallel()
			// A tree of 2^64 - 2 leaves has a partial tile of 254 hashes at
			// level 0 and of 255 hashes at every other level. Their content
			// does not matter here.
			m := &memTiles{data: map[tlog.Tile][]byte{}}
			for level := range uint8(8) {
				tile := tlog.Tile{Level: level, Index: math.MaxUint64 >> (8 * (uint(level) + 1)), Width: 255}
				if level == 0 {
					tile.Width = 254
				}
				m.data[tile] = make([]byte, int(tile.Width)*h.Hash(nil).Size())
			}
			b, err := tlog.NewBuilder(t.Context(), h, math.MaxUint64-1, m)
			assert.NoError(t, err, "NewBuilder must succeed")

			var u tlog.Update
			assert.NoError(t, b.Integrate(all[:1], &u), "the leaf that brings the tree to 2^64 - 1 must be accepted")
			assert.Equal(t, u.Size(), uint64(math.MaxUint64), "the tree must have 2^64 - 1 leaves")
		})

		t.Run("returns ErrRange for a leaf past 2^64 - 1 leaves", func(t *testing.T) {
			t.Parallel()
			// A tree of 2^64 - 1 leaves has a partial tile of 255 hashes at
			// every level. Their content does not matter here.
			m := &memTiles{data: map[tlog.Tile][]byte{}}
			for level := range uint8(8) {
				tile := tlog.Tile{Level: level, Index: math.MaxUint64 >> (8 * (uint(level) + 1)), Width: 255}
				m.data[tile] = make([]byte, 255*h.Hash(nil).Size())
			}
			b, err := tlog.NewBuilder(t.Context(), h, math.MaxUint64, m)
			assert.NoError(t, err, "NewBuilder must succeed")

			var u tlog.Update
			assert.ErrorIs(t, b.Integrate(all[:1], &u), tlog.ErrRange, "the leaf past 2^64 - 1 must be ErrRange")
		})

		t.Run("writes nothing for an empty batch", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: map[tlog.Tile][]byte{}}
			b, err := tlog.NewBuilder(t.Context(), h, 0, m)
			assert.NoError(t, err, "NewBuilder must succeed")
			grow(t, b, m, all, 10)

			var u tlog.Update
			assert.NoError(t, b.Integrate(nil, &u), "Integrate must succeed")
			assert.Empty(t, tilesOf(&u), "an empty batch must write no tile")
		})

		t.Run("reuses the buffers of the Update", func(t *testing.T) {
			t.Parallel()
			b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
			assert.NoError(t, err, "NewBuilder must succeed")

			var u tlog.Update
			assert.NoError(t, b.Integrate(all[:300], &u), "Integrate must succeed")
			first := firstData(&u)
			assert.NotNil(t, first, "the Update must have a tile")
			assert.NoError(t, b.Integrate(all[300:600], &u), "Integrate must succeed")
			assert.Equal(t, firstData(&u), first, "Integrate must write the tiles into the buffers of the Update",
				assert.ByIdentity())
		})

		t.Run("leaves the tree unchanged for an empty batch after a batch into the same Update", func(t *testing.T) {
			t.Parallel()
			b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
			assert.NoError(t, err, "NewBuilder must succeed")

			var u tlog.Update
			assert.NoError(t, b.Integrate(all[:300], &u), "Integrate must succeed")
			assert.NoError(t, b.Integrate(nil, &u), "Integrate must succeed")
			assert.NoError(t, b.Commit(&u), "Commit must succeed")
			assert.NoError(t, b.Integrate(all[:5], &u), "Integrate must succeed")
			assert.NoError(t, b.Commit(&u), "Commit must succeed")
			assert.Equal(t, b.Root(), tlog.Root(h, all[:5]), "the empty batch must not change the tree")
		})
	})

	t.Run("Commit", func(t *testing.T) {
		t.Parallel()

		t.Run("sets the tree of the Builder to the tree of the Update", func(t *testing.T) {
			t.Parallel()
			for size, want := range v.tiles {
				b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
				assert.NoError(t, err, "NewBuilder must succeed")

				var u tlog.Update
				assert.NoError(t, b.Integrate(all[:size], &u), "Integrate must succeed")
				assert.NoError(t, b.Commit(&u), "Commit must succeed")
				expect.Equal(t, b.Root(), want.root, "the committed root of "+strconv.FormatUint(size, 10)+" leaves")
				expect.Equal(t, b.Size(), size, "the committed size of "+strconv.FormatUint(size, 10)+" leaves")
			}
		})

		t.Run("returns ErrStale for an Update of another Builder", func(t *testing.T) {
			t.Parallel()
			b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
			assert.NoError(t, err, "NewBuilder must succeed")
			other, err := tlog.NewBuilder(t.Context(), h, 0, nil)
			assert.NoError(t, err, "NewBuilder must succeed")

			var u tlog.Update
			assert.NoError(t, b.Integrate(all[:5], &u), "Integrate must succeed")
			assert.ErrorIs(t, other.Commit(&u), tlog.ErrStale, "an Update of another Builder must be ErrStale")
		})

		t.Run("returns ErrStale for an Update of a superseded tree", func(t *testing.T) {
			t.Parallel()
			b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
			assert.NoError(t, err, "NewBuilder must succeed")

			var first, second tlog.Update
			assert.NoError(t, b.Integrate(all[:5], &first), "Integrate must succeed")
			assert.NoError(t, b.Integrate(all[:7], &second), "Integrate must succeed")
			assert.NoError(t, b.Commit(&first), "Commit must succeed")
			assert.ErrorIs(t, b.Commit(&second), tlog.ErrStale, "an Update of a superseded tree must be ErrStale")
		})
	})

	t.Run("Update", func(t *testing.T) {
		t.Parallel()

		t.Run("Size", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the size of the tree after the batch", func(t *testing.T) {
				t.Parallel()
				m := &memTiles{data: map[tlog.Tile][]byte{}}
				b, err := tlog.NewBuilder(t.Context(), h, 0, m)
				assert.NoError(t, err, "NewBuilder must succeed")
				grow(t, b, m, all, 10)

				var u tlog.Update
				assert.NoError(t, b.Integrate(all[10:15], &u), "Integrate must succeed")
				assert.Equal(t, u.Size(), uint64(15), "Size must count the leaves of the tree and of the batch")
			})
		})

		t.Run("Root", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the recorded root of every recorded tree", func(t *testing.T) {
				t.Parallel()
				for size, want := range v.tiles {
					b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
					assert.NoError(t, err, "NewBuilder must succeed")

					var u tlog.Update
					assert.NoError(t, b.Integrate(all[:size], &u), "Integrate must succeed")
					expect.Equal(t, u.Root(), want.root, "the root of "+strconv.FormatUint(size, 10)+" leaves")
				}
			})

			t.Run("returns the root of the stored tiles after each batch", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "Root must return the root of the stored tiles after each batch", func(c *prop.Case) {
					m := &memTiles{data: map[tlog.Tile][]byte{}}
					b, err := tlog.NewBuilder(c.Context(), h, 0, m)
					assert.NoError(c, err, "NewBuilder must succeed")

					var u tlog.Update
					for _, n := range batchesOf(c, batchedSize) {
						assert.NoError(c, b.Integrate(all[b.Size():b.Size()+uint64(n)], &u), "Integrate must succeed")
						m.store(&u)

						stored, err := tlog.TreeRoot(c.Context(), h, m, u.Size())
						assert.NoError(c, err, "TreeRoot must succeed")
						assert.Equal(c, u.Root(), stored, "the root at "+strconv.FormatUint(u.Size(), 10))
						assert.NoError(c, b.Commit(&u), "Commit must succeed")
					}
				})
			})
		})

		t.Run("Tiles", func(t *testing.T) {
			t.Parallel()

			t.Run("yields no tiles for the zero Update", func(t *testing.T) {
				t.Parallel()
				var u tlog.Update
				assert.Empty(t, tilesOf(&u), "the zero Update must yield nothing")
			})

			t.Run("stops when the caller stops", func(t *testing.T) {
				t.Parallel()
				b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
				assert.NoError(t, err, "NewBuilder must succeed")

				var u tlog.Update
				assert.NoError(t, b.Integrate(all[:600], &u), "Integrate must succeed")

				n := 0
				for range u.Tiles() {
					n++

					break
				}
				assert.Equal(t, n, 1, "the iteration must end at the break of the caller")
			})
		})
	})
}

// TestBuilderModel checks a Builder against a model of the leaves that it
// committed, through any sequence of calls of a writer: Integrate a batch,
// store the tiles of an Update, Commit an Update after its tiles, and open
// the Builder again at the committed size, as after a restart. Every Update
// integrates a prefix of one list of leaves, so the stored tiles of any
// Update agree with the tiles of every tree.
func TestBuilderModel(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	all := leaves(modelLeaves)

	t.Run("the Builder keeps the tree of the leaves that it committed under any sequence of calls", func(t *testing.T) {
		t.Parallel()
		// roots keeps the root of each prefix of all that a case reached. The
		// cases run one at a time, so they share it without a lock.
		roots := map[int]crypto.Digest{}
		rootOf := func(n int) crypto.Digest {
			if r, ok := roots[n]; ok {
				return r
			}
			roots[n] = tlog.Root(h, all[:n])

			return roots[n]
		}

		prop.ForAll(t, "the Builder must keep the tree of the leaves that it committed", func(c *prop.Case) {
			m := &memTiles{data: map[tlog.Tile][]byte{}}
			b, err := tlog.NewBuilder(c.Context(), h, 0, m)
			assert.NoError(c, err, "NewBuilder must succeed")

			committed := 0
			var updates []pending
			pick := func(c *prop.Case, _ struct{}) any {
				last := len(updates) - 1

				return c.Draw(prop.OneOf(prop.Just(last), prop.Integer(0, last)), "update")
			}
			computed := func(struct{}) bool { return len(updates) > 0 }

			stateful.Steps(c, stateful.Machine[struct{}]{
				Actions: []stateful.Action[struct{}]{
					{
						Name: "integrate",
						Input: func(c *prop.Case, _ struct{}) any {
							return c.Draw(prop.Integer(0, modelBatch), "batch")
						},
						Run: func(c *prop.Case, _ int, in any) {
							size := min(committed+in.(int), len(all))
							u := new(tlog.Update)
							assert.NoError(c, b.Integrate(all[committed:size], u), "Integrate must succeed")
							assert.Equal(c, u.Size(), uint64(size), "the Update must add the batch to the tree")
							assert.Equal(c, u.Root(), rootOf(size), "the Update must compute the root of its leaves")
							updates = append(updates, pending{u: u, b: b, base: committed})
						},
					},
					{
						Name:    "store",
						Enabled: computed,
						Input:   pick,
						Run:     func(_ *prop.Case, _ int, in any) { m.store(updates[in.(int)].u) },
					},
					{
						Name:    "commit",
						Enabled: computed,
						Input:   pick,
						Run: func(c *prop.Case, _ int, in any) {
							p := updates[in.(int)]
							m.store(p.u)
							commitErr := b.Commit(p.u)
							if p.b != b || p.base != committed {
								c.Classify(staleUpdate)
								assert.ErrorIs(c, commitErr, tlog.ErrStale,
									"an Update of a superseded tree must be ErrStale")

								return
							}
							c.Classify(committedUpdate)
							assert.NoError(c, commitErr, "Commit must accept an Update of the current tree")
							committed = int(p.u.Size())
						},
					},
					{
						Name: "reopen",
						Run: func(c *prop.Case, _ int, _ any) {
							b, err = tlog.NewBuilder(c.Context(), h, uint64(committed), m)
							assert.NoError(c, err, "NewBuilder must read the tiles of the committed tree")
						},
					},
				},
				Invariant: func(c *prop.Case, _ struct{}) {
					assert.Equal(c, b.Size(), uint64(committed), "Size must return the committed size")
					assert.Equal(c, b.Root(), rootOf(committed), "Root must return the root of the committed leaves")
				},
			})
		}, prop.Require(committedUpdate, 0.1), prop.Require(staleUpdate, 0.1))
	})
}

// TestBuilderAllocs checks the allocation contract of each function and
// method. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestBuilderAllocs(t *testing.T) {
	h := coresha256.New()
	all := leaves(batchedSize)
	m := &memTiles{data: map[tlog.Tile][]byte{}}

	b, err := tlog.NewBuilder(t.Context(), h, 0, m)
	assert.NoError(t, err, "NewBuilder must succeed")
	grow(t, b, m, all, publishedSize)

	t.Run("NewBuilder", func(t *testing.T) {
		t.Run("of an empty tree", func(t *testing.T) {
			ctx := t.Context()
			expect.MaxAllocs(t, func() { _, err = tlog.NewBuilder(ctx, h, 0, nil) }, 2,
				"NewBuilder must allocate the Builder and its state alone")
			assert.NoError(t, err, "the test must measure a Builder that NewBuilder returns")
		})

		t.Run("of a published size", func(t *testing.T) {
			ctx := t.Context()
			expect.MaxAllocs(t, func() { _, err = tlog.NewBuilder(ctx, h, publishedSize, m) }, 6,
				"NewBuilder must allocate the Builder, its state, the two lists of ReadTiles and two tiles alone")
			assert.NoError(t, err, "the test must measure a Builder that NewBuilder returns")
		})
	})

	t.Run("Size", func(t *testing.T) {
		var got uint64
		expect.MaxAllocs(t, func() { got = b.Size() }, 0, "Size must not allocate")
		assert.Equal(t, got, uint64(publishedSize), "the test must measure the size of the tree")
	})

	t.Run("Root", func(t *testing.T) {
		var got crypto.Digest
		expect.MaxAllocs(t, func() { got = b.Root() }, 0, "Root must not allocate")
		assert.NotEqual(t, got, crypto.Digest{}, "the test must measure a root")
	})

	t.Run("Integrate", func(t *testing.T) {
		var u tlog.Update
		assert.NoError(t, b.Integrate(all[publishedSize:], &u), "Integrate must succeed")

		expect.MaxAllocs(t, func() { err = b.Integrate(all[publishedSize:], &u) }, 0,
			"Integrate must not allocate into a reused Update")
		assert.NoError(t, err, "the test must measure a batch that Integrate accepts")
	})

	t.Run("Commit", func(t *testing.T) {
		expect.MaxAllocsWithSetup(t, func() *update {
			fresh, buildErr := tlog.NewBuilder(t.Context(), h, 0, nil)
			assert.NoError(t, buildErr, "NewBuilder must succeed")

			u := &update{b: fresh}
			assert.NoError(t, fresh.Integrate(all, &u.Update), "Integrate must succeed")

			return u
		}, func(u *update) { err = u.b.Commit(&u.Update) }, 0, "Commit must not allocate")
		assert.NoError(t, err, "the test must measure an Update that Commit accepts")
	})

	t.Run("Update", func(t *testing.T) {
		var u tlog.Update
		assert.NoError(t, b.Integrate(all[publishedSize:], &u), "Integrate must succeed")

		t.Run("Size", func(t *testing.T) {
			var got uint64
			expect.MaxAllocs(t, func() { got = u.Size() }, 0, "Size must not allocate")
			assert.Equal(t, got, uint64(batchedSize), "the test must measure the size after the batch")
		})

		t.Run("Root", func(t *testing.T) {
			var got crypto.Digest
			expect.MaxAllocs(t, func() { got = u.Root() }, 0, "Root must not allocate")
			assert.NotEqual(t, got, crypto.Digest{}, "the test must measure a root")
		})

		t.Run("Tiles", func(t *testing.T) {
			n := 0
			expect.MaxAllocs(t, func() {
				for range u.Tiles() {
					n++
				}
			}, 1, "Tiles must allocate its closure alone")
			assert.NotEqual(t, n, 0, "the test must measure tiles")
		})
	})
}

// BenchmarkBuilder reports the cost of each function and method, and
// fails above the allocations that their contracts state.
func BenchmarkBuilder(b *testing.B) {
	h := coresha256.New()
	all := leaves(batchedSize)
	m := &memTiles{data: map[tlog.Tile][]byte{}}

	tb, err := tlog.NewBuilder(b.Context(), h, 0, m)
	assert.NoError(b, err, "NewBuilder must succeed")
	grow(b, tb, m, all, publishedSize)

	b.Run("NewBuilder", func(b *testing.B) {
		b.Run("of an empty tree", func(b *testing.B) {
			ctx := b.Context()

			var err error

			c := bench.Start(b).MaxAllocs(2)
			defer c.End()

			for c.Loop() {
				_, err = tlog.NewBuilder(ctx, h, 0, nil)
			}

			assert.NoError(b, err, "the benchmark must measure a Builder that NewBuilder returns")
		})

		b.Run("of a published size", func(b *testing.B) {
			ctx := b.Context()

			var err error

			c := bench.Start(b).MaxAllocs(6)
			defer c.End()

			for c.Loop() {
				_, err = tlog.NewBuilder(ctx, h, publishedSize, m)
			}

			assert.NoError(b, err, "the benchmark must measure a Builder that NewBuilder returns")
		})
	})

	b.Run("Size", func(b *testing.B) {
		var got uint64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = tb.Size()
		}

		assert.Equal(b, got, uint64(publishedSize), "the benchmark must measure the size of the tree")
	})

	b.Run("Root", func(b *testing.B) {
		var got crypto.Digest

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = tb.Root()
		}

		assert.NotEqual(b, got, crypto.Digest{}, "the benchmark must measure a root")
	})

	b.Run("Integrate", func(b *testing.B) {
		var u tlog.Update
		assert.NoError(b, tb.Integrate(all[publishedSize:], &u), "Integrate must succeed")

		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = tb.Integrate(all[publishedSize:], &u)
		}

		assert.NoError(b, err, "the benchmark must measure a batch that Integrate accepts")
	})

	b.Run("Commit", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			var u *update
			c.Excluding(func() {
				fresh, buildErr := tlog.NewBuilder(b.Context(), h, 0, nil)
				assert.NoError(b, buildErr, "NewBuilder must succeed")

				u = &update{b: fresh}
				assert.NoError(b, fresh.Integrate(all, &u.Update), "Integrate must succeed")
			})
			err = u.b.Commit(&u.Update)
		}

		assert.NoError(b, err, "the benchmark must measure an Update that Commit accepts")
	})

	b.Run("Update", func(b *testing.B) {
		var u tlog.Update
		assert.NoError(b, tb.Integrate(all[publishedSize:], &u), "Integrate must succeed")

		b.Run("Size", func(b *testing.B) {
			var got uint64

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = u.Size()
			}

			assert.Equal(b, got, uint64(batchedSize), "the benchmark must measure the size after the batch")
		})

		b.Run("Root", func(b *testing.B) {
			var got crypto.Digest

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = u.Root()
			}

			assert.NotEqual(b, got, crypto.Digest{}, "the benchmark must measure a root")
		})

		b.Run("Tiles", func(b *testing.B) {
			n := 0

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				for range u.Tiles() {
					n++
				}
			}

			assert.NotEqual(b, n, 0, "the benchmark must measure tiles")
		})
	})
}

// tileDigest returns the SHA-256 digest of every tile of u, in the format
// of the tiles vectors.
func tileDigest(tb testing.TB, u *tlog.Update) crypto.Digest {
	tb.Helper()

	s := sha256.New()
	for t, data := range u.Tiles() {
		var hdr [11]byte
		hdr[0] = t.Level
		binary.BigEndian.PutUint64(hdr[1:], t.Index)
		binary.BigEndian.PutUint16(hdr[9:], t.Width)
		s.Write(hdr[:])
		s.Write(data)
	}

	d, err := crypto.DigestFromBytes(s.Sum(nil))
	assert.NoError(tb, err, "a SHA-256 sum must be a digest")

	return d
}

// tilesOf returns the tiles of u and a copy of their data.
func tilesOf(u *tlog.Update) map[tlog.Tile][]byte {
	out := map[tlog.Tile][]byte{}
	for t, data := range u.Tiles() {
		out[t] = slices.Clone(data)
	}

	return out
}

// firstData returns the address of the first byte of the first tile of u,
// or nil when u has no tile.
func firstData(u *tlog.Update) *byte {
	for _, data := range u.Tiles() {
		return &data[0]
	}

	return nil
}

// storedOf returns the data that m stores for each tile of want, for a
// comparison with want.
func storedOf(m *memTiles, want map[tlog.Tile][]byte) map[tlog.Tile][]byte {
	got := make(map[tlog.Tile][]byte, len(want))
	for tile := range want {
		got[tile] = m.data[tile]
	}

	return got
}

// batchesOf returns the sizes of batches of 1 to maxBatch leaves that add
// up to size, which c generates.
func batchesOf(c *prop.Case, size int) []int {
	var batches []int
	for rest := size; rest > 0; {
		n := c.Draw(prop.Integer(1, min(maxBatch, rest)), "batch")
		batches = append(batches, n)
		rest -= n
	}

	return batches
}
