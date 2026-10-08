// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"bytes"
	"context"
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

// The fixture values of the cases of TileVerifier.
const (
	// tileHashes is the number of node hashes of the root of a full tile.
	tileHashes = 255

	// farIndex is a leaf of the tree of 70000 leaves whose path shares no tile
	// with the path of leaf 0. Its tile of level 0 is full, and its tile of
	// level 1 is the partial tile of the right edge.
	farIndex = 65536

	// wideSize is the size of the tree of SHA-384 leaves of the case of a
	// hasher with a larger digest.
	wideSize = 300

	// verifyWeight is how much more often a step of the machine of
	// TileVerifier checks a leaf than it takes another action.
	verifyWeight = 4
)

// The tiles of the tree of 70000 leaves that the cases read or change. The
// tree has 273 full tiles and a partial tile of 112 hashes at level 0.
var (
	fullTile0 = tlog.Tile{Level: 0, Index: 0, Width: fullWidth}
	nextTile0 = tlog.Tile{Level: 0, Index: 1, Width: fullWidth}
	fullTile1 = tlog.Tile{Level: 1, Index: 0, Width: fullWidth}
	farTile0  = tlog.Tile{Level: 0, Index: farIndex / fullWidth, Width: fullWidth}
	edgeTile0 = tlog.Tile{Level: 0, Index: 273, Width: 112}
)

// changedTiles is a TileReader over the tiles of m that flips the last byte of
// tile in each of the next remain reads of it.
type changedTiles struct {
	m      *memTiles
	tile   tlog.Tile
	remain int
}

// ReadTiles reads tiles from m, and flips the last byte of the data of c.tile
// while reads of it remain.
func (c *changedTiles) ReadTiles(ctx context.Context, tiles []tlog.Tile, dst [][]byte) error {
	if err := c.m.ReadTiles(ctx, tiles, dst); err != nil {
		return err
	}

	for i, t := range tiles {
		if t == c.tile && c.remain > 0 {
			c.remain--
			dst[i][len(dst[i])-1] ^= 1
		}
	}

	return nil
}

// roomTiles is a TileReader over the tiles of m that records the least room
// of the buffers that its calls receive.
type roomTiles struct {
	m     *memTiles
	least int
}

// ReadTiles records the room of each buffer of dst, and reads tiles from m.
func (r *roomTiles) ReadTiles(ctx context.Context, tiles []tlog.Tile, dst [][]byte) error {
	for _, d := range dst {
		r.least = min(r.least, cap(d)-len(d))
	}

	return r.m.ReadTiles(ctx, tiles, dst)
}

// summingHasher is SHA-256 whose streams count the hashes that they finish in
// sums.
type summingHasher struct {
	coresha256.Hasher

	sums *int
}

// NewStream returns a SHA-256 stream that counts its sums in h.sums.
func (h summingHasher) NewStream() crypto.Stream {
	return summingStream{Stream: h.Hasher.NewStream(), sums: h.sums}
}

// summingStream is a stream that counts its sums in sums.
type summingStream struct {
	crypto.Stream

	sums *int
}

// Sum counts the hash and returns the sum of the stream.
func (s summingStream) Sum() crypto.Digest {
	*s.sums++

	return s.Stream.Sum()
}

func TestVerifier(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	small, smallTiles, large, largeTiles := storedTree(t)
	largeRoot := tlog.Root(h, large)

	// roots[n] is the root of the tree of the first n leaves of small.
	roots := make([]crypto.Digest, smallSize+1)
	for n := range roots {
		roots[n] = tlog.Root(h, small[:n])
	}

	// trees are the sampled trees and the tree of 70000 leaves, with their
	// leaves, their stored tiles and their roots.
	type tree struct {
		tiles *memTiles
		all   []crypto.Digest
		root  crypto.Digest
		size  uint64
	}
	trees := make([]tree, 0, len(sampleSizes)+1)
	for _, size := range sampleSizes {
		trees = append(trees, tree{tiles: smallTiles, all: small, root: roots[size], size: size})
	}
	trees = append(trees, tree{tiles: largeTiles, all: large, root: largeRoot, size: largeSize})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("drops the tiles that the verifier keeps", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: largeTiles.data}
			var v tlog.TileVerifier
			v.Reset(h, m, largeSize, largeRoot)
			assert.NoError(t, v.Verify(t.Context(), 0, large[0]), "Verify must accept the first leaf")
			v.Reset(h, m, largeSize, largeRoot)
			assert.NoError(t, v.Verify(t.Context(), 0, large[0]), "Verify must accept the first leaf after Reset")
			assert.Equal(t, m.calls, 2, "Verify must read the tiles again after Reset")
		})

		t.Run("starts the checks of a tree without the partial tiles of the last tree", func(t *testing.T) {
			t.Parallel()
			var v tlog.TileVerifier
			v.Reset(h, &memTiles{data: largeTiles.data}, largeSize, largeRoot)
			assert.NoError(t, v.Verify(t.Context(), 0, large[0]),
				"Verify must accept the first leaf of the larger tree")
			v.Reset(h, &memTiles{data: smallTiles.data}, smallSize, roots[smallSize])
			assert.Total(t, func(i uint64) error { return v.Verify(t.Context(), i, small[i]) }, indexes(smallSize),
				"Verify must accept every leaf of the smaller tree")
		})

		t.Run("starts the checks of a tree of a hasher with a larger digest", func(t *testing.T) {
			t.Parallel()
			wide := coresha512.New384()
			wideLeaves := make([]crypto.Digest, wideSize)
			for i := range wideLeaves {
				wideLeaves[i] = tlog.LeafHash(wide, []byte("entry "+strconv.Itoa(i)))
			}
			m := &memTiles{data: map[tlog.Tile][]byte{}}
			b, err := tlog.NewBuilder(t.Context(), wide, 0, m)
			assert.NoError(t, err, "NewBuilder must succeed")
			grow(t, b, m, wideLeaves, wideSize)

			var v tlog.TileVerifier
			v.Reset(h, &memTiles{data: smallTiles.data}, smallSize, roots[smallSize])
			assert.NoError(t, v.Verify(t.Context(), 0, small[0]), "Verify must accept the first SHA-256 leaf")
			v.Reset(wide, m, wideSize, b.Root())
			assert.Total(t, func(i uint64) error { return v.Verify(t.Context(), i, wideLeaves[i]) }, indexes(wideSize),
				"Verify must accept every SHA-384 leaf")
		})
	})

	t.Run("Verify", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for every leaf of each tree in index order", func(t *testing.T) {
			t.Parallel()
			for _, tr := range trees {
				var v tlog.TileVerifier
				v.Reset(h, &memTiles{data: tr.tiles.data}, tr.size, tr.root)
				assert.Total(t, func(i uint64) error { return v.Verify(t.Context(), i, tr.all[i]) }, indexes(tr.size),
					"Verify must accept every leaf of the tree of "+strconv.FormatUint(tr.size, 10)+" leaves")
			}
		})

		t.Run("returns nil for every leaf of each tree in reverse index order", func(t *testing.T) {
			t.Parallel()
			for _, tr := range trees {
				var v tlog.TileVerifier
				v.Reset(h, &memTiles{data: tr.tiles.data}, tr.size, tr.root)
				reversed := indexes(tr.size)
				slices.Reverse(reversed)
				assert.Total(t, func(i uint64) error { return v.Verify(t.Context(), i, tr.all[i]) }, reversed,
					"Verify must accept every leaf of the tree of "+strconv.FormatUint(tr.size, 10)+" leaves")
			}
		})

		t.Run("returns nil only for the leaf at its index under any sequence of calls", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Verify must return nil only for the leaf at its index", func(c *prop.Case) {
				m := &memTiles{data: smallTiles.data}
				size := uint64(smallSize)
				var v tlog.TileVerifier
				v.Reset(h, m, size, roots[size])

				// size changes only with the input of a reset, so the draws of the
				// other actions may read it.
				stateful.Steps(c, stateful.Machine[struct{}]{
					Actions: []stateful.Action[struct{}]{
						{
							Name: "reset",
							Input: func(c *prop.Case, _ struct{}) any {
								return c.Draw(prop.Integer[uint64](1, smallSize), "size")
							},
							Run: func(_ *prop.Case, _ int, in any) {
								size, _ = in.(uint64)
								v.Reset(h, m, size, roots[size])
							},
						},
						{
							Name:   "verify",
							Weight: verifyWeight,
							Input: func(c *prop.Case, _ struct{}) any {
								return c.Draw(prop.Integer[uint64](0, size-1), "index")
							},
							Run: func(c *prop.Case, _ int, in any) {
								i, _ := in.(uint64)
								assert.NoError(c, v.Verify(c.Context(), i, small[i]),
									"Verify must accept the leaf at its index")
							},
						},
						{
							Name: "verify another leaf",
							Input: func(c *prop.Case, _ struct{}) any {
								i := c.Draw(prop.Integer[uint64](0, size-1), "index")
								distance := c.Draw(prop.Integer[uint64](1, smallSize-1), "distance")

								return [2]uint64{i, (i + distance) % smallSize}
							},
							Run: func(c *prop.Case, _ int, in any) {
								pair, _ := in.([2]uint64)
								assert.ErrorIs(c, v.Verify(c.Context(), pair[0], small[pair[1]]), tlog.ErrProof,
									"Verify must refuse the leaf of another index")
							},
						},
					},
				})
			})
		})

		t.Run("reads each tile of the tree once for the leaves in index order", func(t *testing.T) {
			t.Parallel()
			for _, tr := range trees {
				m := &memTiles{data: tr.tiles.data}
				var v tlog.TileVerifier
				v.Reset(h, m, tr.size, tr.root)

				var read []tlog.Tile
				assert.Total(t, func(i uint64) error {
					calls := m.calls
					err := v.Verify(t.Context(), i, tr.all[i])
					if m.calls != calls {
						read = append(read, m.last...)
					}

					return err
				}, indexes(tr.size), "Verify must accept every leaf of the tree")
				expect.Permutation(t, read, slices.Collect(tlog.Tiles(0, tr.size)),
					"the checks of "+strconv.FormatUint(tr.size, 10)+" leaves must read each tile once")
			}
		})

		t.Run("hashes each interior node of the tree once for the leaves in index order", func(t *testing.T) {
			t.Parallel()
			for _, tr := range trees {
				sums := 0
				var v tlog.TileVerifier
				v.Reset(summingHasher{sums: &sums}, &memTiles{data: tr.tiles.data}, tr.size, tr.root)
				assert.Total(t, func(i uint64) error { return v.Verify(t.Context(), i, tr.all[i]) }, indexes(tr.size),
					"Verify must accept every leaf of the tree")
				expect.Equal(t, sums, int(tr.size)-1,
					"the checks of "+strconv.FormatUint(tr.size, 10)+" leaves must hash each interior node once")
			}
		})

		t.Run("reads nothing for a leaf in the tiles that it keeps", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: largeTiles.data}
			var v tlog.TileVerifier
			v.Reset(h, m, largeSize, largeRoot)
			assert.NoError(t, v.Verify(t.Context(), 0, large[0]), "Verify must accept the first leaf")
			assert.NoError(t, v.Verify(t.Context(), 1, large[1]), "Verify must accept the second leaf")
			assert.Equal(t, m.calls, 1, "the check of the second leaf must not read")
		})

		t.Run("hashes nothing for a leaf in the tiles that it keeps", func(t *testing.T) {
			t.Parallel()
			sums := 0
			var v tlog.TileVerifier
			v.Reset(summingHasher{sums: &sums}, &memTiles{data: largeTiles.data}, largeSize, largeRoot)
			assert.NoError(t, v.Verify(t.Context(), 0, large[0]), "Verify must accept the first leaf")
			hashed := sums
			assert.NoError(t, v.Verify(t.Context(), 1, large[1]), "Verify must accept the second leaf")
			assert.Equal(t, sums, hashed, "the check of the second leaf must not hash")
		})

		t.Run("reads the full tiles of its path that it does not keep", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: largeTiles.data}
			var v tlog.TileVerifier
			v.Reset(h, m, largeSize, largeRoot)
			assert.NoError(t, v.Verify(t.Context(), farIndex, large[farIndex]), "Verify must accept the far leaf")
			assert.NoError(t, v.Verify(t.Context(), 0, large[0]),
				"Verify must accept the first leaf after the far leaf")
			expect.Equal(t, m.last, []tlog.Tile{fullTile0, fullTile1},
				"the check of the first leaf must read the two full tiles of its path")
			assert.NoError(t, v.Verify(t.Context(), farIndex, large[farIndex]),
				"Verify must accept the far leaf after the first leaf")
			expect.Equal(t, m.last, []tlog.Tile{farTile0},
				"the check of the far leaf must read the full tile of its path")
		})

		t.Run("hashes 255 nodes for each full tile that it reads again", func(t *testing.T) {
			t.Parallel()
			sums := 0
			m := &memTiles{data: largeTiles.data}
			var v tlog.TileVerifier
			v.Reset(summingHasher{sums: &sums}, m, largeSize, largeRoot)
			assert.NoError(t, v.Verify(t.Context(), farIndex, large[farIndex]), "Verify must accept the far leaf")
			for _, i := range []uint64{0, farIndex, 0, farIndex} {
				hashed := sums
				assert.NoError(t, v.Verify(t.Context(), i, large[i]),
					"Verify must accept leaf "+strconv.FormatUint(i, 10))
				expect.Equal(t, sums-hashed, tileHashes*len(m.last),
					"the check of leaf "+strconv.FormatUint(i, 10)+" must hash 255 nodes for each tile that it reads")
			}
		})

		t.Run("hands ReadTiles buffers with room for a full tile and bytes.MinRead more bytes", func(t *testing.T) {
			t.Parallel()
			r := &roomTiles{m: &memTiles{data: largeTiles.data}, least: math.MaxInt}
			var v tlog.TileVerifier
			v.Reset(h, r, largeSize, largeRoot)
			for _, i := range []uint64{0, farIndex} {
				assert.NoError(t, v.Verify(t.Context(), i, large[i]),
					"Verify must accept leaf "+strconv.FormatUint(i, 10))
			}
			assert.InRange(t, r.least, float64(fullWidth*h.Hash(nil).Size()+bytes.MinRead), math.MaxInt,
				"every buffer must have room for a full tile and bytes.MinRead more bytes")
		})

		t.Run("returns ErrProof for the leaf of another index", func(t *testing.T) {
			t.Parallel()
			var v tlog.TileVerifier
			v.Reset(h, &memTiles{data: largeTiles.data}, largeSize, largeRoot)
			for _, i := range []uint64{0, 1, fullWidth - 1, fullWidth, farIndex, largeSize - 1} {
				expect.ErrorIs(t, v.Verify(t.Context(), i, large[(i+1)%largeSize]), tlog.ErrProof,
					"the leaf after leaf "+strconv.FormatUint(i, 10)+" must not verify at its index")
			}
		})

		t.Run("returns ErrProof for a leaf of another digest size", func(t *testing.T) {
			t.Parallel()
			var v tlog.TileVerifier
			v.Reset(h, &memTiles{data: largeTiles.data}, largeSize, largeRoot)
			wider := coresha512.New384().Hash([]byte("wider"))
			assert.ErrorIs(t, v.Verify(t.Context(), 0, wider), tlog.ErrProof, "a SHA-384 leaf must not verify")
		})

		t.Run("returns ErrProof for the root of another tree", func(t *testing.T) {
			t.Parallel()
			var v tlog.TileVerifier
			v.Reset(h, &memTiles{data: smallTiles.data}, smallSize, roots[smallSize-1])
			assert.ErrorIs(t, v.Verify(t.Context(), 0, small[0]), tlog.ErrProof,
				"a leaf must not verify against the root of a smaller tree")
		})

		tests := []struct {
			name string
			tile tlog.Tile
		}{
			{name: "returns ErrProof for a changed full tile of level 0", tile: fullTile0},
			{name: "returns ErrProof for a changed full tile of level 1", tile: fullTile1},
			{name: "returns ErrProof for a changed partial tile of the right edge", tile: edgeTile0},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				r := &changedTiles{m: &memTiles{data: largeTiles.data}, tile: tt.tile, remain: math.MaxInt}
				var v tlog.TileVerifier
				v.Reset(h, r, largeSize, largeRoot)
				assert.ErrorIs(t, v.Verify(t.Context(), 1, large[1]), tlog.ErrProof,
					"a leaf must not verify through a changed tile")
			})
		}

		t.Run("returns ErrProof for the hash that a changed tile contains", func(t *testing.T) {
			t.Parallel()
			r := &changedTiles{m: &memTiles{data: largeTiles.data}, tile: fullTile0, remain: math.MaxInt}
			changed := slices.Clone(large[fullWidth-1].Bytes())
			changed[len(changed)-1] ^= 1
			forged, err := crypto.DigestFromBytes(changed)
			assert.NoError(t, err, "a changed SHA-256 hash must be a digest")

			var v tlog.TileVerifier
			v.Reset(h, r, largeSize, largeRoot)
			assert.ErrorIs(t, v.Verify(t.Context(), fullWidth-1, forged), tlog.ErrProof,
				"the last hash of a changed tile must not verify")
		})

		t.Run("reads the kept tile of a level again after another tile of the level fails", func(t *testing.T) {
			t.Parallel()
			r := &changedTiles{m: &memTiles{data: largeTiles.data}, tile: nextTile0, remain: math.MaxInt}
			var v tlog.TileVerifier
			v.Reset(h, r, largeSize, largeRoot)
			assert.NoError(t, v.Verify(t.Context(), 0, large[0]), "Verify must accept the first leaf")
			assert.ErrorIs(t, v.Verify(t.Context(), fullWidth, large[fullWidth]), tlog.ErrProof,
				"a leaf must not verify through a changed tile")
			calls := r.m.calls
			assert.NoError(t, v.Verify(t.Context(), 0, large[0]), "Verify must accept the first leaf again")
			assert.Equal(t, r.m.calls, calls+1, "the check of the first leaf must read its tile again")
		})

		t.Run("returns ErrProof for a hash of a failed tile at an index of the kept tile", func(t *testing.T) {
			t.Parallel()
			r := &changedTiles{m: &memTiles{data: largeTiles.data}, tile: nextTile0, remain: math.MaxInt}
			var v tlog.TileVerifier
			v.Reset(h, r, largeSize, largeRoot)
			assert.NoError(t, v.Verify(t.Context(), 0, large[0]), "Verify must accept the first leaf")
			assert.ErrorIs(t, v.Verify(t.Context(), fullWidth, large[fullWidth]), tlog.ErrProof,
				"a leaf must not verify through a changed tile")
			assert.ErrorIs(t, v.Verify(t.Context(), 0, large[fullWidth]), tlog.ErrProof,
				"the first hash of the failed tile must not verify at the index of the first leaf")
		})

		t.Run("reads a full tile again after it fails", func(t *testing.T) {
			t.Parallel()
			r := &changedTiles{m: &memTiles{data: largeTiles.data}, tile: fullTile0, remain: 1}
			var v tlog.TileVerifier
			v.Reset(h, r, largeSize, largeRoot)
			assert.ErrorIs(t, v.Verify(t.Context(), 1, large[1]), tlog.ErrProof,
				"a leaf must not verify through a changed tile")
			assert.NoError(t, v.Verify(t.Context(), 1, large[1]), "the leaf must verify once the tile reads unchanged")
		})

		t.Run("reads the right edge again after it fails", func(t *testing.T) {
			t.Parallel()
			r := &changedTiles{m: &memTiles{data: largeTiles.data}, tile: edgeTile0, remain: 1}
			var v tlog.TileVerifier
			v.Reset(h, r, largeSize, largeRoot)
			assert.ErrorIs(t, v.Verify(t.Context(), 1, large[1]), tlog.ErrProof,
				"a leaf must not verify through a changed edge")
			assert.NoError(t, v.Verify(t.Context(), 1, large[1]), "the leaf must verify once the edge reads unchanged")
		})

		t.Run("reads the right edge again after a failed read", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: largeTiles.data, err: errAbsent}
			var v tlog.TileVerifier
			v.Reset(h, m, largeSize, largeRoot)
			assert.ErrorIs(t, v.Verify(t.Context(), 0, large[0]), errAbsent, "the error of the reader must be returned")
			m.err = nil
			assert.NoError(t, v.Verify(t.Context(), 0, large[0]), "the leaf must verify once the reader reads")
		})

		t.Run("returns ErrTileSize for a tile of the wrong length", func(t *testing.T) {
			t.Parallel()
			short := &memTiles{data: map[tlog.Tile][]byte{{Width: 3}: make([]byte, 2*h.Hash(nil).Size())}}
			var v tlog.TileVerifier
			v.Reset(h, short, 3, roots[3])
			assert.ErrorIs(t, v.Verify(t.Context(), 0, small[0]), tlog.ErrTileSize, "a short tile must be ErrTileSize")
		})

		t.Run("returns the error of the reader", func(t *testing.T) {
			t.Parallel()
			var v tlog.TileVerifier
			v.Reset(h, &memTiles{data: map[tlog.Tile][]byte{}}, 3, roots[3])
			assert.ErrorIs(t, v.Verify(t.Context(), 0, small[0]), errAbsent, "the error of the reader must be returned")
		})

		t.Run("returns ErrRange for an index at the size", func(t *testing.T) {
			t.Parallel()
			var v tlog.TileVerifier
			v.Reset(h, &memTiles{data: smallTiles.data}, smallSize, roots[smallSize])
			assert.ErrorIs(t, v.Verify(t.Context(), smallSize, small[0]), tlog.ErrRange,
				"an index past the tree must be ErrRange")
		})

		t.Run("returns ErrRange for the zero TileVerifier", func(t *testing.T) {
			t.Parallel()
			var v tlog.TileVerifier
			assert.ErrorIs(t, v.Verify(t.Context(), 0, small[0]), tlog.ErrRange,
				"the zero TileVerifier must check a tree of no leaves")
		})
	})
}

// TestVerifierAllocs checks the allocation contract of TileVerifier over a
// reader that allocates nothing. MaxAllocs counts the allocations of the whole
// process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestVerifierAllocs(t *testing.T) {
	h := coresha256.New()
	_, _, large, largeTiles := storedTree(t)
	root := tlog.Root(h, large)
	ctx := t.Context()

	var v tlog.TileVerifier

	t.Run("Reset", func(t *testing.T) {
		expect.MaxAllocs(t, func() { v.Reset(h, largeTiles, largeSize, root) }, 0, "Reset must not allocate")
		assert.NoError(t, v.Verify(ctx, 0, large[0]), "the test must measure a Reset to the tree")
	})

	t.Run("Verify", func(t *testing.T) {
		t.Run("of the leaves in index order", func(t *testing.T) {
			v.Reset(h, largeTiles, largeSize, root)

			var (
				i   uint64
				err error
			)
			expect.MaxAllocs(t, func() {
				err = v.Verify(ctx, i, large[i])
				i = (i + 1) % largeSize
			}, 0, "Verify must not allocate for the leaves of the tiles that it keeps")
			assert.NoError(t, err, "the test must measure leaves that verify")
		})

		t.Run("of the leaves of alternating paths", func(t *testing.T) {
			v.Reset(h, largeTiles, largeSize, root)
			pair := [2]uint64{0, farIndex}
			for _, i := range pair {
				assert.NoError(t, v.Verify(ctx, i, large[i]), "Verify must grow the buffers of both paths")
			}

			var (
				k   int
				err error
			)
			expect.MaxAllocs(t, func() {
				i := pair[k%2]
				err = v.Verify(ctx, i, large[i])
				k++
			}, 0, "Verify must not allocate to read and verify tiles into its buffers")
			assert.NoError(t, err, "the test must measure leaves that verify")
		})
	})
}

// BenchmarkVerifier reports the cost of Reset, and of Verify for the leaves in
// index order and for the leaves of alternating paths, over a reader that
// allocates nothing, and fails above the allocations that the contract of
// TileVerifier states.
func BenchmarkVerifier(b *testing.B) {
	h := coresha256.New()
	_, _, large, largeTiles := storedTree(b)
	root := tlog.Root(h, large)

	b.Run("Reset", func(b *testing.B) {
		var v tlog.TileVerifier

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			v.Reset(h, largeTiles, largeSize, root)
		}

		assert.NoError(b, v.Verify(b.Context(), 0, large[0]), "the benchmark must measure a Reset to the tree")
	})

	b.Run("Verify", func(b *testing.B) {
		b.Run("in index order", func(b *testing.B) {
			ctx := b.Context()

			var (
				v   tlog.TileVerifier
				i   uint64
				err error
			)
			v.Reset(h, largeTiles, largeSize, root)

			c := bench.Start(b).MaxAllocs(0).Warmup(1)
			defer c.End()

			for c.Loop() {
				err = v.Verify(ctx, i, large[i])
				i = (i + 1) % largeSize
			}

			assert.NoError(b, err, "the benchmark must measure leaves that verify")
		})

		b.Run("on alternating paths", func(b *testing.B) {
			ctx := b.Context()
			pair := [2]uint64{0, farIndex}

			var (
				v   tlog.TileVerifier
				k   int
				err error
			)
			v.Reset(h, largeTiles, largeSize, root)

			c := bench.Start(b).MaxAllocs(0).Warmup(len(pair))
			defer c.End()

			for c.Loop() {
				i := pair[k%2]
				err = v.Verify(ctx, i, large[i])
				k++
			}

			assert.NoError(b, err, "the benchmark must measure leaves that verify")
		})
	})
}

// indexes returns the indexes of a tree of n leaves, in order.
func indexes(n uint64) []uint64 {
	out := make([]uint64, n)
	for i := range out {
		out[i] = uint64(i)
	}

	return out
}
