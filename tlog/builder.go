// Copyright ThesmOS B.V. 2026
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
// [Builder.Reset] gives a Builder another stored tree in the same state, so
// a writer that integrates the trees of many logs keeps a bounded number of
// Builders.
//
// # Concurrency
//
// Not safe for concurrent use. One writer integrates.
//
// # Allocation contract
//
// [Builder.Integrate] into a reused [Update] allocates nothing once the
// Update's buffers have grown to the batch size. Node hashes fold in
// scratch the Builder allocates once. A Reset keeps the state, unless the
// digests of its hasher need more room.
type Builder struct {
	h crypto.Hasher

	// state is the memory of scratch and of the edges: the scratch, then the
	// room of the edge of each level.
	state []byte

	// edge is the partial tile at each level: the last (size >> 8L) %
	// 256 hashes of tree level 8L.
	edge [tileLevels][]byte

	// scratch has room for one full tile and the input of one node hash,
	// for folding tiles into roots.
	scratch []byte

	// dst and tiles are the list of buffers and the list of tiles that Reset
	// passes to ReadTiles. The Builder keeps them, so neither list escapes
	// through the call. dst does not refer to a buffer between two calls.
	dst   [tileLevels][]byte
	tiles [tileLevels]Tile

	root crypto.Digest
	size uint64

	// resets is the number of calls of Reset. An Update keeps the count of
	// its integration, and Commit refuses an Update whose count differs.
	resets uint64

	// digestSize is the digest size of h.
	digestSize int
}

// NewBuilder returns a Builder for the tree of the given size, stored in
// r. It reads the partial tiles at the tree's right edge in one
// [TileReader.ReadTiles] call, so a writer resumes after a restart from
// the last published size. A size of zero does not read. NewBuilder
// allocates a Builder and calls [Builder.Reset].
//
// Returns [ErrTileSize] when a tile read has the wrong length, and the
// error of ReadTiles when it fails.
//
// # Allocation contract
//
// Allocates the Builder and its state: its right edge and one tile of
// scratch. Beyond that it allocates what ReadTiles allocates, as Reset
// states.
func NewBuilder(ctx context.Context, h crypto.Hasher, size uint64, r TileReader) (*Builder, error) {
	b := new(Builder)
	if err := b.Reset(ctx, h, size, r); err != nil {
		return nil, err
	}

	return b, nil
}

// Reset makes b the Builder of the tree of the given size stored in r, as
// NewBuilder returns it for h, size and r. It reads the partial tiles at the
// tree's right edge in one [TileReader.ReadTiles] call, and passes it a
// buffer for each tile with room for the widest partial tile. A size of zero
// does not read. Every Update that b computed before Reset is stale, so
// Commit returns [ErrStale] for it.
//
// Each buffer is the edge of one level of the state of b. A ReadTiles that
// appends within the room of the buffer writes the tile into the state.
// Reset copies a tile only when ReadTiles returns it in another buffer, and
// does not keep a buffer that ReadTiles returns.
//
// Returns [ErrTileSize] when a tile read has the wrong length, and the error
// of ReadTiles when it fails. b has the tree of size zero after an error.
//
// # Allocation contract
//
// Allocates only when the digests of h are larger than the digests that the
// state of b has room for. Beyond that it allocates what ReadTiles
// allocates, which is nothing for a ReadTiles that appends each tile within
// the room of its buffer.
func (b *Builder) Reset(ctx context.Context, h crypto.Hasher, size uint64, r TileReader) error {
	empty := h.Hash(nil)
	ds := empty.Size()

	// The state is the scratch of one tile and one node hash, then the room
	// of the edge of each level. Only the Builder and the ReadTiles of Reset
	// write an edge, and never past its room.
	scratch := tileWidth*ds + nodeRoom
	room := maxPartialWidth * ds
	if need := scratch + tileLevels*room; len(b.state) < need {
		b.state = make([]byte, need)
	}

	b.h, b.digestSize, b.root, b.size = h, ds, empty, 0
	//dokimi:mutate-skip uoi-incdec: a decrement changes the count as an increment does, so Commit refuses the same Updates
	b.resets++
	b.scratch = b.state[:scratch:scratch]
	for level := range b.edge {
		off := scratch + level*room
		b.edge[level] = b.state[off : off : off+room]
	}

	if size == 0 {
		return nil
	}

	n := 0
	for level := range uint8(tileLevels) {
		row := size >> (tileHeight * uint(level))
		if w := row % tileWidth; w > 0 {
			b.tiles[n] = Tile{Level: level, Index: row >> tileHeight, Width: uint16(w)}
			b.dst[n] = b.edge[level]
			n++
		}
	}
	defer clear(b.dst[:n])

	if err := r.ReadTiles(ctx, b.tiles[:n], b.dst[:n]); err != nil {
		return err
	}

	for i, t := range b.tiles[:n] {
		if len(b.dst[i]) != int(t.Width)*ds {
			return ErrTileSize
		}
	}

	// copy moves no byte for a tile that ReadTiles appended within the room
	// of its buffer, because the tile and the edge then start at one address.
	for i, t := range b.tiles[:n] {
		b.edge[t.Level] = b.edge[t.Level][:len(b.dst[i])]
		copy(b.edge[t.Level], b.dst[i])
	}

	b.size = size
	b.root = rootOf(h, ds, &b.edge, b.scratch)

	return nil
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
			d := subtreeRoot(b.h, ds, u.data.Bytes()[off:off+tileBytes], b.scratch)
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
	u.root = rootOf(b.h, ds, &edge, b.scratch)

	return nil
}

// Commit makes u's tree the Builder's tree. A writer calls it after
// every tile of u is durable.
//
// Returns [ErrStale] when u was not computed from the Builder's current
// tree, such as an Update from before a [Builder.Reset].
//
// # Allocation contract
//
// Zero alloc.
func (b *Builder) Commit(u *Update) error {
	if u.b != b || u.resets != b.resets || u.base != b.size {
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
// partial tile at each level, hashing with h. The tree has at least one
// leaf. scratch has room for half a tile and the input of one node hash.
func rootOf(h crypto.Hasher, ds int, edge *[tileLevels][]byte, scratch []byte) crypto.Digest {
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
			stack[n] = subtreeRoot(h, ds, p[off:off+m], scratch)
			n++
			off += m
		}
	}

	//nolint:gosec // G602: the tree has a leaf, so n is at least one
	root := stack[n-1]
	for k := n - 2; k >= 0; k-- {
		root = nodeHash(h, scratch, stack[k], root)
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

	// resets is the count of Resets of b at the integration, and ds the
	// digest size of b then. Tiles reads ds, so a Reset of b leaves the
	// tiles of u as they are.
	resets uint64
	ds     int

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

// Size returns the number of leaves in the tree the update produces.
func (u *Update) Size() uint64 { return u.size }

// Root returns the Merkle Tree Hash of the tree the update produces.
func (u *Update) Root() crypto.Digest { return u.root }

// Tiles iterates over the tiles to write and their data, level by level
// and within a level by index. The data is valid until the next
// [Builder.Integrate] into u, and a [Builder.Reset] does not change it.
//
// # Allocation contract
//
// One closure per call. Iterating allocates nothing.
func (u *Update) Tiles() iter.Seq2[Tile, []byte] {
	return func(yield func(Tile, []byte) bool) {
		data := u.data.Bytes()
		for _, p := range u.tiles {
			end := p.off + int(p.tile.Width)*u.ds
			if !yield(p.tile, data[p.off:end:end]) {
				return
			}
		}
	}
}

// reset empties u for an integration into b's tree.
func (u *Update) reset(b *Builder) {
	u.b, u.resets, u.ds = b, b.resets, b.digestSize
	u.base, u.size, u.root = b.size, b.size, b.root
	u.data.Reset()
	u.tiles = u.tiles[:0]
	u.levels = 0
}
