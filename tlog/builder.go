// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import (
	"context"
	"iter"
	"math"

	"go.thesmos.sh/core/arena"
	"go.thesmos.sh/core/crypto"
)

// Builder integrates batches of leaf hashes into a tree stored as tiles.
//
// A Builder keeps only the right edge of the tree: the partial tile at
// each of the eight levels. Its state is at most 8 x 255 digests, 64 KiB
// with SHA-256 and 96 KiB with SHA-384, whatever the size of the tree.
//
// # Concurrency
//
// Not safe for concurrent use. One writer integrates.
//
// # Allocation contract
//
// [Builder.Integrate] into a reused [Update] allocates nothing once the
// Update's buffers have grown to the batch size. Node hashes fold in
// scratch the Builder allocates once.
type Builder struct {
	h crypto.Hasher

	// edge is the partial tile at each level: the last (size >> 8L) %
	// 256 hashes of tree level 8L.
	edge [tileLevels][]byte

	// scratch has room for one full tile, for folding tiles into roots.
	scratch []byte

	root crypto.Digest
	size uint64

	// digestSize is the digest size of h.
	digestSize int
}

// NewBuilder returns a Builder for the tree of the given size, stored in
// r. It reads the partial tiles at the tree's right edge in one
// [TileReader.ReadTiles] call, so a writer resumes after a restart from
// the last published size. A size of zero reads nothing.
//
// Returns [ErrTileSize] when a tile read has the wrong length, and the
// error of ReadTiles when it fails.
//
// # Allocation contract
//
// Allocates the Builder's state once: its right edge and one tile of
// scratch.
func NewBuilder(ctx context.Context, h crypto.Hasher, size uint64, r TileReader) (*Builder, error) {
	empty := h.Hash(nil)
	ds := empty.Size()

	state := make([]byte, (tileLevels*(tileWidth-1)+tileWidth)*ds)
	b := &Builder{h: h, root: empty, size: size, digestSize: ds, scratch: state[:tileWidth*ds]}
	for level := range b.edge {
		off := (tileWidth + level*(tileWidth-1)) * ds
		b.edge[level] = state[off:off:(off + (tileWidth-1)*ds)]
	}

	if size == 0 {
		return b, nil
	}

	var (
		tiles [tileLevels]Tile
		dst   [tileLevels][]byte
		n     int
	)
	for level := range uint8(tileLevels) {
		row := size >> (tileHeight * uint(level))
		if w := row % tileWidth; w > 0 {
			tiles[n] = Tile{Level: level, Index: row >> tileHeight, Width: uint16(w)}
			dst[n] = b.edge[level]
			n++
		}
	}

	if err := r.ReadTiles(ctx, tiles[:n], dst[:n]); err != nil {
		return nil, err //nolint:wrapcheck // the reader's error passes through with its class
	}

	for i, t := range tiles[:n] {
		if len(dst[i]) != int(t.Width)*ds {
			return nil, ErrTileSize
		}
		b.edge[t.Level] = append(b.edge[t.Level][:0], dst[i]...)
	}

	s := h.NewStream()
	b.root = rootOf(s, ds, &b.edge, b.scratch)
	s.Close()

	return b, nil
}

// Size returns the number of leaves in the Builder's tree.
func (b *Builder) Size() uint64 { return b.size }

// Root returns the Merkle Tree Hash of the Builder's tree.
func (b *Builder) Root() crypto.Digest { return b.root }

// Integrate computes the tree that appending leaves gives, and writes
// into u every tile that tree has and the Builder's tree does not have
// in the same form: the full tiles the batch completes, and the partial
// tiles at the new right edge. The Builder does not change until
// [Builder.Commit].
//
// Returns [ErrLeafSize] for a leaf whose size is not the digest size of
// the Builder's hasher, and [ErrRange] when the tree would pass 2^64 - 1
// leaves. u is empty after an error.
//
// # Allocation contract
//
// Zero alloc once u's buffers have grown to the batch size.
func (b *Builder) Integrate(leaves []crypto.Digest, u *Update) error {
	u.reset(b)

	if uint64(len(leaves)) > math.MaxUint64-b.size {
		return ErrRange
	}
	for _, leaf := range leaves {
		if leaf.Size() != b.digestSize {
			return ErrLeafSize
		}
	}
	if len(leaves) == 0 {
		return nil
	}

	ds := b.digestSize
	tileBytes := tileWidth * ds
	in := u.carry[0][:0]
	s := b.h.NewStream()
	defer s.Close()

	for level := range tileLevels {
		if level > 0 && len(in) == 0 {
			break
		}

		start := u.data.Len()
		u.data.Append(b.edge[level])
		if level == 0 {
			for _, leaf := range leaves {
				u.data.Append(leaf.Bytes())
			}
		} else {
			u.data.Append(in)
		}

		count := (u.data.Len() - start) / ds
		first := (b.size >> (tileHeight * uint(level))) >> tileHeight
		out := u.carry[(level+1)%2][:0]

		full := count / tileWidth
		for i := range full {
			off := start + i*tileBytes
			u.tiles = append(u.tiles, placedTile{
				tile: Tile{Level: uint8(level), Index: first + uint64(i), Width: tileWidth},
				off:  off,
			})
			d := subtreeRoot(s, ds, u.data.Bytes()[off:off+tileBytes], b.scratch)
			out = append(out, d.Bytes()...)
		}

		u.edge[level] = region{}
		if w := count % tileWidth; w > 0 {
			off := start + full*tileBytes
			u.tiles = append(u.tiles, placedTile{
				//nolint:gosec // G115: full counts tiles and is not negative
				tile: Tile{Level: uint8(level), Index: first + uint64(full), Width: uint16(w)},
				off:  off,
			})
			u.edge[level] = region{off, off + w*ds}
		}

		u.carry[(level+1)%2] = out
		in = out
		u.levels = level + 1
	}

	var edge [tileLevels][]byte
	data := u.data.Bytes()
	for level := range edge {
		edge[level] = b.edge[level]
		if level < u.levels {
			edge[level] = data[u.edge[level].lo:u.edge[level].hi]
		}
	}

	u.size = b.size + uint64(len(leaves))
	u.root = rootOf(s, ds, &edge, b.scratch)

	return nil
}

// Commit makes u's tree the Builder's tree. A writer calls it after
// every tile of u is durable.
//
// Returns [ErrStale] when u was not computed from the Builder's current
// tree.
//
// # Allocation contract
//
// Zero alloc.
func (b *Builder) Commit(u *Update) error {
	if u.b != b || u.base != b.size {
		return ErrStale
	}

	data := u.data.Bytes()
	for level := range u.levels {
		s := u.edge[level]
		b.edge[level] = append(b.edge[level][:0], data[s.lo:s.hi]...)
	}
	b.size, b.root = u.size, u.root

	return nil
}

// rootOf returns the root of the tree whose right edge is edge, the
// partial tile at each level, hashing through s. The tree has at least
// one leaf.
func rootOf(s crypto.Stream, ds int, edge *[tileLevels][]byte, scratch []byte) crypto.Digest {
	// The perfect subtrees of the tree, largest first: the bits of the
	// size, read from the partial tile of the highest level down.
	var stack [64]crypto.Digest

	n := 0
	for i := range tileLevels {
		p := edge[tileLevels-1-i]
		count, off := len(p)/ds, 0
		for j := range tileHeight {
			bit := tileHeight - 1 - j
			if count&(1<<bit) == 0 {
				continue
			}
			m := (1 << bit) * ds
			//nolint:gosec // G602: eight levels of eight bits, so n stays below 64
			stack[n] = subtreeRoot(s, ds, p[off:off+m], scratch)
			n++
			off += m
		}
	}

	//nolint:gosec // G602: the tree has a leaf, so n is at least one
	root := stack[n-1]
	for k := n - 2; k >= 0; k-- {
		root = nodeHash(s, scratch, stack[k], root)
	}

	return root
}

// Update is the result of one [Builder.Integrate] call. Its zero value
// is ready to use, and reusing one across batches reuses its buffers.
//
// # Concurrency
//
// Not safe for concurrent use.
type Update struct {
	b *Builder

	// carry collects the roots of the full tiles of one level, which are
	// the new hashes of the level above. Levels alternate between the
	// two.
	carry [2][]byte

	tiles []placedTile

	// data contains the bytes of every tile, in the order of tiles.
	data arena.Arena

	root crypto.Digest
	base uint64
	size uint64

	// edge is the byte range in data of the new partial tile at each
	// level below levels. An empty range means the level ends on a full
	// tile.
	edge   [tileLevels]region
	levels int
}

// region is the byte range [lo, hi) of a partial tile in [Update.data].
type region struct {
	lo, hi int
}

// placedTile is a tile and the offset of its data in [Update.data].
type placedTile struct {
	tile Tile
	off  int
}

// reset empties u for an integration into b's tree.
func (u *Update) reset(b *Builder) {
	u.b, u.base, u.size, u.root = b, b.size, b.size, b.root
	u.data.Reset()
	u.tiles = u.tiles[:0]
	u.levels = 0
}

// Size returns the number of leaves in the tree the update produces.
func (u *Update) Size() uint64 { return u.size }

// Root returns the Merkle Tree Hash of the tree the update produces.
func (u *Update) Root() crypto.Digest { return u.root }

// Tiles iterates over the tiles to write and their data, level by level
// and within a level by index. The data is valid until the next
// [Builder.Integrate] into u.
//
// # Allocation contract
//
// One closure per call. Iterating allocates nothing.
func (u *Update) Tiles() iter.Seq2[Tile, []byte] {
	return func(yield func(Tile, []byte) bool) {
		if u.b == nil {
			return
		}

		data := u.data.Bytes()
		for _, p := range u.tiles {
			end := p.off + int(p.tile.Width)*u.b.digestSize
			if !yield(p.tile, data[p.off:end:end]) {
				return
			}
		}
	}
}
